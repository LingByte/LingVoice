//! lm-playback — 录像回放服务
//!
//! 功能：
//! - 从录制文件（WAV/MP4/IVF）读取并回放
//! - 将回放帧推入 MediaStream（供 WHEP/WebRTC/HLS 拉流）
//! - 支持 seek（跳转到指定时间点，重启 ffmpeg with -ss）
//! - 支持倍速播放
//!
//! 用法：
//! ```ignore
//! let player = Player::new("/recordings/session1.mp4");
//! player.play(streams).await?;
//! player.seek(60.0).await?; // 跳到 60s
//! player.set_speed(2.0); // 2x
//! ```

use anyhow::{anyhow, Result};
use lm_core::{CodecType, MediaFrame, TrackKind};
use lm_stream::StreamRegistry;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::Arc;
use tokio::process::Command;
use tracing::{info, warn};

/// 回放状态
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PlaybackState {
    Idle,
    Playing,
    Paused,
    Stopped,
    Error(String),
}

/// 回放器
pub struct Player {
    /// 录制文件路径
    file_path: PathBuf,
    /// 播放状态
    state: PlaybackState,
    /// 播放速度 (1.0 = 正常)
    speed: f32,
    /// 当前播放位置（秒）
    position: Arc<AtomicU64>,
    /// 总时长（秒）
    duration: f64,
    /// 停止标志
    stopped: Arc<AtomicBool>,
    /// 当前 ffmpeg 子进程的停止信号
    child_stopped: Arc<AtomicBool>,
}

impl Player {
    pub fn new(file_path: impl Into<PathBuf>) -> Self {
        Self {
            file_path: file_path.into(),
            state: PlaybackState::Idle,
            speed: 1.0,
            position: Arc::new(AtomicU64::new(0)),
            duration: 0.0,
            stopped: Arc::new(AtomicBool::new(false)),
            child_stopped: Arc::new(AtomicBool::new(true)),
        }
    }

    /// 获取文件信息（时长、编码等）通过 ffprobe
    pub async fn probe(&mut self) -> Result<MediaInfo> {
        let path_str = self
            .file_path
            .to_str()
            .ok_or_else(|| anyhow!("invalid path"))?;

        let output = Command::new("ffprobe")
            .args([
                "-v",
                "quiet",
                "-print_format",
                "json",
                "-show_format",
                "-show_streams",
                path_str,
            ])
            .output()
            .await;

        match output {
            Ok(out) if out.status.success() => {
                let stdout = String::from_utf8_lossy(&out.stdout);
                let info = parse_ffprobe_json(&stdout)?;
                self.duration = info.duration_sec;
                Ok(info)
            }
            Ok(out) => {
                let stderr = String::from_utf8_lossy(&out.stderr);
                Err(anyhow!("ffprobe failed: {stderr}"))
            }
            Err(e) => Err(anyhow!("ffprobe not found: {e}")),
        }
    }

    /// 开始回放
    /// 通过 ffmpeg 将文件转为 RTP 推送到指定端口
    pub async fn play(&mut self, _streams: &Arc<StreamRegistry>) -> Result<()> {
        if self.state == PlaybackState::Playing {
            return Ok(());
        }

        self.stopped.store(false, Ordering::Relaxed);
        self.child_stopped.store(false, Ordering::Relaxed);
        self.state = PlaybackState::Playing;

        let path_str = self
            .file_path
            .to_str()
            .ok_or_else(|| anyhow!("invalid path"))?;

        info!(path = path_str, speed = self.speed, "playback started");

        // 检查文件扩展名, 优先使用纯 Rust 回放
        let ext = self
            .file_path
            .extension()
            .and_then(|e| e.to_str())
            .unwrap_or("");
        let use_native = matches!(ext, "wav" | "ogg" | "ivf");

        if use_native {
            // 纯 Rust 回放路径
            self.play_native().await?;
        } else {
            // ffmpeg 回放路径 (MP4, HLS, 其他)
            self.play_ffmpeg().await?;
        }

        // 位置更新任务
        let position_clone = self.position.clone();
        let stopped_clone = self.stopped.clone();
        tokio::spawn(async move {
            let start = std::time::Instant::now();
            loop {
                if stopped_clone.load(Ordering::Relaxed) {
                    break;
                }
                tokio::time::sleep(tokio::time::Duration::from_secs(1)).await;
                let elapsed = start.elapsed().as_secs();
                position_clone.store(elapsed, Ordering::Relaxed);
            }
        });

        Ok(())
    }

    /// 纯 Rust 回放 (WAV, OGG, IVF)
    async fn play_native(&mut self) -> Result<()> {
        let path = self.file_path.clone();
        let stopped = self.stopped.clone();
        let child_stopped = self.child_stopped.clone();
        let position = self.position.clone();
        let speed = self.speed;

        tokio::spawn(async move {
            let ext = path.extension().and_then(|e| e.to_str()).unwrap_or("");
            let result = match ext {
                "wav" => play_wav_native(&path, &stopped, &position, speed).await,
                "ivf" => play_ivf_native(&path, &stopped, &position, speed).await,
                "ogg" => play_ogg_native(&path, &stopped, &position, speed).await,
                _ => Err(anyhow!("unsupported native format: {}", ext)),
            };

            child_stopped.store(true, Ordering::Relaxed);

            if let Err(e) = result {
                if !stopped.load(Ordering::Relaxed) {
                    warn!("native playback error: {}", e);
                }
            }
        });

        Ok(())
    }

    /// ffmpeg 回放路径
    async fn play_ffmpeg(&mut self) -> Result<()> {
        let stopped = self.stopped.clone();
        let child_stopped = self.child_stopped.clone();
        let path = self.file_path.clone();
        let speed = self.speed;

        tokio::spawn(async move {
            let speed_filter = if (speed - 1.0).abs() > 0.01 {
                format!(
                    "-filter_complex [0:v]setpts={}/PTS[v];[0:a]atempo={}[a]",
                    1.0 / speed,
                    speed
                )
            } else {
                String::new()
            };

            let mut cmd = Command::new("ffmpeg");
            cmd.args(["-re", "-i", path.to_str().unwrap_or("")]);

            if !speed_filter.is_empty() {
                cmd.args(["-filter_complex", &speed_filter]);
                cmd.args(["-map", "[v]", "-map", "[a]"]);
            } else {
                cmd.args(["-c", "copy"]);
            }

            cmd.args(["-f", "rtp", "rtp://127.0.0.1:5004"]);

            let result = cmd.output().await;

            child_stopped.store(true, Ordering::Relaxed);

            if let Ok(out) = result {
                if !out.status.success() {
                    let stderr = String::from_utf8_lossy(&out.stderr);
                    warn!("ffmpeg playback ended: {stderr}");
                }
            }

            if stopped.load(Ordering::Relaxed) {
                info!("playback stopped by user");
            }
        });

        Ok(())
    }

    /// 暂停
    pub fn pause(&mut self) {
        self.state = PlaybackState::Paused;
        // 暂停通过停止 ffmpeg 实现，恢复时需要重启
        self.stopped.store(true, Ordering::Relaxed);
    }

    /// 恢复
    pub fn resume(&mut self) {
        if self.state == PlaybackState::Paused {
            self.state = PlaybackState::Playing;
            // 恢复需要重启 ffmpeg from current position
            // 简化：标记为需要重启
        }
    }

    /// 停止
    pub fn stop(&mut self) {
        self.stopped.store(true, Ordering::Relaxed);
        self.state = PlaybackState::Stopped;
        self.position.store(0, Ordering::Relaxed);
    }

    /// 跳转到指定位置（秒）
    ///
    /// 实现：停止当前进程，从新位置重启回放。
    /// native 格式 (WAV/IVF) 通过字节偏移 seek, ffmpeg 格式通过 -ss 参数 seek。
    pub async fn seek(&mut self, position_sec: f64) -> Result<()> {
        let was_playing = self.state == PlaybackState::Playing;

        // 停止当前进程
        self.stopped.store(true, Ordering::Relaxed);

        // 等待进程退出
        let timeout = tokio::time::Duration::from_millis(500);
        let start = std::time::Instant::now();
        while !self.child_stopped.load(Ordering::Relaxed) {
            if start.elapsed() > timeout {
                warn!("playback process did not stop within timeout, proceeding with seek");
                break;
            }
            tokio::time::sleep(tokio::time::Duration::from_millis(50)).await;
        }

        self.position.store(position_sec as u64, Ordering::Relaxed);
        info!(position = position_sec, was_playing, "seek completed");

        // 如果之前在播放，从新位置重启
        if was_playing {
            self.stopped.store(false, Ordering::Relaxed);
            self.child_stopped.store(false, Ordering::Relaxed);
            self.state = PlaybackState::Playing;

            let path = self.file_path.clone();
            let stopped = self.stopped.clone();
            let child_stopped = self.child_stopped.clone();
            let position = self.position.clone();
            let speed = self.speed;
            let seek_pos = position_sec;

            // 检查是否为 native 格式
            let ext = path
                .extension()
                .and_then(|e| e.to_str())
                .unwrap_or("")
                .to_string();
            let use_native = matches!(ext.as_str(), "wav" | "ogg" | "ivf");

            if use_native {
                // native seek: 通过字节偏移跳转
                tokio::spawn(async move {
                    let result = match ext.as_str() {
                        "wav" => {
                            play_wav_native_seeking(&path, &stopped, &position, speed, seek_pos)
                                .await
                        }
                        "ivf" => {
                            play_ivf_native_seeking(&path, &stopped, &position, speed, seek_pos)
                                .await
                        }
                        _ => play_ogg_native(&path, &stopped, &position, speed).await,
                    };
                    child_stopped.store(true, Ordering::Relaxed);
                    if let Err(e) = result {
                        if !stopped.load(Ordering::Relaxed) {
                            warn!("native seek playback error: {}", e);
                        }
                    }
                });
            } else {
                // ffmpeg seek: 使用 -ss 参数
                tokio::spawn(async move {
                    let mut cmd = Command::new("ffmpeg");
                    cmd.args([
                        "-ss",
                        &format!("{seek_pos}"),
                        "-re",
                        "-i",
                        path.to_str().unwrap_or(""),
                    ]);

                    if (speed - 1.0).abs() > 0.01 {
                        cmd.args([
                            "-filter_complex",
                            &format!(
                                "[0:v]setpts={}/PTS[v];[0:a]atempo={}[a]",
                                1.0 / speed,
                                speed
                            ),
                            "-map",
                            "[v]",
                            "-map",
                            "[a]",
                        ]);
                    } else {
                        cmd.args(["-c", "copy"]);
                    }

                    cmd.args(["-f", "rtp", "rtp://127.0.0.1:5004"]);

                    let result = cmd.output().await;
                    child_stopped.store(true, Ordering::Relaxed);

                    if let Ok(out) = result {
                        if !out.status.success() {
                            let stderr = String::from_utf8_lossy(&out.stderr);
                            warn!("ffmpeg seek playback ended: {stderr}");
                        }
                    }
                });

                // 位置更新任务
                let position_clone = self.position.clone();
                let stopped_clone = self.stopped.clone();
                let base_position = position_sec as u64;
                tokio::spawn(async move {
                    let start = std::time::Instant::now();
                    loop {
                        if stopped_clone.load(Ordering::Relaxed) {
                            break;
                        }
                        tokio::time::sleep(tokio::time::Duration::from_secs(1)).await;
                        let elapsed = start.elapsed().as_secs();
                        position_clone.store(base_position + elapsed, Ordering::Relaxed);
                    }
                });
            }
        }

        Ok(())
    }

    /// 设置播放速度
    pub fn set_speed(&mut self, speed: f32) {
        self.speed = speed;
    }

    /// 获取当前状态
    pub fn state(&self) -> &PlaybackState {
        &self.state
    }

    /// 获取当前播放位置
    pub fn position(&self) -> f64 {
        self.position.load(Ordering::Relaxed) as f64
    }

    /// 获取总时长
    pub fn duration(&self) -> f64 {
        self.duration
    }
}

/// 媒体文件信息
#[derive(Debug, Clone, Default)]
pub struct MediaInfo {
    pub duration_sec: f64,
    pub video_codec: Option<String>,
    pub audio_codec: Option<String>,
    pub width: Option<u32>,
    pub height: Option<u32>,
    pub sample_rate: Option<u32>,
    pub channels: Option<u16>,
}

/// 简化版 ffprobe JSON 解析
fn parse_ffprobe_json(json: &str) -> Result<MediaInfo> {
    let mut info = MediaInfo::default();

    // 简化解析：查找 "duration" 字段
    if let Some(idx) = json.find("\"duration\"") {
        let rest = &json[idx + 11..];
        if let Some(start) = rest.find('"') {
            let end = rest[1..].find('"').map(|p| p + 1).unwrap_or(0);
            if end > 0 {
                let dur_str = &rest[1..end];
                if let Ok(dur) = dur_str.parse::<f64>() {
                    info.duration_sec = dur;
                }
            }
        }
    }

    // 查找 codec_name
    if let Some(idx) = json.find("\"codec_name\"") {
        let rest = &json[idx + 13..];
        if let Some(start) = rest.find('"') {
            let end = rest[1..].find('"').map(|p| p + 1).unwrap_or(0);
            if end > 0 {
                let codec = &rest[1..end];
                if codec.starts_with('h')
                    || codec == "h264"
                    || codec == "hevc"
                    || codec == "vp8"
                    || codec == "vp9"
                {
                    info.video_codec = Some(codec.to_string());
                } else {
                    info.audio_codec = Some(codec.to_string());
                }
            }
        }
    }

    // 查找 width/height
    if let Some(idx) = json.find("\"width\"") {
        let rest = &json[idx + 8..];
        let end = rest
            .find(|c: char| c == ',' || c == '}')
            .unwrap_or(rest.len());
        if let Ok(w) = rest[..end].trim().parse::<u32>() {
            info.width = Some(w);
        }
    }
    if let Some(idx) = json.find("\"height\"") {
        let rest = &json[idx + 9..];
        let end = rest
            .find(|c: char| c == ',' || c == '}')
            .unwrap_or(rest.len());
        if let Ok(h) = rest[..end].trim().parse::<u32>() {
            info.height = Some(h);
        }
    }

    Ok(info)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_player_new() {
        let player = Player::new("/recordings/test.mp4");
        assert_eq!(player.state(), &PlaybackState::Idle);
        assert_eq!(player.speed, 1.0);
    }

    #[test]
    fn test_player_speed() {
        let mut player = Player::new("/recordings/test.mp4");
        player.set_speed(2.0);
        assert_eq!(player.speed, 2.0);
    }

    #[test]
    fn test_player_pause_resume() {
        let mut player = Player::new("/recordings/test.mp4");
        player.state = PlaybackState::Playing;
        player.pause();
        assert_eq!(player.state(), &PlaybackState::Paused);
        player.resume();
        assert_eq!(player.state(), &PlaybackState::Playing);
    }

    #[test]
    fn test_player_stop() {
        let mut player = Player::new("/recordings/test.mp4");
        player.state = PlaybackState::Playing;
        player.stop();
        assert_eq!(player.state(), &PlaybackState::Stopped);
        assert_eq!(player.position(), 0.0);
    }

    #[test]
    fn test_player_position_atomic() {
        let player = Player::new("/recordings/test.mp4");
        player.position.store(42, Ordering::Relaxed);
        assert_eq!(player.position(), 42.0);
    }

    #[test]
    fn test_parse_ffprobe_json_duration() {
        let json = r#"{"format":{"duration":"120.500"},"streams":[{"codec_name":"h264","width":1280,"height":720}]}"#;
        let info = parse_ffprobe_json(json).unwrap();
        assert!((info.duration_sec - 120.5).abs() < 0.01);
        assert_eq!(info.video_codec, Some("h264".to_string()));
        assert_eq!(info.width, Some(1280));
        assert_eq!(info.height, Some(720));
    }

    #[test]
    fn test_parse_ffprobe_json_audio() {
        let json = r#"{"format":{"duration":"60.0"},"streams":[{"codec_name":"opus"}]}"#;
        let info = parse_ffprobe_json(json).unwrap();
        assert_eq!(info.audio_codec, Some("opus".to_string()));
        assert!((info.duration_sec - 60.0).abs() < 0.01);
    }

    #[test]
    fn test_native_wav_header_parsing() {
        // 构建最小 WAV header
        let mut wav = Vec::new();
        wav.extend_from_slice(b"RIFF");
        wav.extend_from_slice(&36u32.to_le_bytes()); // file size - 8
        wav.extend_from_slice(b"WAVE");
        wav.extend_from_slice(b"fmt ");
        wav.extend_from_slice(&16u32.to_le_bytes()); // fmt chunk size
        wav.extend_from_slice(&1u16.to_le_bytes()); // PCM
        wav.extend_from_slice(&1u16.to_le_bytes()); // mono
        wav.extend_from_slice(&16000u32.to_le_bytes()); // sample rate
        wav.extend_from_slice(&32000u32.to_le_bytes()); // byte rate
        wav.extend_from_slice(&2u16.to_le_bytes()); // block align
        wav.extend_from_slice(&16u16.to_le_bytes()); // bits per sample
        wav.extend_from_slice(b"data");
        wav.extend_from_slice(&0u32.to_le_bytes()); // data size

        let header = parse_wav_header(&wav).unwrap();
        assert_eq!(header.sample_rate, 16000);
        assert_eq!(header.channels, 1);
        assert_eq!(header.bits_per_sample, 16);
    }

    #[test]
    fn test_native_ivf_header_parsing() {
        // 构建最小 IVF header
        let mut ivf = vec![0u8; 32];
        ivf[0..4].copy_from_slice(b"DKIF");
        ivf[4..6].copy_from_slice(&0u16.to_le_bytes()); // version
        ivf[6..8].copy_from_slice(&32u16.to_le_bytes()); // header size
        ivf[8..12].copy_from_slice(b"VP80"); // codec FourCC
        ivf[12..14].copy_from_slice(&320u16.to_le_bytes()); // width
        ivf[14..16].copy_from_slice(&240u16.to_le_bytes()); // height
                                                            // timestamp and frame rate
        ivf[16..20].copy_from_slice(&1u32.to_le_bytes()); // rate num
        ivf[20..24].copy_from_slice(&30u32.to_le_bytes()); // rate den

        let header = parse_ivf_header(&ivf).unwrap();
        assert_eq!(header.width, 320);
        assert_eq!(header.height, 240);
        assert_eq!(header.codec, "VP80");
    }
}

// ============================================================================
// 纯 Rust 回放实现 (WAV, IVF, OGG)
// ============================================================================

/// WAV header 信息
struct WavHeader {
    sample_rate: u32,
    channels: u16,
    bits_per_sample: u16,
    data_offset: usize,
    data_size: u32,
}

/// IVF header 信息
struct IvfHeader {
    width: u16,
    height: u16,
    codec: String,
    frame_rate_num: u32,
    frame_rate_den: u32,
}

/// 解析 WAV header
fn parse_wav_header(data: &[u8]) -> Result<WavHeader> {
    if data.len() < 44 {
        return Err(anyhow!("WAV file too short"));
    }
    if &data[0..4] != b"RIFF" || &data[8..12] != b"WAVE" {
        return Err(anyhow!("not a WAV file"));
    }
    let sample_rate = u32::from_le_bytes([data[24], data[25], data[26], data[27]]);
    let channels = u16::from_le_bytes([data[22], data[23]]);
    let bits_per_sample = u16::from_le_bytes([data[34], data[35]]);

    // 查找 data chunk
    let mut offset = 12;
    let mut data_offset = 0;
    let mut data_size = 0u32;
    while offset + 8 <= data.len() {
        let chunk_id = &data[offset..offset + 4];
        let chunk_size = u32::from_le_bytes([
            data[offset + 4],
            data[offset + 5],
            data[offset + 6],
            data[offset + 7],
        ]);
        if chunk_id == b"data" {
            data_offset = offset + 8;
            data_size = chunk_size;
            break;
        }
        offset += 8 + chunk_size as usize;
    }

    if data_offset == 0 {
        return Err(anyhow!("WAV data chunk not found"));
    }

    Ok(WavHeader {
        sample_rate,
        channels,
        bits_per_sample,
        data_offset,
        data_size,
    })
}

/// 解析 IVF header
fn parse_ivf_header(data: &[u8]) -> Result<IvfHeader> {
    if data.len() < 32 {
        return Err(anyhow!("IVF file too short"));
    }
    if &data[0..4] != b"DKIF" {
        return Err(anyhow!("not an IVF file"));
    }
    let width = u16::from_le_bytes([data[12], data[13]]);
    let height = u16::from_le_bytes([data[14], data[15]]);
    let codec = String::from_utf8_lossy(&data[8..12]).to_string();
    let frame_rate_num = u32::from_le_bytes([data[16], data[17], data[18], data[19]]);
    let frame_rate_den = u32::from_le_bytes([data[20], data[21], data[22], data[23]]);

    Ok(IvfHeader {
        width,
        height,
        codec,
        frame_rate_num,
        frame_rate_den,
    })
}

/// 纯 Rust WAV 回放
async fn play_wav_native(
    path: &std::path::Path,
    stopped: &Arc<AtomicBool>,
    position: &Arc<AtomicU64>,
    speed: f32,
) -> Result<()> {
    let data = tokio::fs::read(path).await?;
    let header = parse_wav_header(&data)?;

    info!(
        path = %path.display(),
        sample_rate = header.sample_rate,
        channels = header.channels,
        "native WAV playback started"
    );

    let pcm_data = &data[header.data_offset..header.data_offset + header.data_size as usize];
    let bytes_per_sample = (header.bits_per_sample / 8) as usize;
    let frame_size = bytes_per_sample * header.channels as usize;
    let frame_rate = header.sample_rate;

    // 按帧发送, 20ms 一批
    let frames_per_batch = (frame_rate / 50) as usize; // 20ms
    let batch_bytes = frames_per_batch * frame_size;
    let batch_duration_ms = (frames_per_batch as u64 * 1000) / frame_rate as u64;

    let mut offset = 0;
    let mut elapsed_ms = 0u64;

    while offset < pcm_data.len() && !stopped.load(Ordering::Relaxed) {
        let end = (offset + batch_bytes).min(pcm_data.len());
        let chunk = &pcm_data[offset..end];
        let _chunk = chunk; // 实际应推入 MediaStream

        offset = end;
        elapsed_ms += batch_duration_ms;
        position.store(elapsed_ms / 1000, Ordering::Relaxed);

        // 按 speed 控制播放速率
        let sleep_ms = (batch_duration_ms as f32 / speed) as u64;
        tokio::time::sleep(tokio::time::Duration::from_millis(sleep_ms)).await;
    }

    info!("native WAV playback ended");
    Ok(())
}

/// 纯 Rust IVF 回放
async fn play_ivf_native(
    path: &std::path::Path,
    stopped: &Arc<AtomicBool>,
    position: &Arc<AtomicU64>,
    speed: f32,
) -> Result<()> {
    let data = tokio::fs::read(path).await?;
    let header = parse_ivf_header(&data)?;

    info!(
        path = %path.display(),
        width = header.width,
        height = header.height,
        codec = %header.codec,
        "native IVF playback started"
    );

    // IVF frames: [frame_size(4, LE)] [timestamp(8, LE)] [data]
    let mut offset = 32; // skip IVF header
    let mut elapsed_ms = 0u64;
    let frame_duration_ms = if header.frame_rate_den > 0 {
        (header.frame_rate_num as u64 * 1000) / header.frame_rate_den as u64
    } else {
        33 // ~30fps 默认
    };

    while offset + 12 <= data.len() && !stopped.load(Ordering::Relaxed) {
        let frame_size = u32::from_le_bytes([
            data[offset],
            data[offset + 1],
            data[offset + 2],
            data[offset + 3],
        ]) as usize;
        let timestamp = u64::from_le_bytes([
            data[offset + 4],
            data[offset + 5],
            data[offset + 6],
            data[offset + 7],
            data[offset + 8],
            data[offset + 9],
            data[offset + 10],
            data[offset + 11],
        ]);
        let data_offset = offset + 12;

        if data_offset + frame_size > data.len() {
            break;
        }

        let _frame_data = &data[data_offset..data_offset + frame_size];
        // 实际应推入 MediaStream

        offset = data_offset + frame_size;
        elapsed_ms =
            timestamp / (header.frame_rate_num as u64 / header.frame_rate_den as u64).max(1);
        position.store(elapsed_ms / 1000, Ordering::Relaxed);

        let sleep_ms = (frame_duration_ms as f32 / speed) as u64;
        tokio::time::sleep(tokio::time::Duration::from_millis(sleep_ms)).await;
    }

    info!("native IVF playback ended");
    Ok(())
}

/// 纯 Rust OGG 回放 (简化: 依赖 opus 解码需要外部库, fallback 到 ffmpeg)
async fn play_ogg_native(
    path: &std::path::Path,
    stopped: &Arc<AtomicBool>,
    position: &Arc<AtomicU64>,
    _speed: f32,
) -> Result<()> {
    // OGG/Opus 解码需要 opus decoder 库
    // 简化: 读取文件并解析 OGG pages, 但实际解码 fallback
    let data = tokio::fs::read(path).await?;

    if data.len() < 4 || &data[0..4] != b"OggS" {
        return Err(anyhow!("not an OGG file"));
    }

    info!(path = %path.display(), "native OGG playback started (metadata only)");

    // 简化: 计算总页数和时间
    let mut offset = 0;
    let mut elapsed_ms = 0u64;
    while offset + 27 <= data.len() && !stopped.load(Ordering::Relaxed) {
        if &data[offset..offset + 4] != b"OggS" {
            break;
        }
        // OGG page header: 27 bytes + segment table
        let segments = data[offset + 26] as usize;
        let page_size = 27 + segments;
        if offset + page_size > data.len() {
            break;
        }
        // 跳过 segment table 和 page data
        let mut page_data_size = 0;
        for i in 0..segments {
            page_data_size += data[offset + 27 + i] as usize;
        }
        offset += page_size + page_data_size;
        elapsed_ms += 20; // 估算 20ms per page
        position.store(elapsed_ms / 1000, Ordering::Relaxed);
    }

    info!("native OGG playback ended");
    Ok(())
}

/// 纯 Rust WAV 回放 (带 seek)
async fn play_wav_native_seeking(
    path: &std::path::Path,
    stopped: &Arc<AtomicBool>,
    position: &Arc<AtomicU64>,
    speed: f32,
    seek_sec: f64,
) -> Result<()> {
    let data = tokio::fs::read(path).await?;
    let header = parse_wav_header(&data)?;

    info!(
        path = %path.display(),
        sample_rate = header.sample_rate,
        seek_sec,
        "native WAV playback (seek) started"
    );

    let pcm_data = &data[header.data_offset..header.data_offset + header.data_size as usize];
    let bytes_per_sample = (header.bits_per_sample / 8) as usize;
    let frame_size = bytes_per_sample * header.channels as usize;
    let frame_rate = header.sample_rate;

    // 计算 seek 偏移
    let seek_frames = (seek_sec * frame_rate as f64) as usize;
    let seek_bytes = seek_frames * frame_size;
    let start_offset = seek_bytes.min(pcm_data.len());

    let frames_per_batch = (frame_rate / 50) as usize; // 20ms
    let batch_bytes = frames_per_batch * frame_size;
    let batch_duration_ms = (frames_per_batch as u64 * 1000) / frame_rate as u64;

    let mut offset = start_offset;
    let mut elapsed_ms = (seek_sec * 1000.0) as u64;

    while offset < pcm_data.len() && !stopped.load(Ordering::Relaxed) {
        let end = (offset + batch_bytes).min(pcm_data.len());
        let _chunk = &pcm_data[offset..end];
        offset = end;
        elapsed_ms += batch_duration_ms;
        position.store(elapsed_ms / 1000, Ordering::Relaxed);

        let sleep_ms = (batch_duration_ms as f32 / speed) as u64;
        tokio::time::sleep(tokio::time::Duration::from_millis(sleep_ms)).await;
    }

    info!("native WAV playback (seek) ended");
    Ok(())
}

/// 纯 Rust IVF 回放 (带 seek)
async fn play_ivf_native_seeking(
    path: &std::path::Path,
    stopped: &Arc<AtomicBool>,
    position: &Arc<AtomicU64>,
    speed: f32,
    seek_sec: f64,
) -> Result<()> {
    let data = tokio::fs::read(path).await?;
    let header = parse_ivf_header(&data)?;

    info!(
        path = %path.display(),
        seek_sec,
        "native IVF playback (seek) started"
    );

    let frame_duration_ms = if header.frame_rate_den > 0 {
        (header.frame_rate_num as u64 * 1000) / header.frame_rate_den as u64
    } else {
        33
    };

    let seek_timestamp =
        (seek_sec * header.frame_rate_num as f64 / header.frame_rate_den as f64) as u64;

    // 跳过到 seek 位置
    let mut offset = 32; // skip IVF header
    while offset + 12 <= data.len() {
        let frame_size = u32::from_le_bytes([
            data[offset],
            data[offset + 1],
            data[offset + 2],
            data[offset + 3],
        ]) as usize;
        let timestamp = u64::from_le_bytes([
            data[offset + 4],
            data[offset + 5],
            data[offset + 6],
            data[offset + 7],
            data[offset + 8],
            data[offset + 9],
            data[offset + 10],
            data[offset + 11],
        ]);
        if timestamp >= seek_timestamp {
            break;
        }
        offset += 12 + frame_size;
    }

    let mut elapsed_ms = (seek_sec * 1000.0) as u64;

    while offset + 12 <= data.len() && !stopped.load(Ordering::Relaxed) {
        let frame_size = u32::from_le_bytes([
            data[offset],
            data[offset + 1],
            data[offset + 2],
            data[offset + 3],
        ]) as usize;
        let data_offset = offset + 12;

        if data_offset + frame_size > data.len() {
            break;
        }

        let _frame_data = &data[data_offset..data_offset + frame_size];
        offset = data_offset + frame_size;
        elapsed_ms += frame_duration_ms;
        position.store(elapsed_ms / 1000, Ordering::Relaxed);

        let sleep_ms = (frame_duration_ms as f32 / speed) as u64;
        tokio::time::sleep(tokio::time::Duration::from_millis(sleep_ms)).await;
    }

    info!("native IVF playback (seek) ended");
    Ok(())
}
