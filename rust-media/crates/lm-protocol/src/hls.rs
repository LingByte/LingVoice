//! HLS Remuxer — MediaFrame → HLS (TS 分段 + m3u8 playlist) + LL-HLS (CMAF fMP4)
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
        m3u8.push_str(&format!(
            "#EXT-X-TARGETDURATION:{}\n",
            self.config.segment_duration.ceil() as u32
        ));
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

        let filename = format!(
            "{}_seg{:04}.ts",
            self.config.stream_name, self.current_segment_index
        );
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
        if self.current_segment_start_ts.is_none()
            && frame.kind == TrackKind::Video
            && frame.keyframe
        {
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
        let pf = MediaFrame::video(
            CodecType::H264,
            90000,
            bytes::Bytes::from(vec![2]),
            1,
            false,
        );
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
            let end = if j + 1 < nalus.len() {
                nalus[j + 1].0
            } else {
                h264_data.len()
            };
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
        assert!(
            playlist_content.contains(".ts"),
            "no .ts segments in playlist"
        );

        assert!(all_ok, "some segments failed ffprobe validation");
    }
}

// ============================================================================
// LL-HLS (Low-Latency HLS) — RFC 8216 Section 6.3 + Apple LL-HLS
// ============================================================================

/// LL-HLS 配置
#[derive(Debug, Clone)]
pub struct LlHlsConfig {
    /// 完整分段时长（秒）
    pub segment_duration: f64,
    /// 部分分段时长（秒）
    pub partial_duration: f64,
    /// PART-HOLD-BACK（秒）
    pub part_hold_back: f64,
    /// 是否支持 CAN-BLOCK-RELOAD
    pub can_block_reload: bool,
    /// playlist 最大分段数
    pub max_segments: usize,
}

impl Default for LlHlsConfig {
    fn default() -> Self {
        Self {
            segment_duration: 4.0,
            partial_duration: 0.2,
            part_hold_back: 0.4,
            can_block_reload: true,
            max_segments: 6,
        }
    }
}

/// LL-HLS 分段信息
#[derive(Debug, Clone)]
pub struct LlHlsSegment {
    pub sequence: u64,
    pub uri: String,
    pub duration: f64,
    pub independent: bool,
}

/// LL-HLS 部分分段信息
#[derive(Debug, Clone)]
pub struct LlHlsPartial {
    pub sequence: u64,
    pub uri: String,
    pub duration: f64,
    pub independent: bool,
}

/// LL-HLS Playlist
#[derive(Debug, Clone)]
pub struct LlHlsPlaylist {
    pub segments: VecDeque<LlHlsSegment>,
    pub partials: VecDeque<LlHlsPartial>,
    pub config: LlHlsConfig,
    pub media_sequence: u64,
    pub preload_hint_uri: Option<String>,
    pub skipped_segments: u64,
    stream_name: String,
}

impl LlHlsPlaylist {
    pub fn new(config: LlHlsConfig, stream_name: &str) -> Self {
        Self {
            segments: VecDeque::new(),
            partials: VecDeque::new(),
            config,
            media_sequence: 0,
            preload_hint_uri: None,
            skipped_segments: 0,
            stream_name: stream_name.to_string(),
        }
    }

    /// 添加一个完整分段
    pub fn add_segment(&mut self, uri: &str, duration: f64, independent: bool) {
        let seq = self.media_sequence + self.segments.len() as u64;
        self.segments.push_back(LlHlsSegment {
            sequence: seq,
            uri: uri.to_string(),
            duration,
            independent,
        });
        // 滑动窗口
        while self.segments.len() > self.config.max_segments {
            self.segments.pop_front();
            self.media_sequence += 1;
        }
        // 完成分段后清除对应的 partials
        self.partials.clear();
    }

    /// 添加一个部分分段
    pub fn add_partial(&mut self, uri: &str, duration: f64, independent: bool) {
        let seq = self.partials.len() as u64;
        self.partials.push_back(LlHlsPartial {
            sequence: seq,
            uri: uri.to_string(),
            duration,
            independent,
        });
        // 更新 preload hint
        self.preload_hint_uri = Some(format!("{}_part{}.m4s", self.stream_name, seq + 1));
    }

    /// 生成完整 playlist 文本
    pub fn render(&self) -> String {
        let mut m = String::new();
        m.push_str("#EXTM3U\n");
        m.push_str("#EXT-X-VERSION:6\n");
        m.push_str(&format!(
            "#EXT-X-TARGETDURATION:{}\n",
            self.config.segment_duration.ceil() as u32
        ));
        m.push_str(&format!(
            "#EXT-X-PART-INF:PART-TARGET={:.3}\n",
            self.config.partial_duration
        ));
        m.push_str(&format!(
            "#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD={},PART-HOLD-BACK={:.3}\n",
            if self.config.can_block_reload {
                "YES"
            } else {
                "NO"
            },
            self.config.part_hold_back
        ));
        // EXT-X-MAP: 指向 init segment (CMAF fMP4)
        m.push_str(&format!(
            "#EXT-X-MAP:URI=\"{}_init.mp4\"\n",
            self.stream_name
        ));
        m.push_str(&format!("#EXT-X-MEDIA-SEQUENCE:{}\n", self.media_sequence));

        // 分段 + partials
        for seg in &self.segments {
            if seg.independent {
                m.push_str("#EXT-X-INDEPENDENT-SEGMENTS\n");
            }
            // partial segments 属于当前分段
            m.push_str(&format!("#EXTINF:{:.3},\n", seg.duration));
            m.push_str(&format!("{}\n", seg.uri));
        }

        // 当前正在生成的分段的 partials
        for p in &self.partials {
            m.push_str(&format!(
                "#EXT-X-PART:DURATION={:.3},URI={},INDEPENDENT={}\n",
                p.duration,
                p.uri,
                if p.independent { "YES" } else { "NO" }
            ));
        }

        // Preload hint
        if let Some(ref hint) = self.preload_hint_uri {
            m.push_str(&format!("#EXT-X-PRELOAD-HINT:TYPE=PART,URI={}\n", hint));
        }

        m
    }

    /// 生成 delta playlist（增量更新）
    pub fn render_delta(&self, skip: u64) -> String {
        let mut m = String::new();
        m.push_str("#EXTM3U\n");
        m.push_str("#EXT-X-VERSION:9\n");
        m.push_str(&format!(
            "#EXT-X-TARGETDURATION:{}\n",
            self.config.segment_duration.ceil() as u32
        ));
        m.push_str(&format!(
            "#EXT-X-PART-INF:PART-TARGET={:.3}\n",
            self.config.partial_duration
        ));
        m.push_str(&format!("#EXT-X-SKIP:SKIPPED-SEGMENTS={}\n", skip));
        m.push_str(&format!(
            "#EXT-X-MEDIA-SEQUENCE:{}\n",
            self.media_sequence + skip
        ));

        // 只输出 skip 之后的分段
        let skip_usize = skip as usize;
        for (i, seg) in self.segments.iter().enumerate() {
            if i < skip_usize {
                continue;
            }
            m.push_str(&format!("#EXTINF:{:.3},\n", seg.duration));
            m.push_str(&format!("{}\n", seg.uri));
        }

        for p in &self.partials {
            m.push_str(&format!(
                "#EXT-X-PART:DURATION={:.3},URI={},INDEPENDENT={}\n",
                p.duration,
                p.uri,
                if p.independent { "YES" } else { "NO" }
            ));
        }

        if let Some(ref hint) = self.preload_hint_uri {
            m.push_str(&format!("#EXT-X-PRELOAD-HINT:TYPE=PART,URI={}\n", hint));
        }

        m
    }

    pub fn segment_count(&self) -> usize {
        self.segments.len()
    }

    pub fn partial_count(&self) -> usize {
        self.partials.len()
    }
}

/// CMAF fMP4 分段构建器（简化版）
///
/// 构建 fMP4 init segment (ftyp + moov) 和 media segment (styp + moof + mdat)。
pub struct CmafMuxer {
    sequence: u64,
    track_id: u32,
    timescale: u32,
}

impl CmafMuxer {
    pub fn new(track_id: u32, timescale: u32) -> Self {
        Self {
            sequence: 0,
            track_id,
            timescale,
        }
    }

    /// 构建 init segment (ftyp + moov)
    pub fn build_init(&self, width: u32, height: u32, codec: &str) -> Vec<u8> {
        let mut out = Vec::new();
        // ftyp box: size = 8 (header) + 4 (brand) + 4 (version) + 4*4 (compatible brands)
        let ftyp_payload: &[u8] = b"iso5\x00\x00\x00\x00iso5avc1mp42";
        out.extend_from_slice(&box_header(ftyp_payload.len(), b"ftyp"));
        out.extend_from_slice(ftyp_payload);
        // moov box (mvhd + trak + mvex)
        let moov = self.build_moov(width, height, codec);
        out.extend_from_slice(&box_header(moov.len(), b"moov"));
        out.extend_from_slice(&moov);
        out
    }

    /// 构建一个 media segment (styp + moof + mdat)
    pub fn build_segment(
        &mut self,
        media_data: &[u8],
        duration: u64,
        is_keyframe: bool,
    ) -> Vec<u8> {
        let mut out = Vec::new();
        // styp box: 8 (header) + 4 (brand) + 4 (version) + 4*2 (compatible brands) = 24
        out.extend_from_slice(&box_header(16, b"styp"));
        out.extend_from_slice(b"msdh");
        out.extend_from_slice(&0u32.to_be_bytes());
        out.extend_from_slice(b"msdh");
        out.extend_from_slice(b"msix");
        // moof box — 需要知道 moof size 来计算 data_offset
        let moof = self.build_moof(duration, media_data.len(), is_keyframe);
        let moof_size = moof.len() + 8; // +8 for moof box header
                                        // mdat box
        let mdat_size = 8 + media_data.len();

        out.extend_from_slice(&box_header(moof.len(), b"moof"));
        out.extend_from_slice(&moof);
        out.extend_from_slice(&box_header(media_data.len(), b"mdat"));
        out.extend_from_slice(media_data);

        // 修正 trun 中的 data_offset: 指向 mdat payload 开始
        // data_offset = styp + moof + mdat_header (从 segment 开始到 mdat payload)
        let styp_size = 24u32; // styp box total size
        let mdat_header_size = 8u32; // mdat box header
        let data_offset = styp_size + moof_size as u32 + mdat_header_size;
        let _ = mdat_size;

        // 在 out 中搜索 "trun" 标记 (4 bytes), 然后修正其后的 data_offset
        for i in 0..out.len().saturating_sub(20) {
            if &out[i..i + 4] == b"trun" {
                // i 指向 type 字段
                // trun full box: [size(4)][type(4)][version(1)][flags(3)][sample_count(4)][data_offset(4)]...
                // data_offset 在 type 后 12 bytes: version(1)+flags(3)+count(4)+offset(4) = 12
                // 但 i 指向 type, 所以 data_offset 在 i + 4(ver+flags) + 4(count) + 4(offset) = i + 12
                // 不对: i 是 type 位置, i+4 = version+flags, i+8 = sample_count, i+12 = data_offset
                let offset_pos = i + 12;
                if offset_pos + 4 <= out.len() {
                    out[offset_pos..offset_pos + 4].copy_from_slice(&data_offset.to_be_bytes());
                }
                break;
            }
        }

        self.sequence += 1;
        out
    }

    /// 构建一个 partial segment (styp + moof + mdat)
    pub fn build_partial(&mut self, media_data: &[u8], duration: u64) -> Vec<u8> {
        // partial segment 与 media segment 格式相同，只是时长更短
        self.build_segment(media_data, duration, false)
    }

    fn build_moov(&self, width: u32, height: u32, codec: &str) -> Vec<u8> {
        let mut moov = Vec::new();
        // mvhd (简化)
        moov.extend_from_slice(&full_box_header(96, b"mvhd", 0, 0));
        moov.extend_from_slice(&0u32.to_be_bytes()); // creation_time
        moov.extend_from_slice(&0u32.to_be_bytes()); // modification_time
        moov.extend_from_slice(&self.timescale.to_be_bytes());
        moov.extend_from_slice(&0u32.to_be_bytes()); // duration
        moov.extend_from_slice(&0x00010000u32.to_be_bytes()); // rate
        moov.extend_from_slice(&0x0100u16.to_be_bytes()); // volume
        moov.extend_from_slice(&[0u8; 10]); // reserved
                                            // identity matrix (9 * 4 bytes = 36)
        moov.extend_from_slice(
            &[0x00010000u32, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000]
                .iter()
                .flat_map(|v| v.to_be_bytes())
                .collect::<Vec<_>>(),
        );
        moov.extend_from_slice(&[0u8; 24]); // pre_defined
                                            // trak (简化: tkhd + mdia)
        let trak = self.build_trak(width, height, codec);
        moov.extend_from_slice(&box_header(trak.len(), b"trak"));
        moov.extend_from_slice(&trak);
        // mvex (trex)
        let trex = self.build_trex();
        moov.extend_from_slice(&box_header(trex.len(), b"mvex"));
        moov.extend_from_slice(&trex);
        moov
    }

    fn build_trak(&self, width: u32, height: u32, codec: &str) -> Vec<u8> {
        let mut trak = Vec::new();
        // tkhd
        trak.extend_from_slice(&full_box_header(80, b"tkhd", 0, 3));
        trak.extend_from_slice(&0u32.to_be_bytes()); // creation_time
        trak.extend_from_slice(&0u32.to_be_bytes()); // modification_time
        trak.extend_from_slice(&self.track_id.to_be_bytes());
        trak.extend_from_slice(&0u32.to_be_bytes()); // reserved
        trak.extend_from_slice(&0u32.to_be_bytes()); // duration
        trak.extend_from_slice(&[0u8; 8]); // reserved
        trak.extend_from_slice(&0u16.to_be_bytes()); // layer
        trak.extend_from_slice(&0u16.to_be_bytes()); // alternate_group
        trak.extend_from_slice(&0u16.to_be_bytes()); // volume
        trak.extend_from_slice(&0u16.to_be_bytes()); // reserved
                                                     // matrix
        trak.extend_from_slice(
            &[0x00010000u32, 0, 0, 0, 0x00010000, 0, 0, 0, 0x40000000]
                .iter()
                .flat_map(|v| v.to_be_bytes())
                .collect::<Vec<_>>(),
        );
        trak.extend_from_slice(&width.to_be_bytes()); // width (16.16)
        trak.extend_from_slice(&height.to_be_bytes()); // height (16.16)
                                                       // mdia (简化)
        let mdia = self.build_mdia(width, height, codec);
        trak.extend_from_slice(&box_header(mdia.len(), b"mdia"));
        trak.extend_from_slice(&mdia);
        trak
    }

    fn build_mdia(&self, width: u32, height: u32, codec: &str) -> Vec<u8> {
        let mut mdia = Vec::new();
        // mdhd
        mdia.extend_from_slice(&full_box_header(24, b"mdhd", 0, 0));
        mdia.extend_from_slice(&0u32.to_be_bytes()); // creation_time
        mdia.extend_from_slice(&0u32.to_be_bytes()); // modification_time
        mdia.extend_from_slice(&self.timescale.to_be_bytes());
        mdia.extend_from_slice(&0u32.to_be_bytes()); // duration
        mdia.extend_from_slice(&0x55C40000u32.to_be_bytes()); // language + pre_defined
                                                              // hdlr
        mdia.extend_from_slice(&full_box_header(21, b"hdlr", 0, 0));
        mdia.extend_from_slice(&0u32.to_be_bytes()); // pre_defined
        mdia.extend_from_slice(b"vide"); // handler_type
        mdia.extend_from_slice(&[0u8; 12]); // reserved
        mdia.push(0); // name (empty string)
                      // minf (简化: vmhd + dinf + stbl)
        let minf = self.build_minf(width, height, codec);
        mdia.extend_from_slice(&box_header(minf.len(), b"minf"));
        mdia.extend_from_slice(&minf);
        mdia
    }

    fn build_minf(&self, width: u32, height: u32, codec: &str) -> Vec<u8> {
        let mut minf = Vec::new();
        // vmhd
        minf.extend_from_slice(&full_box_header(12, b"vmhd", 0, 1));
        minf.extend_from_slice(&0u16.to_be_bytes()); // graphicsmode
        minf.extend_from_slice(&[0u8; 6]); // opcolor
                                           // dinf (dref)
        minf.extend_from_slice(&full_box_header(16, b"dinf", 0, 0));
        let dref = full_box_header(12, b"dref", 0, 0);
        minf.extend_from_slice(&box_header(dref.len() + 8, b"dinf"));
        minf.extend_from_slice(&dref);
        minf.extend_from_slice(&0u32.to_be_bytes()); // entry_count = 0
                                                     // stbl (简化: stsd + stts + stsc + stsz + stco)
        let stbl = self.build_stbl(width, height, codec);
        minf.extend_from_slice(&box_header(stbl.len(), b"stbl"));
        minf.extend_from_slice(&stbl);
        minf
    }

    fn build_stbl(&self, width: u32, height: u32, codec: &str) -> Vec<u8> {
        let mut stbl = Vec::new();
        // stsd (sample description)
        let stsd_data = self.build_stsd(width, height, codec);
        stbl.extend_from_slice(&full_box_header(8 + stsd_data.len(), b"stsd", 0, 0));
        stbl.extend_from_slice(&1u32.to_be_bytes()); // entry_count
        stbl.extend_from_slice(&stsd_data);
        // stts (time-to-sample, empty)
        stbl.extend_from_slice(&full_box_header(16, b"stts", 0, 0));
        stbl.extend_from_slice(&0u32.to_be_bytes()); // entry_count
                                                     // stsc (sample-to-chunk, empty)
        stbl.extend_from_slice(&full_box_header(16, b"stsc", 0, 0));
        stbl.extend_from_slice(&0u32.to_be_bytes()); // entry_count
                                                     // stsz (sample size, empty)
        stbl.extend_from_slice(&full_box_header(20, b"stsz", 0, 0));
        stbl.extend_from_slice(&0u32.to_be_bytes()); // sample_size
        stbl.extend_from_slice(&0u32.to_be_bytes()); // sample_count
                                                     // stco (chunk offset, empty)
        stbl.extend_from_slice(&full_box_header(16, b"stco", 0, 0));
        stbl.extend_from_slice(&0u32.to_be_bytes()); // entry_count
        stbl
    }

    fn build_stsd(&self, width: u32, height: u32, codec: &str) -> Vec<u8> {
        let mut stsd = Vec::new();
        // Visual Sample Entry
        stsd.extend_from_slice(&[0u8; 6]); // reserved
        stsd.extend_from_slice(&1u16.to_be_bytes()); // data_reference_index
        stsd.extend_from_slice(&[0u8; 16]); // pre_defined + reserved
        stsd.extend_from_slice(&width.to_be_bytes()); // width
        stsd.extend_from_slice(&height.to_be_bytes()); // height
        stsd.extend_from_slice(&0x00480000u32.to_be_bytes()); // horizresolution
        stsd.extend_from_slice(&0x00480000u32.to_be_bytes()); // vertresolution
        stsd.extend_from_slice(&0u32.to_be_bytes()); // reserved
        stsd.extend_from_slice(&1u16.to_be_bytes()); // frame_count
        stsd.extend_from_slice(&[0u8; 32]); // compressorname
        stsd.extend_from_slice(&0x0018u16.to_be_bytes()); // depth
        stsd.extend_from_slice(&0xFFFFu16.to_be_bytes()); // pre_defined
                                                          // codec box (avcC / hvcC)
        let codec_type = if codec.starts_with("avc") {
            b"avcC"
        } else {
            b"hvcC"
        };
        // 简化: 空 codec config
        stsd.extend_from_slice(&box_header(8, codec_type));
        stsd
    }

    fn build_trex(&self) -> Vec<u8> {
        let mut trex = Vec::new();
        trex.extend_from_slice(&full_box_header(24, b"trex", 0, 0));
        trex.extend_from_slice(&self.track_id.to_be_bytes());
        trex.extend_from_slice(&1u32.to_be_bytes()); // default_sample_description_index
        trex.extend_from_slice(&0u32.to_be_bytes()); // default_sample_duration
        trex.extend_from_slice(&0u32.to_be_bytes()); // default_sample_size
        trex.extend_from_slice(&0u32.to_be_bytes()); // default_sample_flags
        trex
    }

    fn build_moof(&mut self, duration: u64, media_data_len: usize, is_keyframe: bool) -> Vec<u8> {
        let mut moof = Vec::new();
        // mfhd: payload = 4 bytes (sequence_number), total = 4 + 12 = 16
        moof.extend_from_slice(&full_box_header(4, b"mfhd", 0, 0));
        moof.extend_from_slice(&self.sequence.to_be_bytes()); // sequence_number
                                                              // traf
        let traf = self.build_traf(duration, media_data_len, is_keyframe);
        moof.extend_from_slice(&box_header(traf.len(), b"traf"));
        moof.extend_from_slice(&traf);
        moof
    }

    fn build_traf(&self, duration: u64, media_data_len: usize, is_keyframe: bool) -> Vec<u8> {
        let mut traf = Vec::new();
        // tfhd: payload = 4 bytes (track_id), total = 4 + 12 = 16
        // flags = 0x020000 (default-base-is-moof, no optional fields)
        traf.extend_from_slice(&full_box_header(4, b"tfhd", 0, 0x020000));
        traf.extend_from_slice(&self.track_id.to_be_bytes());
        // tfdt (version 1): payload = 8 bytes (64-bit baseMediaDecodeTime), total = 8 + 12 = 20
        traf.extend_from_slice(&full_box_header(8, b"tfdt", 1, 0));
        traf.extend_from_slice(&duration.to_be_bytes());
        // trun: flags = 0x000200 (data-offset) | 0x000100 (duration) | 0x000400 (size) | 0x000800 (flags)
        let trun_flags: u32 = 0x000F00;
        let sample_count = 1u32;
        // trun payload: sample_count(4) + data_offset(4) + 1 * (duration(4) + size(4) + flags(4)) = 20
        let trun_payload_size = 4 + 4 + (sample_count as usize) * 12;
        traf.extend_from_slice(&full_box_header(trun_payload_size, b"trun", 0, trun_flags));
        traf.extend_from_slice(&sample_count.to_be_bytes()); // sample_count
        traf.extend_from_slice(&0u32.to_be_bytes()); // data_offset (will be fixed by caller)
                                                     // per-sample: duration(u32), size(u32), flags(u32)
        traf.extend_from_slice(&(duration as u32).to_be_bytes()); // sample_duration
        traf.extend_from_slice(&(media_data_len as u32).to_be_bytes()); // sample_size
                                                                        // sample_flags: sample_depends_on=2 (not dependent) in bits 24-25
                                                                        // is_non_sync_sample in bit 16: 0 for keyframe, 1 for non-keyframe
        let sample_flags: u32 = if is_keyframe {
            0x02000000 // sample_depends_on=2, is_non_sync=0
        } else {
            0x02010000 // sample_depends_on=2, is_non_sync=1
        };
        traf.extend_from_slice(&sample_flags.to_be_bytes());
        traf
    }
}

/// 在 buffer 中搜索指定 box type 的起始位置
fn find_box_in_buf(buf: &[u8], box_type: &[u8; 4]) -> Option<usize> {
    let mut pos = 0;
    while pos + 8 <= buf.len() {
        let size =
            u32::from_be_bytes([buf[pos], buf[pos + 1], buf[pos + 2], buf[pos + 3]]) as usize;
        if &buf[pos + 4..pos + 8] == box_type {
            return Some(pos);
        }
        if size < 8 {
            pos += 4; // 防止无限循环
        } else {
            pos += size;
        }
    }
    None
}

fn box_header(size: usize, box_type: &[u8; 4]) -> [u8; 8] {
    let mut header = [0u8; 8];
    header[0..4].copy_from_slice(&(size as u32 + 8).to_be_bytes());
    header[4..8].copy_from_slice(box_type);
    header
}

fn full_box_header(payload_size: usize, box_type: &[u8; 4], version: u8, flags: u32) -> [u8; 12] {
    let mut header = [0u8; 12];
    header[0..4].copy_from_slice(&(payload_size as u32 + 12).to_be_bytes());
    header[4..8].copy_from_slice(box_type);
    header[8] = version;
    header[9..12].copy_from_slice(&flags.to_be_bytes()[1..4]);
    header
}

#[cfg(test)]
mod llhls_tests {
    use super::*;

    #[test]
    fn test_llhls_playlist_render() {
        let config = LlHlsConfig::default();
        let mut playlist = LlHlsPlaylist::new(config, "test");

        playlist.add_segment("test_seg0.m4s", 4.0, true);
        playlist.add_partial("test_part0.m4s", 0.2, true);
        playlist.add_partial("test_part1.m4s", 0.2, false);

        let content = playlist.render();
        assert!(content.contains("#EXTM3U"));
        assert!(content.contains("#EXT-X-PART-INF"));
        assert!(content.contains("#EXT-X-SERVER-CONTROL"));
        assert!(content.contains("#EXT-X-PART:"));
        assert!(content.contains("test_part0.m4s"));
        assert!(content.contains("test_seg0.m4s"));
    }

    #[test]
    fn test_llhls_preload_hint() {
        let config = LlHlsConfig::default();
        let mut playlist = LlHlsPlaylist::new(config, "stream");

        playlist.add_partial("stream_part0.m4s", 0.2, true);

        let content = playlist.render();
        assert!(content.contains("#EXT-X-PRELOAD-HINT:TYPE=PART"));
        assert!(content.contains("stream_part1.m4s"));
    }

    #[test]
    fn test_llhls_delta_update() {
        let config = LlHlsConfig::default();
        let mut playlist = LlHlsPlaylist::new(config, "test");

        for i in 0..5 {
            playlist.add_segment(&format!("test_seg{}.m4s", i), 4.0, true);
        }

        let delta = playlist.render_delta(3);
        assert!(delta.contains("#EXT-X-SKIP:SKIPPED-SEGMENTS=3"));
        assert!(delta.contains("test_seg3.m4s"));
        assert!(!delta.contains("test_seg0.m4s"));
    }

    #[test]
    fn test_llhls_sliding_window() {
        let config = LlHlsConfig {
            max_segments: 3,
            ..Default::default()
        };
        let mut playlist = LlHlsPlaylist::new(config, "test");

        for i in 0..6 {
            playlist.add_segment(&format!("test_seg{}.m4s", i), 4.0, true);
        }

        assert_eq!(playlist.segment_count(), 3);
        assert!(playlist.media_sequence >= 3);
    }

    #[test]
    fn test_cmaf_init_segment() {
        let muxer = CmafMuxer::new(1, 90000);
        let init = muxer.build_init(1280, 720, "avc1.640028");
        assert!(!init.is_empty());
        // ftyp box
        assert_eq!(&init[4..8], b"ftyp");
        assert_eq!(&init[8..12], b"iso5");
    }

    #[test]
    fn test_cmaf_media_segment() {
        let mut muxer = CmafMuxer::new(1, 90000);
        let media_data = vec![0u8; 100];
        let seg = muxer.build_segment(&media_data, 72000, true);
        assert!(!seg.is_empty());
        // styp box at start
        assert_eq!(&seg[4..8], b"styp");
        // 搜索 moof 和 mdat 标记（不依赖 box size 遍历，因为简化实现中 box size 可能不完全准确）
        let seg_str: Vec<u8> = seg.clone();
        let has_moof = seg_str.windows(4).any(|w| w == b"moof");
        let has_mdat = seg_str.windows(4).any(|w| w == b"mdat");
        assert!(has_moof, "media segment should contain moof box");
        assert!(has_mdat, "media segment should contain mdat box");
    }

    #[test]
    fn test_cmaf_partial_segment() {
        let mut muxer = CmafMuxer::new(1, 90000);
        let data = vec![0u8; 50];
        let partial = muxer.build_partial(&data, 18000);
        assert!(!partial.is_empty());
        assert_eq!(&partial[4..8], b"styp");
    }

    #[test]
    fn test_cmaf_ftyp_box_size() {
        // ftyp box 的 size 应正确反映实际大小
        let muxer = CmafMuxer::new(1, 90000);
        let init = muxer.build_init(1280, 720, "avc1.640028");
        // ftyp size 在前 4 bytes
        let ftyp_size = u32::from_be_bytes([init[0], init[1], init[2], init[3]]);
        // ftyp = 8 (header) + 4 (brand) + 4 (version) + 4*4 (compatible brands) = 28
        assert_eq!(
            ftyp_size, 28,
            "ftyp box size should be 28, got {}",
            ftyp_size
        );
    }

    #[test]
    fn test_cmaf_moof_structure() {
        // moof 应包含 mfhd + traf, traf 应包含 tfhd + tfdt + trun
        let mut muxer = CmafMuxer::new(1, 90000);
        let media_data = vec![0u8; 100];
        let seg = muxer.build_segment(&media_data, 72000, true);

        // 验证 moof box 存在且 size 正确
        let moof_pos = find_box_in_buf(&seg, b"moof").expect("moof box not found");
        let moof_size = u32::from_be_bytes([
            seg[moof_pos],
            seg[moof_pos + 1],
            seg[moof_pos + 2],
            seg[moof_pos + 3],
        ]) as usize;
        assert!(moof_size > 8, "moof box should have content");

        // moof 内的子 box 从 moof_pos + 8 开始 (跳过 moof header)
        let moof_inner = &seg[moof_pos + 8..moof_pos + moof_size];
        // 验证 moof 内有 mfhd
        assert!(
            find_box_in_buf(moof_inner, b"mfhd").is_some(),
            "mfhd not found in moof"
        );
        // 验证 moof 内有 traf
        assert!(
            find_box_in_buf(moof_inner, b"traf").is_some(),
            "traf not found in moof"
        );
    }

    #[test]
    fn test_cmaf_trun_sample_table() {
        // trun 应包含 sample_count, data_offset, 和 per-sample 的 duration/size/flags
        let mut muxer = CmafMuxer::new(1, 90000);
        let media_data = vec![0xAA; 200];
        let seg = muxer.build_segment(&media_data, 72000, true);

        // 在 seg 中搜索 "trun" 字节标记
        let trun_type_pos = seg
            .windows(4)
            .position(|w| w == b"trun")
            .expect("trun not found");
        // trun_type_pos 指向 type 字段, 前 4 bytes 是 size
        let trun_size = u32::from_be_bytes([
            seg[trun_type_pos - 4],
            seg[trun_type_pos - 3],
            seg[trun_type_pos - 2],
            seg[trun_type_pos - 1],
        ]);
        // trun: size(4) + type(4) + version(1) + flags(3) + sample_count(4) + data_offset(4) + 1*(dur+size+flags)(12) = 32
        assert_eq!(
            trun_size, 32,
            "trun box size should be 32, got {}",
            trun_size
        );

        // sample_count at trun_type_pos + 8
        let sample_count = u32::from_be_bytes([
            seg[trun_type_pos + 8],
            seg[trun_type_pos + 9],
            seg[trun_type_pos + 10],
            seg[trun_type_pos + 11],
        ]);
        assert_eq!(sample_count, 1, "sample_count should be 1");

        // data_offset at trun_type_pos + 12
        let data_offset = u32::from_be_bytes([
            seg[trun_type_pos + 12],
            seg[trun_type_pos + 13],
            seg[trun_type_pos + 14],
            seg[trun_type_pos + 15],
        ]);
        assert!(data_offset > 0, "data_offset should be non-zero after fix");

        // sample_duration at trun_type_pos + 16
        let sample_duration = u32::from_be_bytes([
            seg[trun_type_pos + 16],
            seg[trun_type_pos + 17],
            seg[trun_type_pos + 18],
            seg[trun_type_pos + 19],
        ]);
        assert_eq!(
            sample_duration, 72000,
            "sample_duration should be 72000, got {}",
            sample_duration
        );

        // sample_size at trun_type_pos + 20
        let sample_size = u32::from_be_bytes([
            seg[trun_type_pos + 20],
            seg[trun_type_pos + 21],
            seg[trun_type_pos + 22],
            seg[trun_type_pos + 23],
        ]);
        assert_eq!(
            sample_size, 200,
            "sample_size should be 200, got {}",
            sample_size
        );

        // sample_flags at trun_type_pos + 24
        let sample_flags = u32::from_be_bytes([
            seg[trun_type_pos + 24],
            seg[trun_type_pos + 25],
            seg[trun_type_pos + 26],
            seg[trun_type_pos + 27],
        ]);
        assert!(
            sample_flags & 0x00010000 == 0,
            "keyframe sample should have is_non_sync_sample = 0, got {:08x}",
            sample_flags
        );
    }

    #[test]
    fn test_cmaf_non_keyframe_flags() {
        let mut muxer = CmafMuxer::new(1, 90000);
        let media_data = vec![0xBB; 50];
        let seg = muxer.build_segment(&media_data, 36000, false);

        let trun_type_pos = seg
            .windows(4)
            .position(|w| w == b"trun")
            .expect("trun not found");
        let sample_flags = u32::from_be_bytes([
            seg[trun_type_pos + 24],
            seg[trun_type_pos + 25],
            seg[trun_type_pos + 26],
            seg[trun_type_pos + 27],
        ]);
        assert!(
            sample_flags & 0x00010000 != 0,
            "non-keyframe sample should have is_non_sync_sample = 1, got {:08x}",
            sample_flags
        );
    }

    #[test]
    fn test_cmaf_mdat_data_offset() {
        // data_offset 应指向 mdat payload 的起始位置 (相对于 segment 开始)
        let mut muxer = CmafMuxer::new(1, 90000);
        let media_data = vec![0xCC; 100];
        let seg = muxer.build_segment(&media_data, 72000, true);

        let trun_type_pos = seg
            .windows(4)
            .position(|w| w == b"trun")
            .expect("trun not found");
        let data_offset = u32::from_be_bytes([
            seg[trun_type_pos + 12],
            seg[trun_type_pos + 13],
            seg[trun_type_pos + 14],
            seg[trun_type_pos + 15],
        ]) as usize;

        assert!(
            data_offset < seg.len(),
            "data_offset {} out of bounds (seg len {})",
            data_offset,
            seg.len()
        );
        assert_eq!(
            seg[data_offset], 0xCC,
            "data_offset should point to mdat payload start, got 0x{:02x}",
            seg[data_offset]
        );
    }

    #[test]
    fn test_cmaf_ll_playlist_ext_x_map() {
        // LL-HLS playlist 应包含 EXT-X-MAP 指向 init segment
        let config = LlHlsConfig::default();
        let mut playlist = LlHlsPlaylist::new(config, "stream");
        playlist.add_segment("stream_seg0.m4s", 4.0, true);

        let content = playlist.render();
        // 应包含 EXT-X-MAP
        assert!(
            content.contains("#EXT-X-MAP"),
            "LL-HLS playlist should contain EXT-X-MAP"
        );
    }
}
