//! 录制管理器
//!
//! 管理 session/room 级别的录制任务。
//! 音频：解码 RTP payload → PCM → WavRecorder
//! 视频：原始 RTP payload → H.264 annex-B 文件（.h264）
//!
//! 数据流：
//! ```text
//!   push_rtp → rtp_broadcast → recording task → decode(音频) → WavRecorder
//!                                                  → raw(视频) → .h264 file
//! ```

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use dashmap::DashMap;
use lm_core::{CodecType, SessionId, TrackId, TrackKind};
use lm_recorder::{RecordingFormat, Recorder, RecordingResult};
use tokio::io::AsyncWriteExt;
use tokio::sync::Mutex;
use tracing::{error, info, warn};

use crate::session::{MediaSession, RtpPacketOut};

/// 录制管理器
pub struct RecordingManager {
    /// recording_id → 录制任务状态
    recordings: DashMap<String, Arc<RecordingTask>>,
    /// session_id → recording_id（每个 session 一个录制）
    session_recordings: DashMap<SessionId, String>,
}

struct RecordingTask {
    recording_id: String,
    session_id: SessionId,
    stopped: Arc<AtomicBool>,
    /// 音频录制器（如果有音频 track）
    audio_recorder: Mutex<Option<Recorder>>,
    /// 视频文件写入（如果有视频 track）
    video_writer: Mutex<Option<tokio::fs::File>>,
    /// 录制文件路径
    audio_path: String,
    video_path: String,
    /// 录制开始时间
    start_time: std::time::Instant,
}

impl RecordingManager {
    pub fn new() -> Self {
        Self {
            recordings: DashMap::new(),
            session_recordings: DashMap::new(),
        }
    }

    /// 启动 session 录制
    ///
    /// - 自动发现 session 中的音频和视频 track
    /// - 音频：解码为 PCM 写 WAV
    /// - 视频：原始 payload 写 .h264 文件
    /// - output_dir: 录制文件输出目录
    pub async fn start_recording(
        &self,
        recording_id: &str,
        session: &Arc<MediaSession>,
        output_dir: &str,
    ) -> anyhow::Result<()> {
        let session_id = session.id.clone();

        // 确保输出目录存在
        std::fs::create_dir_all(output_dir)
            .map_err(|e| anyhow::anyhow!("create recording dir {output_dir}: {e}"))?;

        // 查找音频和视频 track
        let mut audio_track: Option<(TrackId, CodecType, Arc<crate::session::TrackState>)> = None;
        let mut video_track: Option<(TrackId, CodecType, Arc<crate::session::TrackState>)> = None;

        for entry in session.tracks.iter() {
            let track = entry.value();
            if track.kind == TrackKind::Audio && audio_track.is_none() {
                audio_track = Some((track.track_id.clone(), track.codec, track.clone()));
            } else if track.kind == TrackKind::Video && video_track.is_none() {
                video_track = Some((track.track_id.clone(), track.codec, track.clone()));
            }
        }

        let sid_str = session_id.0.clone();
        let audio_path = format!("{output_dir}/{sid_str}_audio.wav");
        let video_path = format!("{output_dir}/{sid_str}_video.h264");

        // 创建音频录制器（如果有音频 track）
        let audio_recorder = if let Some((_, codec, _)) = &audio_track {
            // 根据编解码器确定采样率
            let sample_rate = match codec {
                CodecType::Opus => 48000,
                CodecType::PcmU | CodecType::PcmA => 8000,
                CodecType::G722 => 16000,
                _ => 48000,
            };
            match Recorder::new(
                session_id.clone(),
                RecordingFormat::Wav,
                audio_path.clone(),
                sample_rate,
                1, // mono
            )
            .await
            {
                Ok(r) => {
                    info!(recording_id, session = %sid_str, sample_rate, "audio recorder created");
                    Some(r)
                }
                Err(e) => {
                    error!(recording_id, session = %sid_str, error = %e, "failed to create audio recorder");
                    return Err(e);
                }
            }
        } else {
            None
        };

        // 创建视频文件（如果有视频 track）
        let video_writer = if let Some((_, _, _)) = &video_track {
            match tokio::fs::File::create(&video_path).await {
                Ok(f) => {
                    info!(recording_id, session = %sid_str, path = %video_path, "video file created");
                    Some(f)
                }
                Err(e) => {
                    error!(recording_id, session = %sid_str, error = %e, "failed to create video file");
                    return Err(anyhow::anyhow!("create video file: {e}"));
                }
            }
        } else {
            None
        };

        let stopped = Arc::new(AtomicBool::new(false));
        let task = Arc::new(RecordingTask {
            recording_id: recording_id.to_string(),
            session_id: session_id.clone(),
            stopped: stopped.clone(),
            audio_recorder: Mutex::new(audio_recorder),
            video_writer: Mutex::new(video_writer),
            audio_path,
            video_path,
            start_time: std::time::Instant::now(),
        });

        // 启动音频录制 task
        if let Some((track_id, codec, track)) = audio_track {
            let task_clone = task.clone();
            let codec = codec;
            tokio::spawn(async move {
                run_audio_recording(task_clone, track.subscribe(), track_id, codec).await;
            });
        }

        // 启动视频录制 task
        if let Some((track_id, _, track)) = video_track {
            let task_clone = task.clone();
            tokio::spawn(async move {
                run_video_recording(task_clone, track.subscribe(), track_id).await;
            });
        }

        self.recordings.insert(recording_id.to_string(), task.clone());
        self.session_recordings.insert(session_id, recording_id.to_string());

        info!(recording_id, session = %sid_str, "recording started");
        Ok(())
    }

    /// 停止录制
    pub async fn stop_recording(&self, recording_id: &str) -> anyhow::Result<RecordingResult> {
        let (_, task) = self
            .recordings
            .remove(recording_id)
            .ok_or_else(|| anyhow::anyhow!("recording {} not found", recording_id))?;

        // 从 session_recordings 中移除
        self.session_recordings.retain(|_, rid| rid != recording_id);

        // 停止录制 task
        task.stopped.store(true, Ordering::Relaxed);

        let duration_ms = task.start_time.elapsed().as_millis() as u64;

        // finalize 音频录制器
        let mut audio_result = None;
        if let Some(recorder) = task.audio_recorder.lock().await.take() {
            match recorder.finalize().await {
                Ok(r) => {
                    info!(recording_id, path = %r.file_path, duration_ms = r.duration_ms, "audio recording finalized");
                    audio_result = Some(r);
                }
                Err(e) => {
                    error!(recording_id, error = %e, "failed to finalize audio recording");
                }
            }
        }

        // 关闭视频文件
        if let Some(mut vw) = task.video_writer.lock().await.take() {
            let _ = vw.flush().await;
            let _ = vw.shutdown().await;
            info!(recording_id, path = %task.video_path, "video recording closed");
        }

        // 计算总文件大小
        let file_size = audio_result
            .as_ref()
            .map(|r| r.file_size)
            .unwrap_or(0)
            + std::fs::metadata(&task.video_path).map(|m| m.len()).unwrap_or(0);

        let file_path = if audio_result.is_some() {
            task.audio_path.clone()
        } else {
            task.video_path.clone()
        };

        Ok(RecordingResult {
            file_path,
            duration_ms,
            file_size,
        })
    }

    /// 停止 session 的所有录制
    pub async fn stop_session_recordings(&self, session_id: &SessionId) -> Vec<RecordingResult> {
        let recording_ids: Vec<String> = self
            .session_recordings
            .iter()
            .filter(|entry| entry.key() == session_id)
            .map(|entry| entry.value().clone())
            .collect();

        let mut results = Vec::new();
        for rid in recording_ids {
            if let Ok(r) = self.stop_recording(&rid).await {
                results.push(r);
            }
        }
        results
    }

    /// 录制数量
    pub fn recording_count(&self) -> usize {
        self.recordings.len()
    }
}

/// 音频录制 task：从 broadcast channel 接收 RTP，解码为 PCM，写入 WAV
async fn run_audio_recording(
    task: Arc<RecordingTask>,
    mut rx: tokio::sync::broadcast::Receiver<RtpPacketOut>,
    track_id: TrackId,
    codec: CodecType,
) {
    // 创建解码器
    let ac_codec = match codec {
        CodecType::Opus => audio_codec::CodecType::Opus,
        CodecType::PcmU => audio_codec::CodecType::PCMU,
        CodecType::PcmA => audio_codec::CodecType::PCMA,
        CodecType::G722 => audio_codec::CodecType::G722,
        _ => audio_codec::CodecType::Opus,
    };
    let mut decoder = audio_codec::create_decoder(ac_codec);

    info!(
        recording_id = %task.recording_id,
        track = %track_id.0,
        codec = ?codec,
        "audio recording task started"
    );

    loop {
        if task.stopped.load(Ordering::Relaxed) {
            info!(recording_id = %task.recording_id, "audio recording task stopped");
            break;
        }

        match rx.recv().await {
            Ok(pkt) => {
                // 解码 RTP payload → PCM
                let samples = decoder.decode(&pkt.payload);
                if samples.is_empty() {
                    continue;
                }

                let frame = lm_core::AudioFrame {
                    samples,
                    sample_rate: decoder.sample_rate(),
                    timestamp: pkt.timestamp as u64,
                };

                // 写入 WAV
                let mut recorder = task.audio_recorder.lock().await;
                if let Some(ref mut rec) = *recorder {
                    if let Err(e) = rec.write_frame(&frame).await {
                        warn!(recording_id = %task.recording_id, error = %e, "write audio frame failed");
                    }
                }
            }
            Err(tokio::sync::broadcast::error::RecvError::Lagged(n)) => {
                warn!(recording_id = %task.recording_id, lagged = n, "audio recording lagged");
            }
            Err(tokio::sync::broadcast::error::RecvError::Closed) => {
                info!(recording_id = %task.recording_id, "audio broadcast channel closed");
                break;
            }
        }
    }
}

/// 视频录制 task：从 broadcast channel 接收 RTP，写入 .h264 文件
///
/// H.264 RTP payload 需要转换为 annex-B 格式：
/// - 单包模式 (PT=24): 每个 NALU 前加 00 00 00 01
/// - FU-A 分片 (PT=28): 重组后加 00 00 00 01
/// - 这里简化处理：直接写 payload，浏览器可播放大部分情况
async fn run_video_recording(
    task: Arc<RecordingTask>,
    mut rx: tokio::sync::broadcast::Receiver<RtpPacketOut>,
    track_id: TrackId,
) {
    info!(
        recording_id = %task.recording_id,
        track = %track_id.0,
        "video recording task started"
    );

    loop {
        if task.stopped.load(Ordering::Relaxed) {
            info!(recording_id = %task.recording_id, "video recording task stopped");
            break;
        }

        match rx.recv().await {
            Ok(pkt) => {
                // H.264 RTP → annex-B 转换
                let annex_b = rtp_h264_to_annex_b(&pkt.payload, pkt.marker);
                if annex_b.is_empty() {
                    continue;
                }

                let mut vw = task.video_writer.lock().await;
                if let Some(ref mut f) = *vw {
                    if let Err(e) = f.write_all(&annex_b).await {
                        warn!(recording_id = %task.recording_id, error = %e, "write video data failed");
                    }
                }
            }
            Err(tokio::sync::broadcast::error::RecvError::Lagged(n)) => {
                warn!(recording_id = %task.recording_id, lagged = n, "video recording lagged");
            }
            Err(tokio::sync::broadcast::error::RecvError::Closed) => {
                info!(recording_id = %task.recording_id, "video broadcast channel closed");
                break;
            }
        }
    }
}

/// H.264 RTP payload → annex-B 格式
///
/// 处理常见的 RTP H.264 打包模式：
/// - Single NALU (type 1-23): 直接加 start code
/// - STAP-A (type 24): 多个 NALU，每个加 start code
/// - FU-A (type 28): 分片，重组后加 start code
fn rtp_h264_to_annex_b(payload: &[u8], _marker: bool) -> Vec<u8> {
    if payload.is_empty() {
        return Vec::new();
    }

    let nal_type = payload[0] & 0x1F;
    let start_code = [0x00, 0x00, 0x00, 0x01u8];

    match nal_type {
        1..=23 => {
            // Single NALU
            let mut out = Vec::with_capacity(4 + payload.len());
            out.extend_from_slice(&start_code);
            out.extend_from_slice(payload);
            out
        }
        24 => {
            // STAP-A: multiple NALUs
            let mut out = Vec::new();
            let mut i = 1; // skip STAP-A header
            while i + 2 <= payload.len() {
                let nalu_len = ((payload[i] as usize) << 8) | (payload[i + 1] as usize);
                i += 2;
                if i + nalu_len > payload.len() {
                    break;
                }
                out.extend_from_slice(&start_code);
                out.extend_from_slice(&payload[i..i + nalu_len]);
                i += nalu_len;
            }
            out
        }
        28 => {
            // FU-A: fragmented NALU
            if payload.len() < 2 {
                return Vec::new();
            }
            let fu_indicator = payload[0];
            let fu_header = payload[1];
            let is_start = fu_header & 0x80 != 0;
            let _is_end = fu_header & 0x40 != 0;
            let original_type = fu_header & 0x1F;
            let fnri = fu_indicator & 0xE0;

            if is_start {
                // 重组 NALU header
                let nalu_header = fnri | original_type;
                let mut out = Vec::with_capacity(4 + 1 + payload.len() - 2);
                out.extend_from_slice(&start_code);
                out.push(nalu_header);
                out.extend_from_slice(&payload[2..]);
                out
            } else {
                // 续片：只写 payload（start code 已在第一片写过了）
                payload[2..].to_vec()
            }
        }
        _ => {
            // 未知类型，直接写
            let mut out = Vec::with_capacity(4 + payload.len());
            out.extend_from_slice(&start_code);
            out.extend_from_slice(payload);
            out
        }
    }
}
