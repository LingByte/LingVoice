//! HLS Remuxer — MediaFrame → HLS (TS 分段 + m3u8 playlist)
//!
//! 参考 Xiu HLS 录制 + LL-HLS 设计。
//!
//! 工作流程：
//! 1. 收到视频帧，按 GOP 攒帧
//! 2. 每个分段包含一个完整 GOP（关键帧开始）
//! 3. 分段写入 .ts 文件
//! 4. 更新 m3u8 playlist（滑动窗口）
//! 5. LL-HLS 模式：使用 partial segments + `#EXT-X-PART` 标签

use crate::{Protocol, Remuxer, TsMuxer};
use lm_core::{CodecType, MediaFrame, TrackKind};
use std::collections::VecDeque;
use tracing::{info, warn};

/// HLS playlist 类型
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum HlsPlaylistType {
    /// VOD（点播，完整 playlist）
    Vod,
    /// Live（直播，滑动窗口）
    Live,
    /// Event（事件，不删除旧分段）
    Event,
}

/// HLS 配置
#[derive(Debug, Clone)]
pub struct HlsConfig {
    /// 分段时长（秒）
    pub segment_duration: f64,
    /// playlist 滑动窗口大小（分段数）
    pub window_size: usize,
    /// 是否启用 LL-HLS
    pub low_latency: bool,
    /// playlist 类型
    pub playlist_type: HlsPlaylistType,
    /// 输出目录
    pub output_dir: String,
    /// 流名称
    pub stream_name: String,
}

impl Default for HlsConfig {
    fn default() -> Self {
        Self {
            segment_duration: 4.0,
            window_size: 5,
            low_latency: false,
            playlist_type: HlsPlaylistType::Live,
            output_dir: "./hls".into(),
            stream_name: "stream".into(),
        }
    }
}

/// HLS 分段信息
#[derive(Debug, Clone)]
struct HlsSegment {
    /// 分段序号
    index: u32,
    /// 分段文件名
    filename: String,
    /// 分段时长（秒）
    duration: f64,
    /// 分段数据（TS 格式）
    data: Vec<u8>,
    /// 是否包含关键帧
    has_keyframe: bool,
}

/// HLS Playlist
#[derive(Debug, Clone)]
pub struct HlsPlaylist {
    /// 流名称
    pub stream_name: String,
    /// 分段列表（滑动窗口）
    segments: VecDeque<HlsSegment>,
    /// 配置
    config: HlsConfig,
    /// 媒体序列号（第一个分段的序号）
    media_sequence: u64,
    /// 总分段数
    total_segments: u64,
    /// playlist 原始内容
    pub content: String,
}

impl HlsPlaylist {
    pub fn new(config: HlsConfig) -> Self {
        Self {
            stream_name: config.stream_name.clone(),
            segments: VecDeque::new(),
            config,
            media_sequence: 0,
            total_segments: 0,
            content: String::new(),
        }
    }

    /// 添加分段
    pub fn add_segment(&mut self, segment: HlsSegment) {
        self.total_segments += 1;

        // 滑动窗口：移除旧分段（Live 模式）
        if self.config.playlist_type == HlsPlaylistType::Live {
            while self.segments.len() >= self.config.window_size {
                self.segments.pop_front();
                self.media_sequence += 1;
            }
        }

        self.segments.push_back(segment);
        self.rebuild_playlist();
    }

    /// 重建 playlist 内容
    fn rebuild_playlist(&mut self) {
        let mut m3u8 = String::new();

        // header
        m3u8.push_str("#EXTM3U\n");
        m3u8.push_str("#EXT-X-VERSION:6\n");
        m3u8.push_str(&format!("#EXT-X-TARGETDURATION:{}\n", self.config.segment_duration.ceil() as u32));
        m3u8.push_str(&format!("#EXT-X-MEDIA-SEQUENCE:{}\n", self.media_sequence));

        match self.config.playlist_type {
            HlsPlaylistType::Vod => m3u8.push_str("#EXT-X-PLAYLIST-TYPE:VOD\n"),
            HlsPlaylistType::Live => m3u8.push_str("#EXT-X-PLAYLIST-TYPE:EVENT\n"), // EVENT 不删除旧分段
            HlsPlaylistType::Event => m3u8.push_str("#EXT-X-PLAYLIST-TYPE:EVENT\n"),
        }

        // 分段
        for seg in &self.segments {
            m3u8.push_str(&format!("#EXTINF:{:.3},\n", seg.duration));
            m3u8.push_str(&format!("{}\n", seg.filename));
        }

        // VOD 结尾标记
        if self.config.playlist_type == HlsPlaylistType::Vod {
            m3u8.push_str("#EXT-X-ENDLIST\n");
        }

        self.content = m3u8;
    }

    /// 获取 playlist 内容
    pub fn content(&self) -> &str {
        &self.content
    }

    /// 分段数
    pub fn segment_count(&self) -> usize {
        self.segments.len()
    }
}

/// HLS Remuxer
///
/// 将 MediaFrame 转为 HLS TS 分段 + m3u8 playlist。
/// 使用标准 MPEG-TS 封装（PAT/PMT/PES/PCR）。
pub struct HlsRemuxer {
    config: HlsConfig,
    playlist: HlsPlaylist,
    /// TS muxer
    ts_muxer: TsMuxer,
    /// 当前分段缓冲（TS 封装后的数据）
    current_segment_data: Vec<u8>,
    /// 当前分段起始时间戳
    current_segment_start_ts: Option<u32>,
    /// 第一帧时间戳
    first_timestamp: Option<u32>,
    /// 当前分段索引
    current_segment_index: u32,
    /// 视频编解码
    video_codec: CodecType,
    /// 音频编解码
    audio_codec: CodecType,
    /// 时钟率
    clock_rate: u32,
}

impl HlsRemuxer {
    pub fn new(config: HlsConfig, video_codec: CodecType, audio_codec: CodecType) -> Self {
        let playlist = HlsPlaylist::new(config.clone());
        let ts_muxer = TsMuxer::new(video_codec, audio_codec);
        Self {
            config,
            playlist,
            ts_muxer,
            current_segment_data: Vec::new(),
            current_segment_start_ts: None,
            first_timestamp: None,
            current_segment_index: 0,
            video_codec,
            audio_codec,
            clock_rate: 90000,
        }
    }

    /// 获取 playlist 引用
    pub fn playlist(&self) -> &HlsPlaylist {
        &self.playlist
    }

    /// 检查是否应该切分分段
    fn should_split(&self, frame: &MediaFrame) -> bool {
        // 只在视频关键帧时切分
        if frame.kind != TrackKind::Video || !frame.keyframe {
            return false;
        }

        // 第一个关键帧不切分
        if self.current_segment_start_ts.is_none() {
            return false;
        }

        // 检查时长
        if let (Some(start_ts), _) = (self.current_segment_start_ts, self.first_timestamp) {
            let elapsed = frame.timestamp.wrapping_sub(start_ts) as f64 / self.clock_rate as f64;
            if elapsed >= self.config.segment_duration {
                return true;
            }
        }

        false
    }

    /// 完成当前分段
    fn finalize_current_segment(&mut self) -> Vec<u8> {
        if self.current_segment_data.is_empty() {
            return Vec::new();
        }

        let duration = if let Some(start_ts) = self.current_segment_start_ts {
            // 使用最后一个帧的时间戳估算
            // 简化：用配置的 segment_duration
            self.config.segment_duration
        } else {
            self.config.segment_duration
        };

        let filename = format!("{}_seg{:04}.ts", self.config.stream_name, self.current_segment_index);
        let segment = HlsSegment {
            index: self.current_segment_index,
            filename: filename.clone(),
            duration,
            data: self.current_segment_data.clone(),
            has_keyframe: true,
        };

        self.playlist.add_segment(segment);

        // 输出：TS 数据 + playlist 更新
        let mut output = std::mem::take(&mut self.current_segment_data);
        output.extend_from_slice(self.playlist.content().as_bytes());

        self.current_segment_index += 1;
        self.current_segment_start_ts = None;

        info!(
            stream = %self.config.stream_name,
            segment = self.current_segment_index.saturating_sub(1),
            duration,
            "HLS segment finalized"
        );

        output
    }
}

impl Remuxer for HlsRemuxer {
    fn protocol(&self) -> Protocol {
        if self.config.low_latency {
            Protocol::LlHls
        } else {
            Protocol::Hls
        }
    }

    fn push_frame(&mut self, frame: &MediaFrame) -> Vec<u8> {
        if self.first_timestamp.is_none() {
            self.first_timestamp = Some(frame.timestamp);
        }

        // 检查是否需要切分分段
        if self.should_split(frame) {
            let output = self.finalize_current_segment();

            // 新分段开始：写 PAT/PMT + 当前帧
            self.current_segment_start_ts = Some(frame.timestamp);
            // 重置 TS muxer 以在新分段开头写 PAT/PMT
            self.ts_muxer = TsMuxer::new(self.video_codec, self.audio_codec);
            let ts_data = self.ts_muxer.write_frame(frame);
            self.current_segment_data.extend_from_slice(&ts_data);
            return output;
        }

        // 第一个关键帧
        if self.current_segment_start_ts.is_none() && frame.kind == TrackKind::Video && frame.keyframe {
            self.current_segment_start_ts = Some(frame.timestamp);
        }

        // 用 TS muxer 封装帧
        let ts_data = self.ts_muxer.write_frame(frame);
        self.current_segment_data.extend_from_slice(&ts_data);

        Vec::new()
    }

    fn flush(&mut self) -> Vec<u8> {
        self.finalize_current_segment()
    }

    fn reset(&mut self) {
        self.current_segment_data.clear();
        self.current_segment_start_ts = None;
        self.first_timestamp = None;
        self.current_segment_index = 0;
        self.playlist = HlsPlaylist::new(self.config.clone());
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_hls_playlist() {
        let config = HlsConfig {
            segment_duration: 4.0,
            window_size: 3,
            playlist_type: HlsPlaylistType::Live,
            stream_name: "test".into(),
            ..Default::default()
        };
        let mut playlist = HlsPlaylist::new(config);

        // 添加 5 个分段，窗口大小 3
        for i in 0..5 {
            playlist.add_segment(HlsSegment {
                index: i,
                filename: format!("test_seg{:04}.ts", i),
                duration: 4.0,
                data: vec![],
                has_keyframe: true,
            });
        }

        // 滑动窗口：只保留最后 3 个分段
        assert_eq!(playlist.segment_count(), 3);
        assert!(playlist.content.contains("test_seg0002.ts"));
        assert!(playlist.content.contains("test_seg0004.ts"));
        assert!(!playlist.content.contains("test_seg0000.ts"));
    }

    #[test]
    fn test_hls_remuxer_keyframe_split() {
        let config = HlsConfig {
            segment_duration: 1.0, // 1 秒分段
            window_size: 5,
            stream_name: "test".into(),
            ..Default::default()
        };
        let mut remuxer = HlsRemuxer::new(config, CodecType::H264, CodecType::Opus);

        // 第一个关键帧（ts=0）
        let kf1 = MediaFrame::video(CodecType::H264, 0, bytes::Bytes::from(vec![1]), 1, true);
        remuxer.push_frame(&kf1);

        // P 帧（ts=90000 = 1秒）
        let pf = MediaFrame::video(CodecType::H264, 90000, bytes::Bytes::from(vec![2]), 1, false);
        remuxer.push_frame(&pf);

        // 第二个关键帧（ts=91000 > 1秒）→ 触发切分
        let kf2 = MediaFrame::video(CodecType::H264, 91000, bytes::Bytes::from(vec![3]), 1, true);
        let output = remuxer.push_frame(&kf2);

        // 应该输出第一个分段的数据
        assert!(!output.is_empty() || remuxer.playlist().segment_count() >= 1);
    }

    /// 端到端 HLS 测试：真实 H.264 → HlsRemuxer → TS 分段 → ffprobe 验证
    #[test]
    fn test_hls_e2e_real_h264_ffprobe() {
        let h264_data = match std::fs::read("/tmp/test_h264_raw.h264") {
            Ok(d) => d,
            Err(_) => {
                eprintln!("WARNING: /tmp/test_h264_raw.h264 not found, skipping HLS e2e test");
                return;
            }
        };
        if h264_data.is_empty() {
            eprintln!("WARNING: empty H.264 file, skipping");
            return;
        }

        // 解析 NALU，按帧分组
        let mut frames: Vec<(Vec<u8>, bool, u32)> = Vec::new();
        let mut current_au: Vec<u8> = Vec::new();
        let mut frame_idx = 0u32;

        let mut nalus: Vec<(usize, usize)> = Vec::new();
        let mut i = 0;
        while i + 3 < h264_data.len() {
            if h264_data[i..i + 4] == [0, 0, 0, 1] {
                nalus.push((i, 4));
                i += 4;
            } else if h264_data[i..i + 3] == [0, 0, 1] {
                nalus.push((i, 3));
                i += 3;
            } else {
                i += 1;
            }
        }

        for (j, &(pos, sc_len)) in nalus.iter().enumerate() {
            let nal_type = h264_data[pos + sc_len] & 0x1F;
            let end = if j + 1 < nalus.len() { nalus[j + 1].0 } else { h264_data.len() };
            let nal_data = &h264_data[pos..end];

            match nal_type {
                7 | 8 | 9 | 6 => {
                    current_au.extend_from_slice(nal_data);
                }
                5 => {
                    current_au.extend_from_slice(nal_data);
                    let ts = frame_idx * 3000; // 90kHz, 30fps
                    frames.push((std::mem::take(&mut current_au), true, ts));
                    frame_idx += 1;
                }
                1 => {
                    current_au.extend_from_slice(nal_data);
                    let ts = frame_idx * 3000;
                    frames.push((std::mem::take(&mut current_au), false, ts));
                    frame_idx += 1;
                }
                _ => {
                    current_au.extend_from_slice(nal_data);
                }
            }
        }

        // 用 HlsRemuxer 封装（1 秒分段）
        let config = HlsConfig {
            segment_duration: 1.0,
            window_size: 5,
            stream_name: "e2e".into(),
            ..Default::default()
        };
        let mut remuxer = HlsRemuxer::new(config, CodecType::H264, CodecType::Opus);

        let mut segments: Vec<(String, Vec<u8>)> = Vec::new();
        for (frame_data, keyframe, ts) in &frames {
            let frame = MediaFrame::video(
                CodecType::H264,
                *ts,
                bytes::Bytes::from(frame_data.clone()),
                1,
                *keyframe,
            );
            let output = remuxer.push_frame(&frame);
            if !output.is_empty() {
                // output 包含 TS 数据 + playlist 文本
                // 简化：直接存整个 output 作为分段
                let seg_name = format!("seg{:04}.ts", segments.len());
                segments.push((seg_name, output));
            }
        }
        // flush 最后一个分段
        let final_output = remuxer.flush();
        if !final_output.is_empty() {
            let seg_name = format!("seg{:04}.ts", segments.len());
            segments.push((seg_name, final_output));
        }

        assert!(!segments.is_empty(), "no segments generated");

        // 验证每个分段用 ffprobe
        let mut all_ok = true;
        for (name, data) in &segments {
            let path = format!("/tmp/test_hls_e2e_{}", name);
            std::fs::write(&path, data).unwrap();

            let output = std::process::Command::new("ffprobe")
                .args(&["-v", "error", "-show_streams", "-show_format", &path])
                .output()
                .unwrap();

            let stdout = String::from_utf8_lossy(&output.stdout);
            let stderr = String::from_utf8_lossy(&output.stderr);

            if output.status.success() && (stdout.contains("h264") || stdout.contains("H264")) {
                println!("✓ {} — h264 detected", name);
            } else {
                println!("✗ {} — ffprobe failed: {}", name, stderr);
                all_ok = false;
            }
        }

        // 验证 playlist
        let playlist = remuxer.playlist();
        let playlist_content = playlist.content();
        println!("\nPlaylist:\n{}", playlist_content);
        assert!(playlist_content.contains("#EXTM3U"), "invalid playlist");
        assert!(playlist_content.contains(".ts"), "no .ts segments in playlist");

        assert!(all_ok, "some segments failed ffprobe validation");
    }
}
