//! 混音管理器
//!
//! 管理 room 级别的音频混音（MCU N-1 架构）。
//! 将 push_rtp → 解码 → ConferenceMixer → 编码 → pull_rtp 串联起来。
//!
//! 数据流：
//! ```text
//!   A push Opus → 解码 → PCM → Mixer ─┐
//!   B push Opus → 解码 → PCM → Mixer ─┤
//!                                     ├→ 混音 → 编码 → Opus → A pull (mix track)
//!                                     └→ 混音 → 编码 → Opus → B pull (mix track)
//! ```
//!
//! 每个参与者在 AddMixParticipant 时：
//! 1. 创建一个 mix-output track（broadcast channel）
//! 2. 注册到 ConferenceMixer，获得 (input_tx, output_rx)
//! 3. 启动 egress bridge：mixer output_rx → 编码Opus → RtpPacketOut → track broadcast

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use dashmap::DashMap;
use lm_core::{CodecType, SessionId, TrackId, TrackKind};
use lm_mixer::ConferenceMixer;
use tokio::sync::mpsc;
use tracing::info;

use crate::session::{MediaSession, RtpPacketOut, TrackState};

/// 混音状态（per mix / per room）
pub struct MixState {
    pub mix_id: String,
    pub room_id: String,
    pub mixer: Arc<ConferenceMixer>,
    /// 混音采样率（用于 egress bridge 创建编码器）
    pub sample_rate: u32,
    /// 输出编码：opus（浏览器）或 pcmu（SIP/极致性能）
    pub output_codec: String,
    /// session_id → 参与者混音状态
    pub participants: DashMap<SessionId, ParticipantMixState>,
}

/// 单个参与者在混音中的状态
pub struct ParticipantMixState {
    pub session_id: SessionId,
    /// 源音频 track ID（push_rtp 热路径查找用）
    pub source_track_id: TrackId,
    /// 向混音器发送该参与者的 PCM 帧
    pub mixer_input_tx: mpsc::Sender<lm_core::AudioFrame>,
    /// 混音输出 track ID（pull_rtp 订阅此 track 获取混音）
    pub mix_track_id: TrackId,
    /// 停止标志（用于清理 bridge task）
    pub stopped: Arc<AtomicBool>,
}

/// 混音管理器
pub struct MixManager {
    /// mix_id → MixState
    mixes: DashMap<String, Arc<MixState>>,
    /// (session_id, source_track_id) → mixer_input_tx
    /// push_rtp 热路径查这个表：如果在表中，走混音路径；否则走 SFU 转发。
    track_mix_inputs: DashMap<(SessionId, TrackId), mpsc::Sender<lm_core::AudioFrame>>,
    /// session_id → mix_id（快速查找 session 属于哪个 mix）
    session_mix: DashMap<SessionId, String>,
}

impl Default for MixManager {
    fn default() -> Self {
        Self::new()
    }
}

impl MixManager {
    pub fn new() -> Self {
        Self {
            mixes: DashMap::new(),
            track_mix_inputs: DashMap::new(),
            session_mix: DashMap::new(),
        }
    }

    /// 启动混音（per room）
    ///
    /// max_speakers: Top-K 最大发言者数（0=混所有人，5=只混Top5）
    pub fn start_mix(
        &self,
        room_id: &str,
        sample_rate: u32,
        max_speakers: usize,
        output_codec: &str,
    ) -> Arc<MixState> {
        let mix_id = uuid::Uuid::new_v4().to_string();
        let mixer = Arc::new(ConferenceMixer::with_max_speakers(&mix_id, sample_rate, max_speakers));
        mixer.start();

        let state = Arc::new(MixState {
            mix_id: mix_id.clone(),
            room_id: room_id.to_string(),
            mixer,
            sample_rate,
            output_codec: output_codec.to_string(),
            participants: DashMap::new(),
        });

        self.mixes.insert(mix_id.clone(), state.clone());
        info!(mix_id = %mix_id, room = %room_id, sample_rate, output_codec, "mix started");
        state
    }

    /// 停止混音
    pub fn stop_mix(&self, mix_id: &str) -> Result<(), String> {
        let (_, state) = self
            .mixes
            .remove(mix_id)
            .ok_or_else(|| format!("mix {} not found", mix_id))?;

        // 停止所有 bridge tasks
        let session_ids: Vec<SessionId> =
            state.participants.iter().map(|p| p.session_id.clone()).collect();
        for entry in state.participants.iter() {
            entry.stopped.store(true, Ordering::Relaxed);
        }

        // 清理 maps
        for sid in &session_ids {
            self.session_mix.remove(sid);
        }
        self.track_mix_inputs
            .retain(|(s, _), _| !session_ids.contains(s));

        state.mixer.stop();
        info!(mix_id = %mix_id, "mix stopped");
        Ok(())
    }

    /// 添加参与者到混音
    ///
    /// - 创建 mix-output track 并加入 session
    /// - 注册到 ConferenceMixer
    /// - 启动 egress bridge：mixer output → 编码 → track broadcast
    pub async fn add_participant(
        &self,
        mix_id: &str,
        session: &Arc<MediaSession>,
        source_track_id: &TrackId,
        sample_rate: u32,
        output_codec: &str,
    ) -> Result<TrackId, String> {
        let mix_state = self
            .mixes
            .get(mix_id)
            .map(|m| m.clone())
            .ok_or_else(|| format!("mix {} not found", mix_id))?;

        let session_id = session.id.clone();

        // 1. 注册到 ConferenceMixer，获得 (input_tx, output_rx)
        let (mixer_input_tx, mixer_output_rx) = mix_state
            .mixer
            .add_participant(session_id.0.clone())
            .await
            .map_err(|e| e.to_string())?;

        // 2. 创建 mix-output track（broadcast channel）
        let mix_track_id = TrackId(format!("mix-{}", session_id.0));
        let track_codec = if output_codec == "pcmu" {
            CodecType::PcmU
        } else {
            CodecType::Opus
        };
        let mix_track = TrackState::new(
            mix_track_id.clone(),
            lm_core::EndpointId(format!("mix-ep-{}", session_id.0)),
            session_id.clone(),
            track_codec,
            TrackKind::Audio,
            0,
        );
        let broadcast_tx = mix_track.rtp_broadcast.clone();
        session.tracks.insert(mix_track_id.clone(), Arc::new(mix_track));

        // 3. 启动 egress bridge task
        let stopped = Arc::new(AtomicBool::new(false));
        let stopped_clone = stopped.clone();
        let mix_id_for_task = mix_id.to_string();
        let session_id_for_task = session_id.0.clone();
        let codec_for_task = output_codec.to_string();

        tokio::spawn(async move {
            run_egress_bridge(
                mixer_output_rx,
                broadcast_tx,
                sample_rate,
                codec_for_task,
                stopped_clone,
                mix_id_for_task,
                session_id_for_task,
            )
            .await;
        });

        // 4. 存储 participant state
        mix_state.participants.insert(
            session_id.clone(),
            ParticipantMixState {
                session_id: session_id.clone(),
                source_track_id: source_track_id.clone(),
                mixer_input_tx: mixer_input_tx.clone(),
                mix_track_id: mix_track_id.clone(),
                stopped,
            },
        );

        // 5. 注册 track_mix_inputs（push_rtp 热路径查这个表）
        self.track_mix_inputs
            .insert((session_id.clone(), source_track_id.clone()), mixer_input_tx);
        self.session_mix.insert(session_id.clone(), mix_id.to_string());

        info!(
            mix_id = %mix_id,
            session = %session_id.0,
            source_track = %source_track_id.0,
            mix_track = %mix_track_id.0,
            "mix participant added"
        );

        Ok(mix_track_id)
    }

    /// 移除参与者
    pub async fn remove_participant(
        &self,
        mix_id: &str,
        session: &Arc<MediaSession>,
        source_track_id: &TrackId,
    ) -> Result<(), String> {
        let mix_state = self
            .mixes
            .get(mix_id)
            .map(|m| m.clone())
            .ok_or_else(|| format!("mix {} not found", mix_id))?;

        let session_id = session.id.clone();

        // 停止 bridge task
        if let Some(p) = mix_state.participants.get(&session_id) {
            p.stopped.store(true, Ordering::Relaxed);
        }

        // 从 mixer 移除
        let _ = mix_state.mixer.remove_participant(&session_id.0).await;

        // 移除 mix-output track
        let mix_track_id = TrackId(format!("mix-{}", session_id.0));
        session.tracks.remove(&mix_track_id);

        // 清理 maps
        mix_state.participants.remove(&session_id);
        self.track_mix_inputs
            .remove(&(session_id.clone(), source_track_id.clone()));
        self.session_mix.remove(&session_id);

        info!(
            mix_id = %mix_id,
            session = %session_id.0,
            "mix participant removed"
        );
        Ok(())
    }

    /// 设置 per-route 增益
    pub fn set_route_gain(
        &self,
        mix_id: &str,
        src_session: &str,
        dst_session: &str,
        gain: f32,
    ) -> Result<(), String> {
        let mix_state = self
            .mixes
            .get(mix_id)
            .map(|m| m.clone())
            .ok_or_else(|| format!("mix {} not found", mix_id))?;
        mix_state.mixer.set_route_gain(src_session, dst_session, gain);
        Ok(())
    }

    /// 查找 track 的混音 input channel（push_rtp 热路径调用）
    pub fn get_mix_input(
        &self,
        session_id: &SessionId,
        track_id: &TrackId,
    ) -> Option<mpsc::Sender<lm_core::AudioFrame>> {
        self.track_mix_inputs
            .get(&(session_id.clone(), track_id.clone()))
            .map(|tx| tx.clone())
    }

    /// session 是否在某个 mix 中
    pub fn session_in_mix(&self, session_id: &SessionId) -> bool {
        self.session_mix.contains_key(session_id)
    }

    /// mix 数量
    pub fn mix_count(&self) -> usize {
        self.mixes.len()
    }

    /// 获取 mix 的参与者数量
    pub fn mix_participant_count(&self, mix_id: &str) -> Option<usize> {
        self.mixes.get(mix_id).map(|m| m.participants.len())
    }

    /// 获取 mix 的采样率
    pub fn mix_sample_rate(&self, mix_id: &str) -> Option<u32> {
        self.mixes.get(mix_id).map(|m| m.sample_rate)
    }

    /// 获取 mix 的输出编码
    pub fn mix_output_codec(&self, mix_id: &str) -> Option<String> {
        self.mixes.get(mix_id).map(|m| m.output_codec.clone())
    }

    /// 获取参与者的源 track ID
    pub fn participant_source_track_id(
        &self,
        mix_id: &str,
        session_id: &SessionId,
    ) -> Option<TrackId> {
        self.mixes
            .get(mix_id)
            .and_then(|m| m.participants.get(session_id).map(|p| p.source_track_id.clone()))
    }

    /// 获取 mix 中所有参与者的 session_id
    pub fn mix_participant_sessions(&self, mix_id: &str) -> Vec<SessionId> {
        self.mixes
            .get(mix_id)
            .map(|m| {
                m.participants
                    .iter()
                    .map(|p| p.session_id.clone())
                    .collect()
            })
            .unwrap_or_default()
    }
}

/// Egress bridge：mixer output → 编码 → track broadcast
///
/// 从 ConferenceMixer 的 output_rx 接收混音后的 PCM 帧，
/// 根据 output_codec 编码为 Opus 或 PCMU，包装成 RtpPacketOut 发送到 broadcast channel。
///
/// 优化：
/// - 静音跳过：如果混音帧全为 0（无 active speaker），不编码不发送
/// - PCMU 模式：µ-law 查表编码，无状态、零成本
/// - Opus 模式：per-bridge 独立编码器（保留帧间预测状态）
async fn run_egress_bridge(
    mut mixer_output_rx: mpsc::Receiver<lm_core::AudioFrame>,
    broadcast_tx: tokio::sync::broadcast::Sender<RtpPacketOut>,
    sample_rate: u32,
    output_codec: String,
    stopped: Arc<AtomicBool>,
    mix_id: String,
    session_id: String,
) {
    // 根据输出 codec 创建编码器
    let is_opus = output_codec != "pcmu";
    let mut opus_encoder: Option<Box<dyn audio_codec::Encoder>> = if is_opus {
        Some(audio_codec::create_opus_encoder(
            sample_rate,
            1,
            audio_codec::opus::OpusApplication::Voip,
        ))
    } else {
        None
    };
    let pcmu_encoder = if !is_opus {
        Some(audio_codec::pcmu::PcmuEncoder::new())
    } else {
        None
    };

    let payload_type: u32 = if is_opus { 111 } else { 0 }; // Opus=111, PCMU=0
    let mut rtp_seq: u32 = 0;
    let mut rtp_ts: u32 = 0;
    let frame_samples = (sample_rate as usize * 20) / 1000;
    let clock_increment = frame_samples as u32;
    let mix_ssrc: u32 = 0x4D495800; // "MIX\0"

    // 静音检测阈值：RMS 能量低于此值则跳过编码
    const SILENCE_THRESHOLD: f32 = 50.0; // i16 RMS

    info!(
        mix_id = %mix_id,
        session = %session_id,
        sample_rate,
        output_codec = %output_codec,
        "egress bridge started"
    );

    loop {
        if stopped.load(Ordering::Relaxed) {
            info!(mix_id = %mix_id, session = %session_id, "egress bridge stopped");
            break;
        }

        match mixer_output_rx.recv().await {
            Some(frame) => {
                // 静音跳过：计算 RMS，低于阈值则不编码
                let rms = rms_energy_fast(&frame.samples);
                if rms < SILENCE_THRESHOLD {
                    // 仍然推进时间戳，但不发送数据包
                    rtp_seq = rtp_seq.wrapping_add(1);
                    rtp_ts = rtp_ts.wrapping_add(clock_increment);
                    continue;
                }

                let encoded = if let Some(ref mut enc) = opus_encoder {
                    enc.encode(&frame.samples)
                } else if pcmu_encoder.is_some() {
                    // PCMU: 直接查表编码
                    let mut buf = vec![0u8; frame.samples.len()];
                    for (i, &s) in frame.samples.iter().enumerate() {
                        buf[i] = audio_codec::pcmu::linear_to_ulaw(s);
                    }
                    buf
                } else {
                    continue;
                };

                if encoded.is_empty() {
                    continue;
                }

                let pkt = RtpPacketOut {
                    ssrc: mix_ssrc,
                    payload_type,
                    sequence_number: rtp_seq,
                    timestamp: rtp_ts,
                    marker: rtp_seq == 0,
                    payload: bytes::Bytes::from(encoded),
                    rid: String::new(),
                    clock_rate: sample_rate,
                };

                let _ = broadcast_tx.send(pkt);

                rtp_seq = rtp_seq.wrapping_add(1);
                rtp_ts = rtp_ts.wrapping_add(clock_increment);
            }
            None => {
                info!(
                    mix_id = %mix_id,
                    session = %session_id,
                    "mixer output closed, stopping egress bridge"
                );
                break;
            }
        }
    }
}

/// 快速 RMS 能量计算（用于静音检测）
fn rms_energy_fast(samples: &[i16]) -> f32 {
    if samples.is_empty() {
        return 0.0;
    }
    let sum: i64 = samples.iter().map(|s| (*s as i64) * (*s as i64)).sum();
    ((sum as f64 / samples.len() as f64).sqrt()) as f32
}
