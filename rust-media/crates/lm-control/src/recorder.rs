//! 录制管理器
//!
//! 管理 session/room 级别的录制任务。
//! 音频：解码 MediaFrame payload → PCM → WavRecorder
//! 视频：MediaFrame payload → IVF (VP8/VP9) / Annex-B (H264)
//!
//! 数据流（重构后）：
//! ```text
//!   push_rtp → MediaStream → Depacketizer → MediaFrame
//!                                          ↓
//!                          RecordingSink (StreamSink)
//!                                          ↓ mpsc channel
//!                          recording task → decode(音频) → WavRecorder
//!                                          → raw(视频) → IVF/H264 file
//! ```

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use dashmap::DashMap;
use lm_core::{CodecType, MediaFrame, SessionId, StreamSink, TrackId, TrackKind};
use lm_recorder::{Recorder, RecordingFormat, RecordingResult};
use lm_stream::{MediaStream, StreamId, StreamRegistry};
use tokio::io::AsyncWriteExt;
use tokio::sync::Mutex;
use tracing::{error, info, warn};

use crate::session::MediaSession;

// ============================================================================
// RecordingSink — StreamSink 实现，通过 channel 转发 MediaFrame
// ============================================================================

/// 录制数据接收器
///
/// 实现 StreamSink，收到 MediaFrame 后通过 mpsc channel 转发给异步录制 task。
/// 这样可以把同步的 StreamSink 回调和异步的文件 I/O 解耦。
struct RecordingSink {
    sender: tokio::sync::mpsc::UnboundedSender<MediaFrame>,
}

impl StreamSink for RecordingSink {
    fn on_frame(&self, frame: &MediaFrame) {
        // 非阻塞发送，如果 channel 关闭则丢弃
        let _ = self.sender.send(frame.clone());
    }
}

// ============================================================================
// RecordingTask
// ============================================================================

struct RecordingTask {
    recording_id: String,
    session_id: SessionId,
    stopped: Arc<AtomicBool>,
    /// 音频录制器
    audio_recorder: Mutex<Option<Recorder>>,
    /// 视频文件写入
    video_writer: Mutex<Option<tokio::fs::File>>,
    /// 视频编解码器
    video_codec: CodecType,
    /// 录制文件路径
    audio_path: String,
    video_path: String,
    /// 录制开始时间
    start_time: std::time::Instant,
    /// 源流订阅句柄（用于停止时移除订阅）
    /// 保存 sink 的 Arc，停止时用于 remove_source_sink
    _audio_sink: Option<Arc<RecordingSink>>,
    _video_sink: Option<Arc<RecordingSink>>,
}

// ============================================================================
// RecordingManager
// ============================================================================

/// 录制管理器
pub struct RecordingManager {
    /// recording_id → 录制任务状态
    recordings: DashMap<String, Arc<RecordingTask>>,
    /// session_id → recording_id
    session_recordings: DashMap<SessionId, String>,
}

impl Default for RecordingManager {
    fn default() -> Self {
        Self::new()
    }
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
    /// 重构后：订阅 MediaStream 的源流，直接收到 MediaFrame（已组装完整的编码帧）。
    /// 不再需要 SSRC 过滤、VP8 descriptor 解析、帧组装。
    pub async fn start_recording(
        &self,
        recording_id: &str,
        session: &Arc<MediaSession>,
        output_dir: &str,
        streams: &Arc<StreamRegistry>,
    ) -> anyhow::Result<()> {
        let session_id = session.id.clone();

        // 确保输出目录存在
        std::fs::create_dir_all(output_dir)
            .map_err(|e| anyhow::anyhow!("create recording dir {output_dir}: {e}"))?;

        // 查找音频和视频 track
        let mut audio_track_id: Option<TrackId> = None;
        let mut audio_codec = CodecType::Opus;
        let mut video_track_id: Option<TrackId> = None;
        let mut video_codec = CodecType::Vp8;

        for entry in session.tracks.iter() {
            let track = entry.value();
            if track.kind == TrackKind::Audio && audio_track_id.is_none() {
                audio_track_id = Some(track.track_id.clone());
                audio_codec = track.codec;
            } else if track.kind == TrackKind::Video && video_track_id.is_none() {
                video_track_id = Some(track.track_id.clone());
                video_codec = track.codec;
            }
        }

        let sid_str = session_id.0.clone();
        let audio_path = format!("{output_dir}/{sid_str}_audio.wav");

        let video_ext = match video_codec {
            CodecType::Vp8 | CodecType::Vp9 => "ivf",
            CodecType::H264 => "h264",
            _ => "raw",
        };
        let video_path = format!("{output_dir}/{sid_str}_video.{video_ext}");

        // 创建音频录制器
        let audio_recorder = if audio_track_id.is_some() {
            let sample_rate = match audio_codec {
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
                1,
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

        // 创建视频文件
        let video_writer = if video_track_id.is_some() {
            match tokio::fs::File::create(&video_path).await {
                Ok(mut f) => {
                    if video_codec == CodecType::Vp8 || video_codec == CodecType::Vp9 {
                        let fourcc: [u8; 4] = if video_codec == CodecType::Vp8 {
                            *b"VP80"
                        } else {
                            *b"VP90"
                        };
                        let header = ivf_header(fourcc, 0, 0);
                        f.write_all(&header).await?;
                    }
                    info!(recording_id, session = %sid_str, path = %video_path, codec = ?video_codec, "video file created");
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

        // 创建音频录制 sink + channel
        let audio_sink = if let Some(ref track_id) = audio_track_id {
            let stream_id = StreamId::new(&sid_str, &track_id.0);
            if let Some(media_stream) = streams.get(&stream_id) {
                let (tx, rx) = tokio::sync::mpsc::unbounded_channel::<MediaFrame>();
                let sink = Arc::new(RecordingSink { sender: tx });
                media_stream.add_source_sink(sink.clone());

                // 启动音频录制 task
                // 把 recorder 从 Option 中取出交给 task，task 独占写入
                let task_stopped = stopped.clone();
                let task_recording_id = recording_id.to_string();
                let task_recorder = audio_recorder; // move ownership to task
                tokio::spawn(async move {
                    run_audio_recording(
                        task_recording_id,
                        task_stopped,
                        rx,
                        audio_codec,
                        task_recorder,
                    )
                    .await;
                });

                Some(sink)
            } else {
                warn!(recording_id, session = %sid_str, track = %track_id.0, "audio MediaStream not found");
                None
            }
        } else {
            None
        };

        // 创建视频录制 sink + channel
        let video_sink = if let Some(ref track_id) = video_track_id {
            let stream_id = StreamId::new(&sid_str, &track_id.0);
            if let Some(media_stream) = streams.get(&stream_id) {
                let (tx, rx) = tokio::sync::mpsc::unbounded_channel::<MediaFrame>();
                let sink = Arc::new(RecordingSink { sender: tx });
                media_stream.add_source_sink(sink.clone());

                // 启动视频录制 task
                let task_stopped = stopped.clone();
                let task_recording_id = recording_id.to_string();
                let task_video_writer = video_writer; // move ownership to task
                let task_video_codec = video_codec;
                tokio::spawn(async move {
                    run_video_recording(
                        task_recording_id,
                        task_stopped,
                        rx,
                        task_video_codec,
                        task_video_writer,
                    )
                    .await;
                });

                Some(sink)
            } else {
                warn!(recording_id, session = %sid_str, track = %track_id.0, "video MediaStream not found");
                None
            }
        } else {
            None
        };

        // recorder 和 video_writer 的所有权已移交给 task，
        // RecordingTask 中保存 None（stop_recording 时由 task 已关闭）
        let task = Arc::new(RecordingTask {
            recording_id: recording_id.to_string(),
            session_id: session_id.clone(),
            stopped: stopped.clone(),
            audio_recorder: Mutex::new(None),
            video_writer: Mutex::new(None),
            video_codec,
            audio_path,
            video_path,
            start_time: std::time::Instant::now(),
            _audio_sink: audio_sink,
            _video_sink: video_sink,
        });

        self.recordings
            .insert(recording_id.to_string(), task.clone());
        self.session_recordings
            .insert(session_id, recording_id.to_string());

        info!(recording_id, session = %sid_str, "recording started");
        Ok(())
    }

    /// 停止录制
    pub async fn stop_recording(&self, recording_id: &str) -> anyhow::Result<RecordingResult> {
        let (_, task) = self
            .recordings
            .remove(recording_id)
            .ok_or_else(|| anyhow::anyhow!("recording {} not found", recording_id))?;

        self.session_recordings.retain(|_, rid| rid != recording_id);

        // 停止录制 task
        task.stopped.store(true, Ordering::Relaxed);

        let duration_ms = task.start_time.elapsed().as_millis() as u64;

        // finalize 音频
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

        let file_size = audio_result.as_ref().map(|r| r.file_size).unwrap_or(0)
            + std::fs::metadata(&task.video_path)
                .map(|m| m.len())
                .unwrap_or(0);

        let file_path = if task.video_path.is_empty() {
            audio_result.map(|r| r.file_path).unwrap_or_default()
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

// ============================================================================
// 音频录制 task — 从 channel 读取 MediaFrame，解码为 PCM，写入 WAV
// ============================================================================

async fn run_audio_recording(
    recording_id: String,
    stopped: Arc<AtomicBool>,
    mut rx: tokio::sync::mpsc::UnboundedReceiver<MediaFrame>,
    codec: CodecType,
    recorder: Option<Recorder>,
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

    // recorder 在 task 内部独占（从 start_recording 传递）
    // 但 Recorder 不能 clone，所以用 Option 包裹
    let mut recorder_opt = recorder;

    info!(
        recording_id = %recording_id,
        codec = ?codec,
        "audio recording task started (MediaFrame mode)"
    );

    loop {
        if stopped.load(Ordering::Relaxed) {
            info!(recording_id = %recording_id, "audio recording task stopped");
            break;
        }

        match rx.recv().await {
            Some(frame) => {
                // 解码 MediaFrame payload → PCM
                let samples = decoder.decode(&frame.data);
                if samples.is_empty() {
                    continue;
                }

                let audio_frame = lm_core::AudioFrame {
                    samples,
                    sample_rate: decoder.sample_rate(),
                    timestamp: frame.timestamp as u64,
                };

                // 写入 WAV
                if let Some(ref mut rec) = recorder_opt {
                    if let Err(e) = rec.write_frame(&audio_frame).await {
                        warn!(recording_id = %recording_id, error = %e, "write audio frame failed");
                    }
                }
            }
            None => {
                info!(recording_id = %recording_id, "audio frame channel closed");
                break;
            }
        }
    }
}

// ============================================================================
// 视频录制 task — 从 channel 读取 MediaFrame，写入 IVF/H264
// ============================================================================

async fn run_video_recording(
    recording_id: String,
    stopped: Arc<AtomicBool>,
    mut rx: tokio::sync::mpsc::UnboundedReceiver<MediaFrame>,
    codec: CodecType,
    video_writer: Option<tokio::fs::File>,
) {
    let mut writer_opt = video_writer;
    let mut dimensions_set = false;
    let mut first_timestamp: Option<u32> = None;
    let mut frame_count: u64 = 0;

    info!(
        recording_id = %recording_id,
        codec = ?codec,
        "video recording task started (MediaFrame mode)"
    );

    loop {
        if stopped.load(Ordering::Relaxed) {
            info!(recording_id = %recording_id, "video recording task stopped");
            break;
        }

        match rx.recv().await {
            Some(frame) => {
                if let Some(ref mut f) = writer_opt {
                    match codec {
                        CodecType::Vp8 | CodecType::Vp9 => {
                            // IVF 写入

                            // 从 keyframe 提取 dimensions，更新 IVF header
                            if !dimensions_set && frame.keyframe {
                                if let Some((w, h)) =
                                    lm_depacketizer::vp8_extract_dimensions(&frame.data)
                                {
                                    info!(
                                        recording_id = %recording_id,
                                        width = w, height = h,
                                        "extracted dimensions from keyframe, rewriting IVF header"
                                    );
                                    use tokio::io::{AsyncSeekExt, SeekFrom};
                                    let _ = f.seek(SeekFrom::Start(0)).await;
                                    let fourcc: [u8; 4] = if codec == CodecType::Vp8 {
                                        *b"VP80"
                                    } else {
                                        *b"VP90"
                                    };
                                    let header = ivf_header(fourcc, w, h);
                                    let _ = f.write_all(&header).await;
                                    let _ = f.seek(SeekFrom::Start(32)).await;
                                    dimensions_set = true;
                                }
                            }

                            // 时间戳：相对于第一帧
                            let ts_base = first_timestamp.unwrap_or(frame.timestamp);
                            if first_timestamp.is_none() {
                                first_timestamp = Some(frame.timestamp);
                            }
                            let rel_ts = frame.timestamp.wrapping_sub(ts_base) as u64;

                            // IVF 帧头 + 帧数据
                            frame_count += 1;
                            let ivf_frame = ivf_frame_header(frame.data.len() as u32, rel_ts);
                            let _ = f.write_all(&ivf_frame).await;
                            let _ = f.write_all(&frame.data).await;
                        }
                        CodecType::H264 => {
                            // H264 Annex-B 写入（MediaFrame 已包含起始码）
                            frame_count += 1;
                            let _ = f.write_all(&frame.data).await;
                        }
                        _ => {}
                    }
                }
            }
            None => {
                info!(recording_id = %recording_id, "video frame channel closed");
                break;
            }
        }
    }

    info!(recording_id = %recording_id, frame_count, "video recording task ended");
}

// ============================================================================
// IVF 容器格式
// ============================================================================

/// IVF 文件头 (32 bytes)
fn ivf_header(fourcc: [u8; 4], width: u16, height: u16) -> [u8; 32] {
    let mut h = [0u8; 32];
    h[0..4].copy_from_slice(b"DKIF");
    h[4..6].copy_from_slice(&0u16.to_le_bytes());
    h[6..8].copy_from_slice(&32u16.to_le_bytes());
    h[8..12].copy_from_slice(&fourcc);
    h[12..14].copy_from_slice(&width.to_le_bytes());
    h[14..16].copy_from_slice(&height.to_le_bytes());
    h[16..20].copy_from_slice(&90000u32.to_le_bytes()); // timebase denominator
    h[20..24].copy_from_slice(&1u32.to_le_bytes()); // timebase numerator
    h[24..28].copy_from_slice(&0u32.to_le_bytes());
    h[28..32].copy_from_slice(&0u32.to_le_bytes());
    h
}

/// IVF 帧头 (12 bytes)
fn ivf_frame_header(size: u32, timestamp: u64) -> [u8; 12] {
    let mut h = [0u8; 12];
    h[0..4].copy_from_slice(&size.to_le_bytes());
    h[4..12].copy_from_slice(&timestamp.to_le_bytes());
    h
}
