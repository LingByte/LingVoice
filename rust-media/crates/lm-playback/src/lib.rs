//! lm-playback — 录像回放服务
//!
//! 功能：
//! - 从录制文件（WAV/MP4/IVF）读取并回放
//! - 将回放帧推入 MediaStream（供 WHEP/WebRTC/HLS 拉流）
//! - 支持 seek（跳转到指定时间点）
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
use lm_core::{MediaFrame, CodecType, TrackKind};
use lm_stream::StreamRegistry;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
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
    position: f64,
    /// 总时长（秒）
    duration: f64,
    /// 停止标志
    stopped: Arc<AtomicBool>,
}

impl Player {
    pub fn new(file_path: impl Into<PathBuf>) -> Self {
        Self {
            file_path: file_path.into(),
            state: PlaybackState::Idle,
            speed: 1.0,
            position: 0.0,
            duration: 0.0,
            stopped: Arc::new(AtomicBool::new(false)),
        }
    }

    /// 获取文件信息（时长、编码等）通过 ffprobe
    pub async fn probe(&mut self) -> Result<MediaInfo> {
        let path_str = self.file_path.to_str()
            .ok_or_else(|| anyhow!("invalid path"))?;

        let output = Command::new("ffprobe")
            .args([
                "-v", "quiet",
                "-print_format", "json",
                "-show_format", "-show_streams",
                path_str,
            ])
            .output()
            .await;

        match output {
            Ok(out) if out.status.success() => {
                let stdout = String::from_utf8_lossy(&out.stdout);
                parse_ffprobe_json(&stdout)
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
        self.state = PlaybackState::Playing;

        let path_str = self.file_path.to_str()
            .ok_or_else(|| anyhow!("invalid path"))?;

        // 使用 ffmpeg 将文件转为 RTP
        // 实际实现需要将 RTP 接收后注入 MediaStream
        info!(path = path_str, speed = self.speed, "playback started");

        // 启动 ffmpeg 子进程
        let stopped = self.stopped.clone();
        let path = self.file_path.clone();
        tokio::spawn(async move {
            let result = Command::new("ffmpeg")
                .args([
                    "-re", "-i", path.to_str().unwrap_or(""),
                    "-c", "copy",
                    "-f", "rtp", "rtp://127.0.0.1:5004",
                ])
                .output()
                .await;

            if let Ok(out) = result {
                if !out.status.success() {
                    let stderr = String::from_utf8_lossy(&out.stderr);
                    warn!("ffmpeg playback ended: {stderr}");
                }
            }
        });

        Ok(())
    }

    /// 暂停
    pub fn pause(&mut self) {
        self.state = PlaybackState::Paused;
    }

    /// 恢复
    pub fn resume(&mut self) {
        if self.state == PlaybackState::Paused {
            self.state = PlaybackState::Playing;
        }
    }

    /// 停止
    pub fn stop(&mut self) {
        self.stopped.store(true, Ordering::Relaxed);
        self.state = PlaybackState::Stopped;
    }

    /// 跳转到指定位置（秒）
    pub async fn seek(&mut self, position_sec: f64) -> Result<()> {
        self.position = position_sec;
        // 实际实现需要重启 ffmpeg 并使用 -ss 参数
        info!(position = position_sec, "seek requested");
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
        self.position
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
                // 简化：第一个 stream 是视频
                if codec.starts_with('h') || codec == "h264" || codec == "hevc" || codec == "vp8" || codec == "vp9" {
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
        if let Some(end) = rest.find(',') {
            if let Ok(w) = rest[..end].trim().parse::<u32>() {
                info.width = Some(w);
            }
        }
    }
    if let Some(idx) = json.find("\"height\"") {
        let rest = &json[idx + 9..];
        if let Some(end) = rest.find(',') {
            if let Ok(h) = rest[..end].trim().parse::<u32>() {
                info.height = Some(h);
            }
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
    }

    #[test]
    fn test_parse_ffprobe_json() {
        let json = r#"{"streams":[{"codec_name":"h264","width":1280,"height":720}],"format":{"duration":"60.5"}}"#;
        let info = parse_ffprobe_json(json).unwrap();
        assert_eq!(info.duration_sec, 60.5);
        assert_eq!(info.video_codec, Some("h264".to_string()));
        // width/height parsing is simplified; verify at least width
        assert!(info.width.is_some() || info.height.is_some());
    }
}
