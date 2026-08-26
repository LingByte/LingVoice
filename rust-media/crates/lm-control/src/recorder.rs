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
use tracing::{debug, error, info, warn};

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
    /// 视频编解码器
    video_codec: CodecType,
    /// 视频帧计数（用于 IVF 时间戳）
    video_frame_count: Mutex<u64>,
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

        // 视频文件扩展名根据编解码器决定
        let video_codec = video_track.as_ref().map(|(_, c, _)| *c).unwrap_or(CodecType::Vp8);
        let video_ext = match video_codec {
            CodecType::Vp8 => "ivf",
            CodecType::Vp9 => "ivf",
            CodecType::H264 => "h264",
            _ => "raw",
        };
        let video_path = format!("{output_dir}/{sid_str}_video.{video_ext}");

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
        let video_writer = if let Some((_, codec, _)) = &video_track {
            match tokio::fs::File::create(&video_path).await {
                Ok(mut f) => {
                    // VP8/VP9: 写入 IVF 容器头（width/height 初始为 0，收到 keyframe 后回写）
                    if *codec == CodecType::Vp8 || *codec == CodecType::Vp9 {
                        let fourcc: [u8; 4] = if *codec == CodecType::Vp8 {
                            *b"VP80"
                        } else {
                            *b"VP90"
                        };
                        let header = ivf_header(fourcc, 0, 0, 30);
                        f.write_all(&header).await?;
                    }
                    info!(recording_id, session = %sid_str, path = %video_path, codec = ?codec, "video file created");
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
            video_codec,
            video_frame_count: Mutex::new(0),
            audio_path,
            video_path,
            start_time: std::time::Instant::now(),
        });

        // 启动音频录制 task
        if let Some((track_id, codec, track)) = audio_track {
            let task_clone = task.clone();
            let ssrc = track.ssrc;
            tokio::spawn(async move {
                run_audio_recording(task_clone, track.subscribe(), track_id, codec, ssrc).await;
            });
        }

        // 启动视频录制 task
        if let Some((track_id, codec, track)) = video_track {
            let task_clone = task.clone();
            let ssrc = track.ssrc;
            tokio::spawn(async move {
                run_video_recording(task_clone, track.subscribe(), track_id, codec, ssrc).await;
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
    expected_ssrc: u32,
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

    // SSRC 过滤：只录制匹配 SSRC 的包
    // 如果 expected_ssrc 为 0，用第一个收到的包的 SSRC
    let mut filter_ssrc = expected_ssrc;

    info!(
        recording_id = %task.recording_id,
        track = %track_id.0,
        codec = ?codec,
        ssrc = expected_ssrc,
        "audio recording task started"
    );

    loop {
        if task.stopped.load(Ordering::Relaxed) {
            info!(recording_id = %task.recording_id, "audio recording task stopped");
            break;
        }

        match rx.recv().await {
            Ok(pkt) => {
                // SSRC 过滤
                if filter_ssrc == 0 {
                    filter_ssrc = pkt.ssrc;
                    info!(recording_id = %task.recording_id, ssrc = pkt.ssrc, "audio recording: learned SSRC from first packet");
                }
                if pkt.ssrc != filter_ssrc {
                    continue;
                }

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

/// 视频录制 task：从 broadcast channel 接收 RTP，按编解码器写入对应格式文件
///
/// - VP8/VP9: 重组分片帧，剥离 RTP descriptor，写入 IVF 容器
/// - H.264: 重组 FU-A 分片，写入 annex-B 格式
async fn run_video_recording(
    task: Arc<RecordingTask>,
    mut rx: tokio::sync::broadcast::Receiver<RtpPacketOut>,
    track_id: TrackId,
    codec: CodecType,
    expected_ssrc: u32,
) {
    info!(
        recording_id = %task.recording_id,
        track = %track_id.0,
        codec = ?codec,
        ssrc = expected_ssrc,
        "video recording task started"
    );

    // SSRC 过滤：只录制匹配 SSRC 的包
    // 如果 expected_ssrc 为 0，用第一个收到的包的 SSRC
    let mut filter_ssrc = expected_ssrc;

    // 分片重组 buffer
    let mut frame_buffer: Vec<u8> = Vec::new();
    let mut last_seq: Option<u32> = None;
    let mut dimensions_set = false;
    let mut first_timestamp: Option<u32> = None;

    loop {
        if task.stopped.load(Ordering::Relaxed) {
            info!(recording_id = %task.recording_id, "video recording task stopped");
            break;
        }

        match rx.recv().await {
            Ok(pkt) => {
                // SSRC 过滤
                if filter_ssrc == 0 {
                    filter_ssrc = pkt.ssrc;
                    info!(recording_id = %task.recording_id, ssrc = pkt.ssrc, "video recording: learned SSRC from first packet");
                }
                if pkt.ssrc != filter_ssrc {
                    continue;
                }

                match codec {
                    CodecType::Vp8 => {
                        // VP8: 解析 RTP descriptor (RFC 7741)
                        let desc = match vp8_parse_descriptor(&pkt.payload) {
                            Some(d) => d,
                            None => {
                                warn!(recording_id = %task.recording_id, "failed to parse VP8 descriptor, skipping packet");
                                continue;
                            }
                        };

                        // 重复/乱序包检测：用 wrapping 算术判断包是否"落后"
                        // diff == 0: 完全相同的重复包
                        // diff > 0x80000000: 包的序列号"落后"于已处理的（旧包/乱序包）
                        // 这两种情况都跳过，避免重复数据污染帧 buffer
                        if let Some(prev) = last_seq {
                            let diff = pkt.sequence_number.wrapping_sub(prev);
                            if diff == 0 || diff > 0x80000000 {
                                continue;
                            }
                        }
                        last_seq = Some(pkt.sequence_number);

                        // VP8 RTP: S=1 且 PID=0 表示新帧开始
                        if desc.start_of_partition && desc.partition_index == 0 {
                            // 新帧开始，清空 buffer（丢弃不完整的上一帧）
                            if !frame_buffer.is_empty() && !pkt.marker {
                                // 上一个帧没有 marker 就收到了新帧的起始，说明丢包
                                debug!(recording_id = %task.recording_id, "new frame started before previous completed, discarding partial frame");
                            }
                            frame_buffer.clear();
                        }

                        // 剥离 descriptor，追加 VP8 payload
                        let payload = &pkt.payload[desc.descriptor_len..];
                        if !payload.is_empty() {
                            frame_buffer.extend_from_slice(payload);
                        }

                        // marker=true 表示帧完整
                        if pkt.marker && !frame_buffer.is_empty() {
                            let frame_data = std::mem::take(&mut frame_buffer);

                            // 从 keyframe 提取 dimensions，更新 IVF header
                            if !dimensions_set {
                                if let Some((w, h)) = vp8_extract_dimensions(&frame_data) {
                                    info!(recording_id = %task.recording_id, width = w, height = h, "extracted VP8 dimensions from keyframe, rewriting IVF header");
                                    let mut vw = task.video_writer.lock().await;
                                    if let Some(ref mut f) = *vw {
                                        use tokio::io::{AsyncSeekExt, SeekFrom};
                                        let _ = f.seek(SeekFrom::Start(0)).await;
                                        let header = ivf_header(*b"VP80", w, h, 30);
                                        let _ = f.write_all(&header).await;
                                        let _ = f.seek(SeekFrom::Start(32)).await;
                                    }
                                    dimensions_set = true;
                                }
                            }

                            let mut vw = task.video_writer.lock().await;
                            if let Some(ref mut f) = *vw {
                                let frame_count = {
                                    let mut fc = task.video_frame_count.lock().await;
                                    *fc += 1;
                                    *fc
                                };
                                // 使用 RTP timestamp 作为 IVF 帧时间戳（90kHz 时钟）
                                // 相对于第一帧的时间戳，避免巨大的起始时间戳
                                let ts_base = first_timestamp.unwrap_or(pkt.timestamp);
                                if first_timestamp.is_none() {
                                    first_timestamp = Some(pkt.timestamp);
                                }
                                let rel_ts = pkt.timestamp.wrapping_sub(ts_base) as u64;
                                let ivf_frame = ivf_frame_header(frame_data.len() as u32, rel_ts);
                                let _ = f.write_all(&ivf_frame).await;
                                let _ = f.write_all(&frame_data).await;
                            }
                        }
                    }
                    CodecType::Vp9 => {
                        // VP9: 类似 VP8，descriptor 格式不同 (RFC 9131)
                        // 简化处理：VP9 RTP descriptor 首字节 bit 7 = I (PictureID present)
                        // 暂时复用 VP8 descriptor 解析（近似）
                        let desc = match vp8_parse_descriptor(&pkt.payload) {
                            Some(d) => d,
                            None => continue,
                        };
                        let payload = &pkt.payload[desc.descriptor_len..];
                        if !payload.is_empty() {
                            frame_buffer.extend_from_slice(payload);
                        }
                        if pkt.marker && !frame_buffer.is_empty() {
                            let frame_data = std::mem::take(&mut frame_buffer);
                            let mut vw = task.video_writer.lock().await;
                            if let Some(ref mut f) = *vw {
                                let frame_count = {
                                    let mut fc = task.video_frame_count.lock().await;
                                    *fc += 1;
                                    *fc
                                };
                                let ivf_frame = ivf_frame_header(frame_data.len() as u32, pkt.timestamp as u64);
                                let _ = f.write_all(&ivf_frame).await;
                                let _ = f.write_all(&frame_data).await;
                            }
                        }
                    }
                    CodecType::H264 => {
                        // H.264: 重组 FU-A 分片
                        let nalu_data = rtp_h264_reassemble(&pkt.payload, &mut frame_buffer);
                        if pkt.marker && !nalu_data.is_empty() {
                            let frame_data = std::mem::take(&mut frame_buffer);

                            let mut vw = task.video_writer.lock().await;
                            if let Some(ref mut f) = *vw {
                                let _ = f.write_all(&frame_data).await;
                            }
                        }
                    }
                    _ => {}
                }
            }
            Err(tokio::sync::broadcast::error::RecvError::Lagged(n)) => {
                warn!(recording_id = %task.recording_id, lagged = n, "video recording lagged");
                // 丢包后清空 buffer，避免写半帧
                frame_buffer.clear();
            }
            Err(tokio::sync::broadcast::error::RecvError::Closed) => {
                info!(recording_id = %task.recording_id, "video broadcast channel closed");
                break;
            }
        }
    }
}

/// VP8 RTP payload descriptor 解析结果
#[derive(Debug, Default)]
struct Vp8Descriptor {
    /// descriptor 总字节数
    descriptor_len: usize,
    /// 是否为 partition 起始 (S bit)
    start_of_partition: bool,
    /// partition index (PID)
    partition_index: u8,
}

/// VP8 RTP payload: 解析 descriptor (RFC 7741)
///
/// ```text
///  0 1 2 3 4 5 6 7
/// +-+-+-+-+-+-+-+-+
/// |X|R|N|S|R| PID |
/// +-+-+-+-+-+-+-+-+
/// X: |I|L|T|K| RSV |
/// +-+-+-+-+-+-+-+-+
/// I: |M| PictureID |
/// +-+-+-+-+-+-+-+-+
/// L: |   TL0PICIDX |
/// +-+-+-+-+-+-+-+-+
/// T/K: |Y|KEYIDX| TID |
/// +-+-+-+-+-+-+-+-+
/// ```
fn vp8_parse_descriptor(payload: &[u8]) -> Option<Vp8Descriptor> {
    if payload.is_empty() {
        return None;
    }

    let mut i = 0;
    let first = payload[0];
    i += 1;

    let start_of_partition = first & 0x10 != 0;
    let partition_index = first & 0x0f;

    // X bit (bit 7): 是否有扩展
    let has_extension = first & 0x80 != 0;
    if has_extension {
        if i >= payload.len() {
            return None;
        }
        let ext = payload[i];
        i += 1;

        // I bit (bit 7 of extension): PictureID present
        if ext & 0x80 != 0 {
            if i >= payload.len() {
                return None;
            }
            let pic_id_first = payload[i];
            i += 1;
            // M bit (bit 7): 16-bit PictureID
            if pic_id_first & 0x80 != 0 {
                if i >= payload.len() {
                    return None;
                }
                i += 1;
            }
        }
        // L bit (bit 6): TL0PICIDX present
        if ext & 0x40 != 0 {
            if i >= payload.len() {
                return None;
            }
            i += 1;
        }
        // T bit (bit 5) or K bit (bit 4): TID/Y/KEYIDX present
        if ext & 0x20 != 0 || ext & 0x10 != 0 {
            if i >= payload.len() {
                return None;
            }
            i += 1;
        }
    }

    Some(Vp8Descriptor {
        descriptor_len: i,
        start_of_partition,
        partition_index,
    })
}

/// 从 VP8 keyframe 提取 width/height
///
/// VP8 keyframe 格式 (RFC 6386):
/// ```text
/// frame_tag(3 bytes) | sync_code(3: 0x9d 0x01 0x2a) | width(2 LE) | height(2 LE)
/// ```
/// frame_tag byte 0 bit 0 = frame_type (0=keyframe)
fn vp8_extract_dimensions(frame: &[u8]) -> Option<(u16, u16)> {
    // keyframe: 3 (tag) + 3 (sync) + 2 (width) + 2 (height) = 10 bytes minimum
    if frame.len() < 10 {
        return None;
    }
    // frame_tag byte 0 bit 0: 0 = keyframe
    if frame[0] & 0x01 != 0 {
        return None; // inter frame, no dimensions
    }
    // sync code: 0x9d 0x01 0x2a
    if frame[3] != 0x9d || frame[4] != 0x01 || frame[5] != 0x2a {
        return None;
    }
    let width = u16::from_le_bytes([frame[6], frame[7]]) & 0x3fff;
    let height = u16::from_le_bytes([frame[8], frame[9]]) & 0x3fff;
    if width == 0 || height == 0 {
        return None;
    }
    Some((width, height))
}

/// IVF 文件头 (32 bytes)
///
/// IVF 是简单的 VP8/VP9/AV1 容器格式：
/// ```text
/// 0  1  2  3  4  5  6  7  8  9 10 11 12 13 14 15
/// DKIF                              version
/// 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31
/// fourcc      width   height  framerate       num_frames
/// ```
fn ivf_header(fourcc: [u8; 4], width: u16, height: u16, fps: u32) -> [u8; 32] {
    let mut h = [0u8; 32];
    h[0..4].copy_from_slice(b"DKIF");
    h[4..6].copy_from_slice(&0u16.to_le_bytes()); // version 0
    h[6..8].copy_from_slice(&32u16.to_le_bytes()); // header size 32
    h[8..12].copy_from_slice(&fourcc);
    h[12..14].copy_from_slice(&width.to_le_bytes());
    h[14..16].copy_from_slice(&height.to_le_bytes());
    // timebase = 1/90000（RTP 视频时钟率），帧时间戳用 RTP timestamp
    // 这样 30fps 的帧间隔 = 90000/30 = 3000 ticks = 33.3ms，正确
    h[16..20].copy_from_slice(&90000u32.to_le_bytes()); // timebase denominator
    h[20..24].copy_from_slice(&1u32.to_le_bytes()); // timebase numerator
    h[24..28].copy_from_slice(&0u32.to_le_bytes()); // num frames
    h[28..32].copy_from_slice(&0u32.to_le_bytes()); // unused
    // fps 参数保留用于未来可能的帧率字段，IVF header 本身不存 fps
    let _ = fps;
    h
}

/// IVF 帧头 (12 bytes)
/// timestamp 使用 RTP 时钟率 (90000 for video)
fn ivf_frame_header(frame_size: u32, timestamp: u64) -> [u8; 12] {
    let mut h = [0u8; 12];
    h[0..4].copy_from_slice(&frame_size.to_le_bytes());
    h[4..12].copy_from_slice(&timestamp.to_le_bytes());
    h
}

/// H.264 RTP payload 重组为 annex-B 格式
///
/// 处理常见的 RTP H.264 打包模式：
/// - Single NALU (type 1-23): 直接加 start code 写入 buffer
/// - STAP-A (type 24): 多个 NALU，每个加 start code
/// - FU-A (type 28): 分片，第一片加 start code + 重组 NALU header，续片只追加 payload
///
/// `buffer` 用于跨包重组 FU-A 分片
fn rtp_h264_reassemble(payload: &[u8], buffer: &mut Vec<u8>) -> Vec<u8> {
    if payload.is_empty() {
        return Vec::new();
    }

    let nal_type = payload[0] & 0x1F;
    let start_code = [0x00, 0x00, 0x00, 0x01u8];

    match nal_type {
        1..=23 => {
            // Single NALU: 完整帧
            let mut out = Vec::with_capacity(4 + payload.len());
            out.extend_from_slice(&start_code);
            out.extend_from_slice(payload);
            out
        }
        24 => {
            // STAP-A: multiple NALUs in one packet
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
            let original_type = fu_header & 0x1F;
            let fnri = fu_indicator & 0xE0;

            if is_start {
                // 新帧开始：清空 buffer，写 start code + 重组 NALU header
                buffer.clear();
                buffer.extend_from_slice(&start_code);
                buffer.push(fnri | original_type);
                buffer.extend_from_slice(&payload[2..]);
            } else {
                // 续片：只追加 payload
                buffer.extend_from_slice(&payload[2..]);
            }
            // 返回空（调用方在 marker=true 时用 buffer）
            Vec::new()
        }
        _ => {
            let mut out = Vec::with_capacity(4 + payload.len());
            out.extend_from_slice(&start_code);
            out.extend_from_slice(payload);
            out
        }
    }
}
