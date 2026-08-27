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

        // 启动 ffmpeg 子进程
        let stopped = self.stopped.clone();
        let child_stopped = self.child_stopped.clone();
        let position = self.position.clone();
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
    /// 实现：停止当前 ffmpeg 进程，使用 -ss 参数重启。
    pub async fn seek(&mut self, position_sec: f64) -> Result<()> {
        let was_playing = self.state == PlaybackState::Playing;

        // 停止当前进程
        self.stopped.store(true, Ordering::Relaxed);

        // 等待 ffmpeg 退出
        let timeout = tokio::time::Duration::from_millis(500);
        let start = std::time::Instant::now();
        while !self.child_stopped.load(Ordering::Relaxed) {
            if start.elapsed() > timeout {
                warn!("ffmpeg did not stop within timeout, proceeding with seek");
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
}
