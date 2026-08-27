//! lm-thumbnail — 视频截图/缩略图生成
//!
//! 功能：
//! - 从视频帧（YUV420p）生成 JPEG/PNG 缩略图
//! - 从录制文件提取缩略图（通过 ffmpeg）
//! - 定时截图（每隔 N 秒生成一张）
//!
//! 用法：
//! ```ignore
//! // 从 YUV 帧生成缩略图
//! let jpeg = Thumbnail::from_yuv(&yuv_frame, ThumbnailFormat::Jpeg, 80)?;
//!
//! // 从文件提取缩略图
//! let thumb = Thumbnail::from_file("video.mp4", "00:01:00", 320, 240).await?;
//! ```

use anyhow::{anyhow, Result};
use std::path::PathBuf;
use std::process::Command;
use tokio::process::Command as AsyncCommand;

/// 缩略图格式
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ThumbnailFormat {
    Jpeg,
    Png,
}

impl ThumbnailFormat {
    fn extension(&self) -> &str {
        match self {
            ThumbnailFormat::Jpeg => "jpg",
            ThumbnailFormat::Png => "png",
        }
    }

    fn ffmpeg_codec(&self) -> &str {
        match self {
            ThumbnailFormat::Jpeg => "mjpeg",
            ThumbnailFormat::Png => "png",
        }
    }
}

/// 缩略图生成器
pub struct Thumbnail;

impl Thumbnail {
    /// 从 YUV420p 帧生成缩略图（纯 Rust，无依赖）
    ///
    /// 简化实现：将 YUV 转 RGB，然后输出为简化的 JPEG/PNG。
    /// 实际生产环境应使用 image crate 或 ffmpeg。
    pub fn from_yuv(
        yuv: &video_codec::YuvFrame,
        format: ThumbnailFormat,
        quality: u8,
    ) -> Result<Vec<u8>> {
        match format {
            ThumbnailFormat::Png => Self::yuv_to_png(yuv),
            ThumbnailFormat::Jpeg => Self::yuv_to_jpeg(yuv, quality),
        }
    }

    /// 从视频文件提取缩略图（通过 ffmpeg）
    ///
    /// timestamp: "HH:MM:SS" 格式
    /// width/height: 缩略图尺寸（0 = 保持原比例）
    pub async fn from_file(
        file_path: &str,
        timestamp: &str,
        width: u32,
        height: u32,
        format: ThumbnailFormat,
    ) -> Result<Vec<u8>> {
        let scale_filter = if width > 0 && height > 0 {
            format!("scale={width}:{height}")
        } else if width > 0 {
            format!("scale={width}:-1")
        } else {
            "scale=320:-1".to_string()
        };

        let output = AsyncCommand::new("ffmpeg")
            .args([
                "-ss",
                timestamp,
                "-i",
                file_path,
                "-frames:v",
                "1",
                "-vf",
                &scale_filter,
                "-f",
                "image2pipe",
                "-vcodec",
                format.ffmpeg_codec(),
                "pipe:1",
            ])
            .output()
            .await
            .map_err(|e| anyhow!("ffmpeg not found: {e}"))?;

        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr);
            return Err(anyhow!("ffmpeg thumbnail failed: {stderr}"));
        }

        Ok(output.stdout)
    }

    /// 从视频文件提取多张缩略图（定时截图）
    ///
    /// interval_sec: 每隔 N 秒截一张
    /// 返回缩略图文件路径列表
    pub async fn from_file_interval(
        file_path: &str,
        interval_sec: u32,
        output_dir: &str,
        width: u32,
        format: ThumbnailFormat,
    ) -> Result<Vec<String>> {
        let ext = format.extension();
        let pattern = format!("{output_dir}/thumb_%04d.{ext}");

        let scale_filter = if width > 0 {
            format!("scale={width}:-1")
        } else {
            "scale=320:-1".to_string()
        };

        let output = AsyncCommand::new("ffmpeg")
            .args([
                "-i",
                file_path,
                "-vf",
                &format!("fps=1/{interval_sec},{scale_filter}"),
                "-f",
                "image2",
                &pattern,
            ])
            .output()
            .await
            .map_err(|e| anyhow!("ffmpeg not found: {e}"))?;

        if !output.status.success() {
            let stderr = String::from_utf8_lossy(&output.stderr);
            return Err(anyhow!("ffmpeg interval thumbnail failed: {stderr}"));
        }

        // 列出生成的文件
        let mut files = Vec::new();
        let dir = std::fs::read_dir(output_dir).map_err(|e| anyhow!("read output dir: {e}"))?;
        for entry in dir {
            if let Ok(entry) = entry {
                let path = entry.path();
                if let Some(name) = path.file_name().and_then(|n| n.to_str()) {
                    if name.starts_with("thumb_") && name.ends_with(ext) {
                        files.push(path.to_string_lossy().to_string());
                    }
                }
            }
        }
        files.sort();

        Ok(files)
    }

    // ─── 纯 Rust YUV → 图片 ─────────────────────────────────────────

    /// YUV420p → PNG（简化版，使用最小化的 PNG 编码）
    fn yuv_to_png(yuv: &video_codec::YuvFrame) -> Result<Vec<u8>> {
        let width = yuv.width as usize;
        let height = yuv.height as usize;
        let rgb = yuv_to_rgb(yuv, width, height);

        // 最小化 PNG：使用 raw deflate (stored blocks)
        encode_png(&rgb, width, height)
    }

    /// YUV420p → JPEG（Baseline JPEG 编码）
    ///
    /// 实现完整的 Baseline JPEG 编码：
    /// - YUV420p 直接作为 YCbCr 4:2:0（无需色彩转换）
    /// - 8x8 DCT 变换（含 C(u) 归一化）
    /// - 量化（使用标准量化表，按 quality 缩放）
    /// - Zigzag 扫描
    /// - Huffman 编码（标准 DC/AC 表）
    /// - byte stuffing（0xFF → 0xFF 0x00）
    fn yuv_to_jpeg(yuv: &video_codec::YuvFrame, quality: u8) -> Result<Vec<u8>> {
        let width = yuv.width as usize;
        let height = yuv.height as usize;
        debug_assert!(width > 0 && height > 0, "image dimensions must be positive");

        // 4:2:0 sampling (Y 2x2, Cb/Cr 1x1) → MCU = 16x16
        let padded_w = (width + 15) & !15;
        let padded_h = (height + 15) & !15;
        let chroma_w = padded_w / 2;
        let chroma_h = padded_h / 2;

        let mut y_plane = vec![0u8; padded_w * padded_h];
        let mut cb_plane = vec![128u8; chroma_w * chroma_h];
        let mut cr_plane = vec![128u8; chroma_w * chroma_h];

        // Copy Y plane with edge replication padding
        for row in 0..padded_h {
            let src_row = row.min(height - 1);
            for col in 0..padded_w {
                let src_col = col.min(width - 1);
                y_plane[row * padded_w + col] = yuv.y[src_row * width + src_col];
            }
        }

        // Copy Cb/Cr planes (4:2:0 subsampled)
        let orig_chroma_w = width / 2;
        let orig_chroma_h = height / 2;
        if orig_chroma_w > 0 && orig_chroma_h > 0 {
            for row in 0..chroma_h {
                let src_row = row.min(orig_chroma_h - 1);
                for col in 0..chroma_w {
                    let src_col = col.min(orig_chroma_w - 1);
                    cb_plane[row * chroma_w + col] = yuv.u[src_row * orig_chroma_w + src_col];
                    cr_plane[row * chroma_w + col] = yuv.v[src_row * orig_chroma_w + src_col];
                }
            }
        }

        encode_jpeg(
            &y_plane, &cb_plane, &cr_plane, padded_w, padded_h, chroma_w, chroma_h, width, height,
            quality,
        )
    }
}

/// YUV420p → RGB888
fn yuv_to_rgb(yuv: &video_codec::YuvFrame, width: usize, height: usize) -> Vec<u8> {
    let mut rgb = Vec::with_capacity(width * height * 3);

    for y in 0..height {
        for x in 0..width {
            let y_val = yuv.y[y * width + x] as i32;
            let u_val = yuv.u[(y / 2) * (width / 2) + (x / 2)] as i32 - 128;
            let v_val = yuv.v[(y / 2) * (width / 2) + (x / 2)] as i32 - 128;

            let r = (y_val + 1402 * v_val / 1000).clamp(0, 255) as u8;
            let g = (y_val - 344 * u_val / 1000 - 714 * v_val / 1000).clamp(0, 255) as u8;
            let b = (y_val + 1772 * u_val / 1000).clamp(0, 255) as u8;

            rgb.push(r);
            rgb.push(g);
            rgb.push(b);
        }
    }

    rgb
}

/// 最小化 PNG 编码（使用 stored deflate blocks）
fn encode_png(rgb: &[u8], width: usize, height: usize) -> Result<Vec<u8>> {
    let mut png = Vec::new();

    // PNG signature
    png.extend_from_slice(&[137, 80, 78, 71, 13, 10, 26, 10]);

    // IHDR chunk
    let mut ihdr = Vec::new();
    ihdr.extend_from_slice(&(width as u32).to_be_bytes());
    ihdr.extend_from_slice(&(height as u32).to_be_bytes());
    ihdr.push(8); // bit depth
    ihdr.push(2); // color type = RGB
    ihdr.push(0); // compression
    ihdr.push(0); // filter
    ihdr.push(0); // interlace
    write_chunk(&mut png, b"IHDR", &ihdr);

    // IDAT chunk: raw image data with filter bytes + stored deflate
    let mut raw = Vec::with_capacity(height * (1 + width * 3));
    for y in 0..height {
        raw.push(0); // filter type = None
        raw.extend_from_slice(&rgb[y * width * 3..(y + 1) * width * 3]);
    }

    // zlib wrapper: 2 byte header + stored blocks + 4 byte adler32
    let mut idat = Vec::new();
    idat.push(0x78); // zlib header (CM=8, CINFO=7)
    idat.push(0x01); // FLG (no dict, level 0)

    // Stored deflate blocks
    let mut offset = 0;
    while offset < raw.len() {
        let remaining = raw.len() - offset;
        let block_size = std::cmp::min(remaining, 65535);
        let is_last = offset + block_size >= raw.len();

        idat.push(if is_last { 1 } else { 0 }); // BFINAL + BTYPE=00 (stored)
        idat.extend_from_slice(&(block_size as u16).to_le_bytes());
        idat.extend_from_slice(&(!block_size as u16).to_le_bytes());
        idat.extend_from_slice(&raw[offset..offset + block_size]);
        offset += block_size;
    }

    // Adler32
    let adler = adler32(&raw);
    idat.extend_from_slice(&adler.to_be_bytes());

    write_chunk(&mut png, b"IDAT", &idat);

    // IEND chunk
    write_chunk(&mut png, b"IEND", &[]);

    Ok(png)
}

/// 写 PNG chunk
fn write_chunk(png: &mut Vec<u8>, chunk_type: &[u8; 4], data: &[u8]) {
    png.extend_from_slice(&(data.len() as u32).to_be_bytes());
    let start = png.len();
    png.extend_from_slice(chunk_type);
    png.extend_from_slice(data);
    let crc = crc32(&png[start..]);
    png.extend_from_slice(&crc.to_be_bytes());
}

/// CRC32 (PNG polynomial)
fn crc32(data: &[u8]) -> u32 {
    static mut TABLE: [u32; 256] = [0; 256];
    static INIT: std::sync::Once = std::sync::Once::new();

    unsafe {
        INIT.call_once(|| {
            for i in 0..256u32 {
                let mut c = i;
                for _ in 0..8 {
                    if c & 1 != 0 {
                        c = 0xEDB88320 ^ (c >> 1);
                    } else {
                        c >>= 1;
                    }
                }
                TABLE[i as usize] = c;
            }
        });

        let mut crc: u32 = 0xFFFFFFFF;
        for &byte in data {
            crc = TABLE[((crc ^ byte as u32) & 0xFF) as usize] ^ (crc >> 8);
        }
        crc ^ 0xFFFFFFFF
    }
}

/// Adler32 checksum
fn adler32(data: &[u8]) -> u32 {
    let mut a: u32 = 1;
    let mut b: u32 = 0;
    for &byte in data {
        a = (a + byte as u32) % 65521;
        b = (b + a) % 65521;
    }
    (b << 16) | a
}

// ============================================================================
// Baseline JPEG 编码
// ============================================================================

const STD_LUMA_QT: [u8; 64] = [
    16, 11, 10, 16, 24, 40, 51, 61, 12, 12, 14, 19, 26, 58, 60, 55, 14, 13, 16, 24, 40, 57, 69, 56,
    14, 17, 22, 29, 51, 87, 80, 62, 18, 22, 37, 56, 68, 109, 103, 77, 24, 35, 55, 64, 81, 104, 113,
    92, 49, 64, 78, 87, 103, 121, 120, 101, 72, 92, 95, 98, 112, 100, 103, 99,
];

const STD_CHROMA_QT: [u8; 64] = [
    17, 18, 24, 47, 99, 99, 99, 99, 18, 21, 26, 66, 99, 99, 99, 99, 24, 26, 56, 99, 99, 99, 99, 99,
    47, 66, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99,
    99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99, 99,
];

const ZIGZAG: [u8; 64] = [
    0, 1, 8, 16, 9, 2, 3, 10, 17, 24, 32, 25, 18, 11, 4, 5, 12, 19, 26, 33, 40, 48, 41, 34, 27, 20,
    13, 6, 7, 14, 21, 28, 35, 42, 49, 56, 57, 50, 43, 36, 29, 22, 15, 23, 30, 37, 44, 51, 58, 59,
    52, 45, 38, 31, 39, 46, 53, 60, 61, 54, 47, 55, 62, 63,
];

const LUMA_DC_BITS: [u8; 16] = [0, 1, 5, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0];
const LUMA_DC_VALS: [u8; 12] = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11];

const LUMA_AC_BITS: [u8; 16] = [0, 2, 1, 3, 3, 2, 4, 3, 5, 5, 4, 4, 0, 0, 1, 0x7d];
const LUMA_AC_VALS: [u8; 162] = [
    0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12, 0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07,
    0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08, 0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0,
    0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28,
    0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49,
    0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69,
    0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89,
    0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7,
    0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5,
    0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2,
    0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8,
    0xf9, 0xfa,
];

const CHROMA_DC_BITS: [u8; 16] = [0, 3, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0];
const CHROMA_DC_VALS: [u8; 12] = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11];

const CHROMA_AC_BITS: [u8; 16] = [0, 2, 1, 2, 4, 4, 3, 4, 7, 5, 4, 4, 0, 1, 2, 0x77];
const CHROMA_AC_VALS: [u8; 162] = [
    0x00, 0x01, 0x02, 0x03, 0x11, 0x04, 0x05, 0x21, 0x31, 0x06, 0x12, 0x41, 0x51, 0x07, 0x61, 0x71,
    0x13, 0x22, 0x32, 0x81, 0x08, 0x14, 0x42, 0x91, 0xa1, 0xb1, 0xc1, 0x09, 0x23, 0x33, 0x52, 0xf0,
    0x15, 0x62, 0x72, 0xd1, 0x0a, 0x16, 0x24, 0x34, 0xe1, 0x25, 0xf1, 0x17, 0x18, 0x19, 0x1a, 0x26,
    0x27, 0x28, 0x29, 0x2a, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48,
    0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68,
    0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7a, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87,
    0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5,
    0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3,
    0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda,
    0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8,
    0xf9, 0xfa,
];

struct HuffTable {
    codes: Vec<(u16, u8)>,
}

impl HuffTable {
    fn build(bits: &[u8; 16], vals: &[u8]) -> Self {
        let mut codes = vec![(0u16, 0u8); 256];
        let mut code: u32 = 0;
        let mut idx = 0;
        for len in 0..16 {
            for _ in 0..bits[len] {
                if idx < vals.len() {
                    let sym = vals[idx] as usize;
                    codes[sym] = (code as u16, (len + 1) as u8);
                    code += 1;
                }
                idx += 1;
            }
            code <<= 1;
        }
        HuffTable { codes }
    }

    #[inline]
    fn encode(&self, sym: u8) -> (u16, u8) {
        self.codes[sym as usize]
    }
}

struct BitWriter {
    data: Vec<u8>,
    current_byte: u8,
    bit_pos: u8,
}

impl BitWriter {
    fn new() -> Self {
        Self {
            data: Vec::new(),
            current_byte: 0,
            bit_pos: 0,
        }
    }

    fn write_bits(&mut self, value: u32, n_bits: u8) {
        debug_assert!(n_bits <= 32, "n_bits exceeds 32");
        for i in (0..n_bits).rev() {
            let bit = ((value >> i) & 1) as u8;
            self.current_byte = (self.current_byte << 1) | bit;
            self.bit_pos += 1;
            if self.bit_pos == 8 {
                self.push_byte(self.current_byte);
                self.current_byte = 0;
                self.bit_pos = 0;
            }
        }
    }

    fn push_byte(&mut self, b: u8) {
        self.data.push(b);
        if b == 0xFF {
            self.data.push(0x00);
        }
    }

    fn flush(&mut self) {
        if self.bit_pos > 0 {
            self.current_byte <<= 8 - self.bit_pos;
            self.current_byte |= (1 << (8 - self.bit_pos)) - 1;
            self.push_byte(self.current_byte);
            self.current_byte = 0;
            self.bit_pos = 0;
        }
    }
}

fn dct_8x8(block: &mut [i32; 64]) {
    let mut tmp = [0.0f64; 64];

    for row in 0..8 {
        for k in 0..8 {
            let mut sum = 0.0;
            for n in 0..8 {
                let cos = ((2 * n + 1) as f64 * k as f64 * std::f64::consts::PI / 16.0).cos();
                sum += block[row * 8 + n] as f64 * cos;
            }
            let ck = if k == 0 { 1.0 / 2f64.sqrt() } else { 1.0 };
            tmp[row * 8 + k] = 0.5 * ck * sum;
        }
    }

    for col in 0..8 {
        for k in 0..8 {
            let mut sum = 0.0;
            for n in 0..8 {
                let cos = ((2 * n + 1) as f64 * k as f64 * std::f64::consts::PI / 16.0).cos();
                sum += tmp[n * 8 + col] * cos;
            }
            let ck = if k == 0 { 1.0 / 2f64.sqrt() } else { 1.0 };
            block[k * 8 + col] = (0.5 * ck * sum).round() as i32;
        }
    }
}

fn make_qt(std_qt: &[u8; 64], quality: u8) -> [u16; 64] {
    let q = quality.clamp(1, 100) as i32;
    let scale = if q < 50 { 5000 / q } else { 200 - 2 * q };

    let mut qt = [0u16; 64];
    for i in 0..64 {
        let val = (std_qt[i] as i32 * scale + 50) / 100;
        qt[i] = val.clamp(1, 255) as u16;
    }
    qt
}

fn encode_block(
    writer: &mut BitWriter,
    block: &[u8; 64],
    qt: &[u16; 64],
    dc_table: &HuffTable,
    ac_table: &HuffTable,
    prev_dc: &mut i32,
) {
    let mut dct_block = [0i32; 64];
    for i in 0..64 {
        dct_block[i] = block[i] as i32 - 128;
    }
    dct_8x8(&mut dct_block);

    let mut quantized = [0i32; 64];
    for i in 0..64 {
        let coeff = dct_block[i];
        let q = qt[i] as i32;
        quantized[i] = (coeff + q / 2) / q;
    }

    let mut zz = [0i32; 64];
    for i in 0..64 {
        zz[i] = quantized[ZIGZAG[i] as usize];
    }

    let dc_val = zz[0];
    let dc_diff = dc_val - *prev_dc;
    *prev_dc = dc_val;

    let (dc_cat, dc_bits, dc_nbits) = encode_vlc(dc_diff);
    let (code, len) = dc_table.encode(dc_cat);
    writer.write_bits(code as u32, len);
    if dc_nbits > 0 {
        writer.write_bits(dc_bits, dc_nbits);
    }

    let mut run = 0;
    for i in 1..64 {
        if zz[i] == 0 {
            run += 1;
        } else {
            while run > 15 {
                let (code, len) = ac_table.encode(0xF0);
                writer.write_bits(code as u32, len);
                run -= 16;
            }
            let (ac_cat, ac_bits, ac_nbits) = encode_vlc(zz[i]);
            let sym = ((run << 4) | ac_cat as usize) as u8;
            let (code, len) = ac_table.encode(sym);
            writer.write_bits(code as u32, len);
            if ac_nbits > 0 {
                writer.write_bits(ac_bits, ac_nbits);
            }
            run = 0;
        }
    }
    if run > 0 {
        let (code, len) = ac_table.encode(0x00);
        writer.write_bits(code as u32, len);
    }
}

fn encode_vlc(value: i32) -> (u8, u32, u8) {
    if value == 0 {
        return (0, 0, 0);
    }
    let abs_val = value.unsigned_abs();
    let mag = 32 - abs_val.leading_zeros() as u8;
    let bits = if value < 0 {
        (value - 1 + (1 << mag)) as u32 & ((1u32 << mag) - 1)
    } else {
        value as u32
    };
    (mag, bits, mag)
}

fn encode_jpeg(
    y_plane: &[u8],
    cb_plane: &[u8],
    cr_plane: &[u8],
    padded_w: usize,
    padded_h: usize,
    chroma_w: usize,
    chroma_h: usize,
    orig_w: usize,
    orig_h: usize,
    quality: u8,
) -> Result<Vec<u8>> {
    debug_assert!(
        padded_w % 16 == 0 && padded_h % 16 == 0,
        "padded dims must be 16-aligned"
    );
    debug_assert!(
        chroma_w == padded_w / 2 && chroma_h == padded_h / 2,
        "chroma dims mismatch"
    );

    let mut jpeg = Vec::new();

    jpeg.extend_from_slice(&[0xFF, 0xD8]);

    write_dqt(&mut jpeg, 0, &make_qt(&STD_LUMA_QT, quality));
    write_dqt(&mut jpeg, 1, &make_qt(&STD_CHROMA_QT, quality));

    write_sof0(&mut jpeg, orig_w as u16, orig_h as u16);

    write_dht(&mut jpeg, 0, 0, &LUMA_DC_BITS, &LUMA_DC_VALS);
    write_dht(&mut jpeg, 0, 1, &LUMA_AC_BITS, &LUMA_AC_VALS);
    write_dht(&mut jpeg, 1, 0, &CHROMA_DC_BITS, &CHROMA_DC_VALS);
    write_dht(&mut jpeg, 1, 1, &CHROMA_AC_BITS, &CHROMA_AC_VALS);

    write_sos(&mut jpeg);

    let luma_dc = HuffTable::build(&LUMA_DC_BITS, &LUMA_DC_VALS);
    let luma_ac = HuffTable::build(&LUMA_AC_BITS, &LUMA_AC_VALS);
    let chroma_dc = HuffTable::build(&CHROMA_DC_BITS, &CHROMA_DC_VALS);
    let chroma_ac = HuffTable::build(&CHROMA_AC_BITS, &CHROMA_AC_VALS);

    let mut writer = BitWriter::new();
    let mut prev_dc_y = 0i32;
    let mut prev_dc_cb = 0i32;
    let mut prev_dc_cr = 0i32;

    let luma_qt = make_qt(&STD_LUMA_QT, quality);
    let chroma_qt = make_qt(&STD_CHROMA_QT, quality);

    let mcu_cols = padded_w / 16;
    let mcu_rows = padded_h / 16;
    let total_mcus = mcu_cols * mcu_rows;
    let mut mcu_index = 0usize;

    for mcu_y in (0..padded_h).step_by(16) {
        for mcu_x in (0..padded_w).step_by(16) {
            mcu_index += 1;
            debug_assert!(mcu_index <= total_mcus, "MCU count overflow");

            for by in 0..2 {
                for bx in 0..2 {
                    let mut block = [0u8; 64];
                    let base_x = mcu_x + bx * 8;
                    let base_y = mcu_y + by * 8;
                    for row in 0..8 {
                        for col in 0..8 {
                            block[row * 8 + col] =
                                y_plane[(base_y + row) * padded_w + (base_x + col)];
                        }
                    }
                    encode_block(
                        &mut writer,
                        &block,
                        &luma_qt,
                        &luma_dc,
                        &luma_ac,
                        &mut prev_dc_y,
                    );
                }
            }

            let cb_base_x = mcu_x / 2;
            let cb_base_y = mcu_y / 2;
            let mut block = [0u8; 64];
            for row in 0..8 {
                for col in 0..8 {
                    block[row * 8 + col] =
                        cb_plane[(cb_base_y + row) * chroma_w + (cb_base_x + col)];
                }
            }
            encode_block(
                &mut writer,
                &block,
                &chroma_qt,
                &chroma_dc,
                &chroma_ac,
                &mut prev_dc_cb,
            );

            let mut block = [0u8; 64];
            for row in 0..8 {
                for col in 0..8 {
                    block[row * 8 + col] =
                        cr_plane[(cb_base_y + row) * chroma_w + (cb_base_x + col)];
                }
            }
            encode_block(
                &mut writer,
                &block,
                &chroma_qt,
                &chroma_dc,
                &chroma_ac,
                &mut prev_dc_cr,
            );
        }
    }

    writer.flush();
    jpeg.extend_from_slice(&writer.data);

    jpeg.extend_from_slice(&[0xFF, 0xD9]);

    Ok(jpeg)
}

fn write_dqt(jpeg: &mut Vec<u8>, table_id: u8, qt: &[u16; 64]) {
    jpeg.extend_from_slice(&[0xFF, 0xDB]);
    jpeg.extend_from_slice(&67u16.to_be_bytes());
    jpeg.push(table_id);
    for &val in qt {
        jpeg.push(val as u8);
    }
}

fn write_sof0(jpeg: &mut Vec<u8>, width: u16, height: u16) {
    jpeg.extend_from_slice(&[0xFF, 0xC0]);
    jpeg.extend_from_slice(&17u16.to_be_bytes());
    jpeg.push(8);
    jpeg.extend_from_slice(&height.to_be_bytes());
    jpeg.extend_from_slice(&width.to_be_bytes());
    jpeg.push(3);
    jpeg.extend_from_slice(&[1, 0x22, 0]);
    jpeg.extend_from_slice(&[2, 0x11, 1]);
    jpeg.extend_from_slice(&[3, 0x11, 1]);
}

fn write_dht(jpeg: &mut Vec<u8>, table_id: u8, class: u8, bits: &[u8; 16], vals: &[u8]) {
    let length = 2 + 1 + 16 + vals.len() as u16;
    jpeg.extend_from_slice(&[0xFF, 0xC4]);
    jpeg.extend_from_slice(&length.to_be_bytes());
    jpeg.push((class << 4) | table_id);
    jpeg.extend_from_slice(bits);
    jpeg.extend_from_slice(vals);
}

fn write_sos(jpeg: &mut Vec<u8>) {
    jpeg.extend_from_slice(&[0xFF, 0xDA]);
    jpeg.extend_from_slice(&12u16.to_be_bytes());
    jpeg.push(3);
    jpeg.extend_from_slice(&[1, 0x00]);
    jpeg.extend_from_slice(&[2, 0x11]);
    jpeg.extend_from_slice(&[3, 0x11]);
    jpeg.extend_from_slice(&[0, 63, 0]);
}

/// 简化 BMP 编码（fallback for JPEG）
fn encode_bmp(rgb: &[u8], width: usize, height: usize) -> Result<Vec<u8>> {
    let row_size = (width * 3 + 3) & !3; // 4-byte aligned
    let image_size = row_size * height;
    let file_size = 54 + image_size;

    let mut bmp = Vec::with_capacity(file_size);

    // BMP header
    bmp.extend_from_slice(b"BM");
    bmp.extend_from_slice(&(file_size as u32).to_le_bytes());
    bmp.extend_from_slice(&0u32.to_le_bytes()); // reserved
    bmp.extend_from_slice(&54u32.to_le_bytes()); // data offset

    // DIB header
    bmp.extend_from_slice(&40u32.to_le_bytes()); // header size
    bmp.extend_from_slice(&(width as i32).to_le_bytes());
    bmp.extend_from_slice(&(height as i32).to_le_bytes()); // bottom-up
    bmp.extend_from_slice(&1u16.to_le_bytes()); // planes
    bmp.extend_from_slice(&24u16.to_le_bytes()); // bpp
    bmp.extend_from_slice(&0u32.to_le_bytes()); // compression
    bmp.extend_from_slice(&(image_size as u32).to_le_bytes());
    bmp.extend_from_slice(&2835u32.to_be_bytes()); // x ppm
    bmp.extend_from_slice(&2835u32.to_be_bytes()); // y ppm
    bmp.extend_from_slice(&0u32.to_le_bytes()); // colors
    bmp.extend_from_slice(&0u32.to_le_bytes()); // important colors

    // Pixel data (bottom-up, BGR)
    for y in (0..height).rev() {
        let row = &rgb[y * width * 3..(y + 1) * width * 3];
        for x in 0..width {
            bmp.push(row[x * 3 + 2]); // B
            bmp.push(row[x * 3 + 1]); // G
            bmp.push(row[x * 3]); // R
        }
        // padding
        let padding = row_size - width * 3;
        for _ in 0..padding {
            bmp.push(0);
        }
    }

    Ok(bmp)
}

// ============================================================================
// JPEG 结构解析（测试用）
// ============================================================================

#[derive(Debug, Clone)]
pub struct JpegStructure {
    pub has_soi: bool,
    pub has_eoi: bool,
    pub dqt_tables: Vec<(u8, Vec<u8>)>,
    pub sof0: Option<Sof0Info>,
    pub dht_tables: Vec<DhtInfo>,
    pub has_sos: bool,
    pub entropy_data_size: usize,
}

#[derive(Debug, Clone)]
pub struct Sof0Info {
    pub precision: u8,
    pub width: u16,
    pub height: u16,
    pub components: Vec<Sof0Component>,
}

#[derive(Debug, Clone)]
pub struct Sof0Component {
    pub id: u8,
    pub h_sampling: u8,
    pub v_sampling: u8,
    pub qt_id: u8,
}

#[derive(Debug, Clone)]
pub struct DhtInfo {
    pub table_class: u8,
    pub table_id: u8,
    pub bits: [u8; 16],
    pub vals: Vec<u8>,
}

pub fn parse_jpeg_structure(data: &[u8]) -> Result<JpegStructure> {
    if data.len() < 4 {
        return Err(anyhow!("data too short"));
    }

    let mut pos = 0;
    let mut structure = JpegStructure {
        has_soi: false,
        has_eoi: false,
        dqt_tables: Vec::new(),
        sof0: None,
        dht_tables: Vec::new(),
        has_sos: false,
        entropy_data_size: 0,
    };

    if data[0] == 0xFF && data[1] == 0xD8 {
        structure.has_soi = true;
        pos = 2;
    } else {
        return Err(anyhow!("missing SOI marker"));
    }

    while pos < data.len() {
        if data[pos] != 0xFF {
            pos += 1;
            continue;
        }

        // Skip fill bytes (0xFF 0xFF ...)
        while pos < data.len() && data[pos] == 0xFF {
            pos += 1;
        }
        if pos >= data.len() {
            break;
        }

        let marker = data[pos];
        pos += 1;

        match marker {
            0xD9 => {
                structure.has_eoi = true;
                break;
            }
            0xD8 => {
                continue;
            }
            0xDB => {
                let len = u16::from_be_bytes([data[pos], data[pos + 1]]) as usize;
                let mut offset = pos + 2;
                while offset < pos + len {
                    let table_info = data[offset];
                    let table_id = table_info & 0x0F;
                    let precision = (table_info >> 4) & 0x0F;
                    let table_size = if precision == 0 { 64 } else { 128 };
                    let table_data = data[offset + 1..offset + 1 + table_size].to_vec();
                    structure.dqt_tables.push((table_id, table_data));
                    offset += 1 + table_size;
                }
                pos += len;
            }
            0xC0 => {
                let len = u16::from_be_bytes([data[pos], data[pos + 1]]) as usize;
                let precision = data[pos + 2];
                let height = u16::from_be_bytes([data[pos + 3], data[pos + 4]]);
                let width = u16::from_be_bytes([data[pos + 5], data[pos + 6]]);
                let num_components = data[pos + 7];
                let mut components = Vec::new();
                for c in 0..num_components as usize {
                    let base = pos + 8 + c * 3;
                    components.push(Sof0Component {
                        id: data[base],
                        h_sampling: (data[base + 1] >> 4) & 0x0F,
                        v_sampling: data[base + 1] & 0x0F,
                        qt_id: data[base + 2],
                    });
                }
                structure.sof0 = Some(Sof0Info {
                    precision,
                    width,
                    height,
                    components,
                });
                pos += len;
            }
            0xC4 => {
                let len = u16::from_be_bytes([data[pos], data[pos + 1]]) as usize;
                let mut offset = pos + 2;
                while offset < pos + len {
                    let table_info = data[offset];
                    let table_class = (table_info >> 4) & 0x0F;
                    let table_id = table_info & 0x0F;
                    let mut bits = [0u8; 16];
                    bits.copy_from_slice(&data[offset + 1..offset + 17]);
                    let vals_len: usize = bits.iter().map(|&b| b as usize).sum();
                    let vals = data[offset + 17..offset + 17 + vals_len].to_vec();
                    structure.dht_tables.push(DhtInfo {
                        table_class,
                        table_id,
                        bits,
                        vals,
                    });
                    offset += 17 + vals_len;
                }
                pos += len;
            }
            0xDA => {
                let len = u16::from_be_bytes([data[pos], data[pos + 1]]) as usize;
                structure.has_sos = true;
                pos += len;
                let entropy_start = pos;
                while pos < data.len() - 1 {
                    if data[pos] == 0xFF && data[pos + 1] != 0x00 && data[pos + 1] != 0xFF {
                        break;
                    }
                    pos += 1;
                }
                structure.entropy_data_size = pos - entropy_start;
            }
            0x00 => {
                continue;
            }
            _ => {
                if pos + 1 < data.len() {
                    let len = u16::from_be_bytes([data[pos], data[pos + 1]]) as usize;
                    pos += len;
                } else {
                    break;
                }
            }
        }
    }

    Ok(structure)
}

// ============================================================================
// 最小 JPEG 解码器（测试用 — 验证编码正确性）
// ============================================================================

pub struct JpegDecoder<'a> {
    data: &'a [u8],
    pos: usize,
    qt_tables: [Option<[u16; 64]>; 4],
    huff_dc: [Option<HuffTable>; 4],
    huff_ac: [Option<HuffTable>; 4],
    width: u16,
    height: u16,
    components: Vec<DecComponent>,
}

#[derive(Clone)]
struct DecComponent {
    id: u8,
    h_sampling: u8,
    v_sampling: u8,
    qt_id: u8,
    dc_table_id: u8,
    ac_table_id: u8,
}

struct BitReader<'a> {
    data: &'a [u8],
    pos: usize,
    current_byte: u8,
    bits_left: u8,
}

impl<'a> BitReader<'a> {
    fn new(data: &'a [u8]) -> Self {
        Self {
            data,
            pos: 0,
            current_byte: 0,
            bits_left: 0,
        }
    }

    fn read_bit(&mut self) -> Option<u8> {
        if self.bits_left == 0 {
            if self.pos >= self.data.len() {
                return None;
            }
            self.current_byte = self.data[self.pos];
            self.pos += 1;
            if self.current_byte == 0xFF {
                if self.pos < self.data.len() && self.data[self.pos] == 0x00 {
                    self.pos += 1;
                } else {
                    return None;
                }
            }
            self.bits_left = 8;
        }
        self.bits_left -= 1;
        Some((self.current_byte >> self.bits_left) & 1)
    }

    fn read_bits(&mut self, n: u8) -> Option<u32> {
        let mut result = 0u32;
        for _ in 0..n {
            let bit = self.read_bit()?;
            result = (result << 1) | bit as u32;
        }
        Some(result)
    }

    fn decode_huffman(&mut self, table: &HuffTable) -> Option<u8> {
        let mut code: u32 = 0;
        for len in 1..=16u8 {
            code = (code << 1) | self.read_bit()? as u32;
            for (sym, &(s_code, s_len)) in table.codes.iter().enumerate() {
                if s_len == len && s_code as u32 == code {
                    return Some(sym as u8);
                }
            }
        }
        None
    }
}

impl<'a> JpegDecoder<'a> {
    pub fn new(data: &'a [u8]) -> Self {
        Self {
            data,
            pos: 0,
            qt_tables: Default::default(),
            huff_dc: Default::default(),
            huff_ac: Default::default(),
            width: 0,
            height: 0,
            components: Vec::new(),
        }
    }

    pub fn decode(&mut self) -> Result<(u16, u16, Vec<u8>, Vec<u8>, Vec<u8>)> {
        if self.data.len() < 4 || self.data[0] != 0xFF || self.data[1] != 0xD8 {
            return Err(anyhow!("missing SOI"));
        }
        self.pos = 2;

        while self.pos < self.data.len() {
            if self.data[self.pos] != 0xFF {
                self.pos += 1;
                continue;
            }
            while self.pos < self.data.len() && self.data[self.pos] == 0xFF {
                self.pos += 1;
            }
            if self.pos >= self.data.len() {
                break;
            }
            let marker = self.data[self.pos];
            self.pos += 1;

            match marker {
                0xD9 => break,
                0xD8 => continue,
                0xDB => self.parse_dqt()?,
                0xC0 => self.parse_sof0()?,
                0xC4 => self.parse_dht()?,
                0xDA => {
                    self.parse_sos()?;
                    let (w, h, y, cb, cr) = self.decode_entropy()?;
                    return Ok((w, h, y, cb, cr));
                }
                _ => {
                    let len =
                        u16::from_be_bytes([self.data[self.pos], self.data[self.pos + 1]]) as usize;
                    self.pos += len;
                }
            }
        }
        Err(anyhow!("no SOS found"))
    }

    fn parse_dqt(&mut self) -> Result<()> {
        let len = u16::from_be_bytes([self.data[self.pos], self.data[self.pos + 1]]) as usize;
        let mut offset = self.pos + 2;
        while offset < self.pos + len {
            let info = self.data[offset];
            let precision = (info >> 4) & 0x0F;
            let table_id = (info & 0x0F) as usize;
            offset += 1;
            let mut qt = [0u16; 64];
            if precision == 0 {
                for i in 0..64 {
                    qt[i] = self.data[offset + i] as u16;
                }
                offset += 64;
            } else {
                for i in 0..64 {
                    qt[i] = u16::from_be_bytes([
                        self.data[offset + i * 2],
                        self.data[offset + i * 2 + 1],
                    ]);
                }
                offset += 128;
            }
            self.qt_tables[table_id] = Some(qt);
        }
        self.pos += len;
        Ok(())
    }

    fn parse_sof0(&mut self) -> Result<()> {
        let len = u16::from_be_bytes([self.data[self.pos], self.data[self.pos + 1]]) as usize;
        self.pos += 2;
        let _precision = self.data[self.pos];
        self.pos += 1;
        self.height = u16::from_be_bytes([self.data[self.pos], self.data[self.pos + 1]]);
        self.pos += 2;
        self.width = u16::from_be_bytes([self.data[self.pos], self.data[self.pos + 1]]);
        self.pos += 2;
        let num_components = self.data[self.pos];
        self.pos += 1;
        for _ in 0..num_components {
            let id = self.data[self.pos];
            let sampling = self.data[self.pos + 1];
            let qt_id = self.data[self.pos + 2];
            self.pos += 3;
            self.components.push(DecComponent {
                id,
                h_sampling: (sampling >> 4) & 0x0F,
                v_sampling: sampling & 0x0F,
                qt_id,
                dc_table_id: 0,
                ac_table_id: 0,
            });
        }
        let _ = len;
        Ok(())
    }

    fn parse_dht(&mut self) -> Result<()> {
        let len = u16::from_be_bytes([self.data[self.pos], self.data[self.pos + 1]]) as usize;
        let mut offset = self.pos + 2;
        while offset < self.pos + len {
            let info = self.data[offset];
            let table_class = (info >> 4) & 0x0F;
            let table_id = (info & 0x0F) as usize;
            offset += 1;
            let mut bits = [0u8; 16];
            bits.copy_from_slice(&self.data[offset..offset + 16]);
            offset += 16;
            let vals_len: usize = bits.iter().map(|&b| b as usize).sum();
            let vals = &self.data[offset..offset + vals_len];
            offset += vals_len;
            let table = HuffTable::build(&bits, vals);
            if table_class == 0 {
                self.huff_dc[table_id] = Some(table);
            } else {
                self.huff_ac[table_id] = Some(table);
            }
        }
        self.pos += len;
        Ok(())
    }

    fn parse_sos(&mut self) -> Result<()> {
        let _len = u16::from_be_bytes([self.data[self.pos], self.data[self.pos + 1]]);
        self.pos += 2;
        let num_components = self.data[self.pos];
        self.pos += 1;
        for i in 0..num_components as usize {
            let comp_id = self.data[self.pos];
            let table_ids = self.data[self.pos + 1];
            self.pos += 2;
            let dc_id = (table_ids >> 4) & 0x0F;
            let ac_id = table_ids & 0x0F;
            for comp in &mut self.components {
                if comp.id == comp_id {
                    comp.dc_table_id = dc_id;
                    comp.ac_table_id = ac_id;
                }
            }
            let _ = i;
        }
        self.pos += 3; // Ss, Se, AhAl
        Ok(())
    }

    fn decode_entropy(&mut self) -> Result<(u16, u16, Vec<u8>, Vec<u8>, Vec<u8>)> {
        let entropy_start = self.pos;
        let mut entropy_end = self.data.len();
        let mut i = self.pos;
        while i < self.data.len() - 1 {
            if self.data[i] == 0xFF && self.data[i + 1] != 0x00 && self.data[i + 1] != 0xFF {
                entropy_end = i;
                break;
            }
            i += 1;
        }
        let entropy_data = &self.data[entropy_start..entropy_end];
        let mut reader = BitReader::new(entropy_data);

        let padded_w = ((self.width as usize + 15) / 16) * 16;
        let padded_h = ((self.height as usize + 15) / 16) * 16;
        let chroma_w = padded_w / 2;
        let chroma_h = padded_h / 2;

        let mut y_plane = vec![0u8; padded_w * padded_h];
        let mut cb_plane = vec![128u8; chroma_w * chroma_h];
        let mut cr_plane = vec![128u8; chroma_w * chroma_h];

        let mut prev_dc = [0i32; 3];

        for mcu_y in (0..padded_h).step_by(16) {
            for mcu_x in (0..padded_w).step_by(16) {
                for by in 0..2 {
                    for bx in 0..2 {
                        let block = decode_block(
                            &mut reader,
                            self.qt_tables[0].as_ref().unwrap(),
                            self.huff_dc[0].as_ref().unwrap(),
                            self.huff_ac[0].as_ref().unwrap(),
                            &mut prev_dc[0],
                        )?;
                        for row in 0..8 {
                            for col in 0..8 {
                                y_plane
                                    [(mcu_y + by * 8 + row) * padded_w + (mcu_x + bx * 8 + col)] =
                                    block[row * 8 + col];
                            }
                        }
                    }
                }
                let cb_block = decode_block(
                    &mut reader,
                    self.qt_tables[1].as_ref().unwrap(),
                    self.huff_dc[1].as_ref().unwrap(),
                    self.huff_ac[1].as_ref().unwrap(),
                    &mut prev_dc[1],
                )?;
                let cr_block = decode_block(
                    &mut reader,
                    self.qt_tables[1].as_ref().unwrap(),
                    self.huff_dc[1].as_ref().unwrap(),
                    self.huff_ac[1].as_ref().unwrap(),
                    &mut prev_dc[2],
                )?;
                let cb_base_x = mcu_x / 2;
                let cb_base_y = mcu_y / 2;
                for row in 0..8 {
                    for col in 0..8 {
                        cb_plane[(cb_base_y + row) * chroma_w + (cb_base_x + col)] =
                            cb_block[row * 8 + col];
                        cr_plane[(cb_base_y + row) * chroma_w + (cb_base_x + col)] =
                            cr_block[row * 8 + col];
                    }
                }
            }
        }

        Ok((self.width, self.height, y_plane, cb_plane, cr_plane))
    }
}

fn decode_block(
    reader: &mut BitReader,
    qt: &[u16; 64],
    dc_table: &HuffTable,
    ac_table: &HuffTable,
    prev_dc: &mut i32,
) -> Result<[u8; 64]> {
    let mut zz = [0i32; 64];

    let dc_cat = reader
        .decode_huffman(dc_table)
        .ok_or(anyhow!("DC decode failed"))?;
    let dc_diff = if dc_cat == 0 {
        0
    } else {
        let bits = reader
            .read_bits(dc_cat)
            .ok_or(anyhow!("DC bits read failed"))?;
        decode_vlc_value(bits, dc_cat)
    };
    zz[0] = *prev_dc + dc_diff;
    *prev_dc = zz[0];

    let mut i = 1;
    while i < 64 {
        let sym = reader
            .decode_huffman(ac_table)
            .ok_or(anyhow!("AC decode failed"))?;
        if sym == 0x00 {
            break;
        }
        let run = (sym >> 4) as usize;
        let cat = (sym & 0x0F) as u8;
        if cat == 0 && run == 15 {
            i += 16;
            continue;
        }
        i += run;
        if i >= 64 {
            break;
        }
        let bits = reader
            .read_bits(cat)
            .ok_or(anyhow!("AC bits read failed"))?;
        zz[i] = decode_vlc_value(bits, cat);
        i += 1;
    }

    let mut quantized = [0i32; 64];
    for j in 0..64 {
        quantized[ZIGZAG[j] as usize] = zz[j];
    }

    let mut dct_block = [0.0f64; 64];
    for j in 0..64 {
        dct_block[j] = quantized[j] as f64 * qt[j] as f64;
    }

    let mut block = [0u8; 64];
    idct_8x8(&dct_block, &mut block);
    Ok(block)
}

fn decode_vlc_value(bits: u32, cat: u8) -> i32 {
    if cat == 0 {
        return 0;
    }
    let max_val = 1i32 << cat;
    if bits < (max_val as u32 >> 1) {
        bits as i32 - max_val + 1
    } else {
        bits as i32
    }
}

fn idct_8x8(dct: &[f64; 64], out: &mut [u8; 64]) {
    let mut tmp = [0.0f64; 64];

    for row in 0..8 {
        for n in 0..8 {
            let mut sum = 0.0;
            for k in 0..8 {
                let ck = if k == 0 { 1.0 / 2f64.sqrt() } else { 1.0 };
                let cos = ((2 * n + 1) as f64 * k as f64 * std::f64::consts::PI / 16.0).cos();
                sum += ck * dct[row * 8 + k] * cos;
            }
            tmp[row * 8 + n] = 0.5 * sum;
        }
    }

    for col in 0..8 {
        for n in 0..8 {
            let mut sum = 0.0;
            for k in 0..8 {
                let ck = if k == 0 { 1.0 / 2f64.sqrt() } else { 1.0 };
                let cos = ((2 * n + 1) as f64 * k as f64 * std::f64::consts::PI / 16.0).cos();
                sum += ck * tmp[k * 8 + col] * cos;
            }
            let val = (0.5 * sum + 128.0).round() as i32;
            out[n * 8 + col] = val.clamp(0, 255) as u8;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // ─── 基础编码测试 ──────────────────────────────────────────────

    #[test]
    fn test_yuv_to_rgb() {
        let yuv = video_codec::YuvFrame::black(4, 4, 0);
        let rgb = yuv_to_rgb(&yuv, 4, 4);
        assert_eq!(rgb.len(), 4 * 4 * 3);
        assert_eq!(rgb[0], 0);
        assert_eq!(rgb[1], 0);
        assert_eq!(rgb[2], 0);
    }

    #[test]
    fn test_encode_png() {
        let rgb = vec![255, 0, 0, 0, 255, 0, 0, 0, 255, 255, 255, 255];
        let png = encode_png(&rgb, 2, 2).unwrap();
        assert_eq!(&png[0..8], &[137, 80, 78, 71, 13, 10, 26, 10]);
    }

    #[test]
    fn test_encode_bmp() {
        let rgb = vec![255, 0, 0];
        let bmp = encode_bmp(&rgb, 1, 1).unwrap();
        assert_eq!(&bmp[0..2], b"BM");
    }

    #[test]
    fn test_thumbnail_from_yuv_png() {
        let yuv = video_codec::YuvFrame::black(8, 8, 0);
        let png = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Png, 80).unwrap();
        assert!(!png.is_empty());
        assert_eq!(&png[0..8], &[137, 80, 78, 71, 13, 10, 26, 10]);
    }

    #[test]
    fn test_adler32() {
        assert_eq!(adler32(b"hello"), 0x062c0215);
    }

    #[test]
    fn test_crc32() {
        assert_eq!(crc32(b"IEND"), 0xAE426082);
    }

    // ─── JPEG marker 结构测试 ──────────────────────────────────────

    #[test]
    fn test_jpeg_soi_eoi_markers() {
        let yuv = video_codec::YuvFrame::black(16, 16, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        assert!(&jpeg[0..2] == &[0xFF, 0xD8], "SOI marker missing");
        assert!(
            &jpeg[jpeg.len() - 2..] == &[0xFF, 0xD9],
            "EOI marker missing"
        );
    }

    #[test]
    fn test_jpeg_structure_markers_present() {
        let yuv = video_codec::YuvFrame::black(32, 32, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 75).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();

        assert!(structure.has_soi, "SOI must be present");
        assert!(structure.has_eoi, "EOI must be present");
        assert!(structure.has_sos, "SOS must be present");
        assert!(structure.sof0.is_some(), "SOF0 must be present");
        assert_eq!(
            structure.dqt_tables.len(),
            2,
            "must have 2 DQT tables (luma + chroma)"
        );
        assert_eq!(
            structure.dht_tables.len(),
            4,
            "must have 4 DHT tables (DC/AC × luma/chroma)"
        );
        assert!(
            structure.entropy_data_size > 0,
            "must have entropy-coded data"
        );
    }

    #[test]
    fn test_jpeg_sof0_dimensions() {
        let yuv = video_codec::YuvFrame::black(48, 32, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();
        let sof0 = structure.sof0.unwrap();

        assert_eq!(sof0.precision, 8, "precision must be 8-bit");
        assert_eq!(sof0.width, 48, "width must match");
        assert_eq!(sof0.height, 32, "height must match");
        assert_eq!(sof0.components.len(), 3, "must have 3 components (YCbCr)");

        assert_eq!(sof0.components[0].id, 1, "Y component id must be 1");
        assert_eq!(
            sof0.components[0].h_sampling, 2,
            "Y horizontal sampling must be 2"
        );
        assert_eq!(
            sof0.components[0].v_sampling, 2,
            "Y vertical sampling must be 2"
        );
        assert_eq!(
            sof0.components[0].qt_id, 0,
            "Y quantization table must be 0"
        );

        assert_eq!(sof0.components[1].id, 2, "Cb component id must be 2");
        assert_eq!(
            sof0.components[1].h_sampling, 1,
            "Cb horizontal sampling must be 1"
        );
        assert_eq!(
            sof0.components[1].v_sampling, 1,
            "Cb vertical sampling must be 1"
        );
        assert_eq!(
            sof0.components[1].qt_id, 1,
            "Cb quantization table must be 1"
        );

        assert_eq!(sof0.components[2].id, 3, "Cr component id must be 3");
        assert_eq!(
            sof0.components[2].h_sampling, 1,
            "Cr horizontal sampling must be 1"
        );
        assert_eq!(
            sof0.components[2].v_sampling, 1,
            "Cr vertical sampling must be 1"
        );
        assert_eq!(
            sof0.components[2].qt_id, 1,
            "Cr quantization table must be 1"
        );
    }

    #[test]
    fn test_jpeg_dqt_tables() {
        let yuv = video_codec::YuvFrame::black(16, 16, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 50).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();

        let (luma_id, luma_qt) = &structure.dqt_tables[0];
        assert_eq!(*luma_id, 0, "first DQT must be table 0 (luma)");
        assert_eq!(luma_qt.len(), 64, "DQT must have 64 values");

        let (chroma_id, chroma_qt) = &structure.dqt_tables[1];
        assert_eq!(*chroma_id, 1, "second DQT must be table 1 (chroma)");
        assert_eq!(chroma_qt.len(), 64, "DQT must have 64 values");

        for &v in luma_qt {
            assert!(v >= 1 && v <= 255, "DQT value out of range: {}", v);
        }
        for &v in chroma_qt {
            assert!(v >= 1 && v <= 255, "DQT value out of range: {}", v);
        }
    }

    #[test]
    fn test_jpeg_dht_tables() {
        let yuv = video_codec::YuvFrame::black(16, 16, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();

        let has_luma_dc = structure
            .dht_tables
            .iter()
            .any(|t| t.table_class == 0 && t.table_id == 0);
        let has_luma_ac = structure
            .dht_tables
            .iter()
            .any(|t| t.table_class == 1 && t.table_id == 0);
        let has_chroma_dc = structure
            .dht_tables
            .iter()
            .any(|t| t.table_class == 0 && t.table_id == 1);
        let has_chroma_ac = structure
            .dht_tables
            .iter()
            .any(|t| t.table_class == 1 && t.table_id == 1);

        assert!(has_luma_dc, "must have luma DC Huffman table");
        assert!(has_luma_ac, "must have luma AC Huffman table");
        assert!(has_chroma_dc, "must have chroma DC Huffman table");
        assert!(has_chroma_ac, "must have chroma AC Huffman table");

        for dht in &structure.dht_tables {
            let vals_count: usize = dht.bits.iter().map(|&b| b as usize).sum();
            assert_eq!(
                dht.vals.len(),
                vals_count,
                "DHT vals count must match BITS sum"
            );
        }
    }

    #[test]
    fn test_jpeg_dqt_length_correct() {
        let yuv = video_codec::YuvFrame::black(16, 16, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();

        let mut pos = 2;
        while pos < jpeg.len() - 1 {
            if jpeg[pos] == 0xFF && jpeg[pos + 1] == 0xDB {
                let len = u16::from_be_bytes([jpeg[pos + 2], jpeg[pos + 3]]);
                assert_eq!(
                    len, 67,
                    "DQT marker length must be 67 (2+1+64), got {}",
                    len
                );
                return;
            }
            pos += 1;
        }
        panic!("DQT marker not found");
    }

    // ─── byte stuffing 测试 ────────────────────────────────────────

    #[test]
    fn test_jpeg_byte_stuffing() {
        let mut yuv = video_codec::YuvFrame::black(16, 16, 0);
        for i in 0..yuv.y.len() {
            yuv.y[i] = ((i * 37 + 13) % 256) as u8;
        }
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 100).unwrap();

        // Parse to find the actual entropy data boundaries
        let structure = parse_jpeg_structure(&jpeg).unwrap();
        assert!(structure.has_sos, "SOS must be present");

        // Find SOS marker and skip past it to get entropy data start
        let sos_pos = find_marker(&jpeg, 0xDA).expect("SOS marker not found");
        let sos_len = u16::from_be_bytes([jpeg[sos_pos + 2], jpeg[sos_pos + 3]]) as usize;
        let entropy_start = sos_pos + 2 + sos_len;
        let entropy_end = entropy_start + structure.entropy_data_size;

        assert!(
            entropy_end <= jpeg.len() - 2,
            "entropy end must be before EOI"
        );

        let mut i = entropy_start;
        while i < entropy_end {
            if jpeg[i] == 0xFF {
                assert!(
                    jpeg[i + 1] == 0x00 || jpeg[i + 1] == 0xFF,
                    "unstuffed 0xFF at pos {} followed by 0x{:02X}",
                    i,
                    jpeg[i + 1]
                );
                if jpeg[i + 1] == 0x00 {
                    i += 2;
                    continue;
                }
            }
            i += 1;
        }
    }

    fn find_marker(data: &[u8], marker: u8) -> Option<usize> {
        let mut i = 0;
        while i < data.len() - 1 {
            if data[i] == 0xFF && data[i + 1] == marker {
                return Some(i);
            }
            i += 1;
        }
        None
    }

    // ─── encode_vlc 单元测试 ───────────────────────────────────────

    #[test]
    fn test_encode_vlc_zero() {
        let (cat, bits, nbits) = encode_vlc(0);
        assert_eq!(cat, 0);
        assert_eq!(bits, 0);
        assert_eq!(nbits, 0);
    }

    #[test]
    fn test_encode_vlc_positive() {
        let (cat, bits, nbits) = encode_vlc(5);
        assert_eq!(cat, 3, "5 = 101b → category 3");
        assert_eq!(bits, 5, "positive value → bits = value");
        assert_eq!(nbits, 3);
    }

    #[test]
    fn test_encode_vlc_negative() {
        let (cat, bits, nbits) = encode_vlc(-5);
        assert_eq!(cat, 3, "-5 → category 3");
        assert_eq!(
            bits, 2,
            "-5 → 5-1=4, 2^3-1-4=3... wait: -5 + (2^3 - 1) = -5 + 7 = 2"
        );
        assert_eq!(nbits, 3);
    }

    #[test]
    fn test_encode_vlc_roundtrip() {
        for v in [-1023, -255, -128, -1, 0, 1, 128, 255, 1023] {
            let (cat, bits, nbits) = encode_vlc(v);
            let decoded = decode_vlc_value(bits, cat);
            assert_eq!(decoded, v, "VLC roundtrip failed for {}", v);
        }
    }

    // ─── DCT 单元测试 ──────────────────────────────────────────────

    #[test]
    fn test_dct_constant_block() {
        let mut block = [0i32; 64];
        for i in 0..64 {
            block[i] = 128 - 128; // level shift → 0
        }
        dct_8x8(&mut block);
        for i in 0..64 {
            assert_eq!(
                block[i], 0,
                "DCT of zero block must be zero, got {} at {}",
                block[i], i
            );
        }
    }

    #[test]
    fn test_dct_dc_coefficient() {
        let mut block = [0i32; 64];
        for i in 0..64 {
            block[i] = 255 - 128; // level shift → 127
        }
        dct_8x8(&mut block);
        let dc = block[0];
        // F(0,0) = (1/4) * (1/√2)² * 64 * 127 = 0.25 * 0.5 * 64 * 127 = 1016
        let expected = 1016i32;
        assert!(
            (dc - expected).abs() <= 1,
            "DC coefficient should be ~{}, got {}",
            expected,
            dc
        );
        for i in 1..64 {
            assert!(
                block[i].abs() <= 1,
                "AC coefficients of constant block should be ~0, got {} at {}",
                block[i],
                i
            );
        }
    }

    #[test]
    fn test_dct_idct_roundtrip() {
        let mut block = [0i32; 64];
        for i in 0..64 {
            block[i] = ((i as i32 * 31 + 17) % 256) - 128;
        }
        let original = block;

        dct_8x8(&mut block);

        let mut dct_f64 = [0.0f64; 64];
        for i in 0..64 {
            dct_f64[i] = block[i] as f64;
        }
        let mut reconstructed = [0u8; 64];
        idct_8x8(&dct_f64, &mut reconstructed);

        for i in 0..64 {
            let orig_val = (original[i] + 128).clamp(0, 255) as u8;
            assert!(
                (orig_val as i32 - reconstructed[i] as i32).abs() <= 1,
                "DCT→IDCT roundtrip error at {}: orig={}, recon={}",
                i,
                orig_val,
                reconstructed[i]
            );
        }
    }

    // ─── 量化表测试 ────────────────────────────────────────────────

    #[test]
    fn test_make_qt_quality_50() {
        let qt = make_qt(&STD_LUMA_QT, 50);
        assert_eq!(qt[0], 16, "quality=50 should not scale (scale=100)");
        assert_eq!(qt[1], 11);
    }

    #[test]
    fn test_make_qt_quality_100() {
        let qt = make_qt(&STD_LUMA_QT, 100);
        for &v in &qt {
            assert_eq!(v, 1, "quality=100 should give all-1 quantization table");
        }
    }

    #[test]
    fn test_make_qt_quality_1() {
        let qt = make_qt(&STD_LUMA_QT, 1);
        for &v in &qt {
            assert_eq!(v, 255, "quality=1 should give all-255 quantization table");
        }
    }

    #[test]
    fn test_make_qt_quality_clamp() {
        let qt_low = make_qt(&STD_LUMA_QT, 0);
        let qt_1 = make_qt(&STD_LUMA_QT, 1);
        assert_eq!(qt_low, qt_1, "quality=0 should clamp to 1");

        let qt_high = make_qt(&STD_LUMA_QT, 200);
        let qt_100 = make_qt(&STD_LUMA_QT, 100);
        assert_eq!(qt_high, qt_100, "quality=200 should clamp to 100");
    }

    // ─── Huffman 表测试 ────────────────────────────────────────────

    #[test]
    fn test_huff_table_build_luma_dc() {
        let table = HuffTable::build(&LUMA_DC_BITS, &LUMA_DC_VALS);
        let (code0, len0) = table.encode(0);
        assert!(len0 > 0, "symbol 0 must have a valid code");

        let (code11, len11) = table.encode(11);
        assert!(len11 > 0, "symbol 11 must have a valid code");

        // All 12 DC symbols (0-11) must have valid codes
        for sym in 0..=11u8 {
            let (_, len) = table.encode(sym);
            assert!(len > 0, "DC symbol {} must have a valid code", sym);
        }

        // Codes must be unique
        let mut codes = std::collections::HashSet::new();
        for sym in 0..=11u8 {
            let (code, len) = table.encode(sym);
            codes.insert((code, len));
        }
        assert_eq!(codes.len(), 12, "all 12 DC codes must be unique");

        let _ = code0;
        let _ = code11;
    }

    #[test]
    fn test_huff_table_luma_dc_uses_correct_vals() {
        let table = HuffTable::build(&LUMA_DC_BITS, &LUMA_DC_VALS);
        for sym in 0..=11u8 {
            let (_, len) = table.encode(sym);
            assert!(len > 0, "DC symbol {} must have a valid code", sym);
        }
        let (_, len12) = table.encode(12);
        assert_eq!(len12, 0, "symbol 12 should not exist in DC table");
    }

    // ─── 多种尺寸测试 ──────────────────────────────────────────────

    #[test]
    fn test_jpeg_16x16() {
        let yuv = video_codec::YuvFrame::black(16, 16, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();
        let sof0 = structure.sof0.as_ref().unwrap();
        assert_eq!(sof0.width, 16);
        assert_eq!(sof0.height, 16);
    }

    #[test]
    fn test_jpeg_32x32() {
        let yuv = video_codec::YuvFrame::black(32, 32, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();
        assert_eq!(structure.sof0.as_ref().unwrap().width, 32);
    }

    #[test]
    fn test_jpeg_64x64() {
        let yuv = video_codec::YuvFrame::black(64, 64, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();
        assert_eq!(structure.sof0.as_ref().unwrap().width, 64);
    }

    #[test]
    fn test_jpeg_non_aligned_17x13() {
        let yuv = video_codec::YuvFrame::black(17, 13, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();
        let sof0 = structure.sof0.as_ref().unwrap();
        assert_eq!(sof0.width, 17, "non-aligned width must be preserved");
        assert_eq!(sof0.height, 13, "non-aligned height must be preserved");
    }

    #[test]
    fn test_jpeg_non_aligned_33x25() {
        let yuv = video_codec::YuvFrame::black(33, 25, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 75).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();
        let sof0 = structure.sof0.as_ref().unwrap();
        assert_eq!(sof0.width, 33);
        assert_eq!(sof0.height, 25);
    }

    #[test]
    fn test_jpeg_1x1() {
        let yuv = video_codec::YuvFrame::black(1, 1, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        let structure = parse_jpeg_structure(&jpeg).unwrap();
        let sof0 = structure.sof0.as_ref().unwrap();
        assert_eq!(sof0.width, 1);
        assert_eq!(sof0.height, 1);
    }

    // ─── 多种 quality 测试 ─────────────────────────────────────────

    #[test]
    fn test_jpeg_quality_levels() {
        let yuv = video_codec::YuvFrame::black(32, 32, 0);
        for q in [1u8, 10, 25, 50, 75, 90, 100] {
            let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, q).unwrap();
            let structure = parse_jpeg_structure(&jpeg).unwrap();
            assert!(
                structure.has_soi && structure.has_eoi,
                "quality={} must produce valid JPEG",
                q
            );
        }
    }

    #[test]
    fn test_jpeg_quality_affects_file_size() {
        let mut yuv = video_codec::YuvFrame::black(32, 32, 0);
        for i in 0..yuv.y.len() {
            yuv.y[i] = ((i * 37) % 256) as u8;
        }

        let jpeg_low = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 10).unwrap();
        let jpeg_high = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 95).unwrap();

        let low_struct = parse_jpeg_structure(&jpeg_low).unwrap();
        let high_struct = parse_jpeg_structure(&jpeg_high).unwrap();

        assert!(
            high_struct.entropy_data_size >= low_struct.entropy_data_size,
            "higher quality should produce more entropy data (low={}, high={})",
            low_struct.entropy_data_size,
            high_struct.entropy_data_size
        );
    }

    // ─── roundtrip 编解码测试 ──────────────────────────────────────

    #[test]
    fn test_jpeg_roundtrip_black() {
        let yuv = video_codec::YuvFrame::black(16, 16, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 100).unwrap();

        let mut decoder = JpegDecoder::new(&jpeg);
        let (w, h, y_decoded, cb_decoded, cr_decoded) = decoder.decode().unwrap();

        assert_eq!(w, 16);
        assert_eq!(h, 16);

        for &y_val in &y_decoded {
            assert!(y_val <= 2, "black image Y should be ~0, got {}", y_val);
        }
        for &cb_val in &cb_decoded {
            assert!(
                cb_val >= 126 && cb_val <= 130,
                "black image Cb should be ~128, got {}",
                cb_val
            );
        }
        for &cr_val in &cr_decoded {
            assert!(
                cr_val >= 126 && cr_val <= 130,
                "black image Cr should be ~128, got {}",
                cr_val
            );
        }
    }

    #[test]
    fn test_jpeg_roundtrip_white() {
        let mut yuv = video_codec::YuvFrame::black(16, 16, 0);
        for i in 0..yuv.y.len() {
            yuv.y[i] = 255;
        }

        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 100).unwrap();
        let mut decoder = JpegDecoder::new(&jpeg);
        let (_w, _h, y_decoded, _cb, _cr) = decoder.decode().unwrap();

        for &y_val in &y_decoded {
            assert!(y_val >= 253, "white image Y should be ~255, got {}", y_val);
        }
    }

    #[test]
    fn test_jpeg_roundtrip_gray() {
        let mut yuv = video_codec::YuvFrame::black(16, 16, 0);
        for i in 0..yuv.y.len() {
            yuv.y[i] = 128;
        }

        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 100).unwrap();
        let mut decoder = JpegDecoder::new(&jpeg);
        let (_w, _h, y_decoded, _cb, _cr) = decoder.decode().unwrap();

        for &y_val in &y_decoded {
            assert!(
                (y_val as i32 - 128).abs() <= 2,
                "gray image Y should be ~128, got {}",
                y_val
            );
        }
    }

    #[test]
    fn test_jpeg_roundtrip_gradient() {
        let mut yuv = video_codec::YuvFrame::black(32, 32, 0);
        for row in 0..32 {
            for col in 0..32 {
                yuv.y[row * 32 + col] = ((row + col) * 4) as u8;
            }
        }

        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 95).unwrap();
        let mut decoder = JpegDecoder::new(&jpeg);
        let (w, h, y_decoded, _cb, _cr) = decoder.decode().unwrap();

        assert_eq!(w, 32);
        assert_eq!(h, 32);

        let padded_w = 32usize;
        let mut max_error = 0i32;
        for row in 0..32 {
            for col in 0..32 {
                let orig = yuv.y[row * 32 + col] as i32;
                let decoded = y_decoded[row * padded_w + col] as i32;
                let err = (orig - decoded).abs();
                max_error = max_error.max(err);
            }
        }
        assert!(
            max_error <= 5,
            "gradient roundtrip max error should be <= 5, got {}",
            max_error
        );
    }

    #[test]
    fn test_jpeg_roundtrip_solid_color() {
        let mut yuv = video_codec::YuvFrame::black(16, 16, 0);
        for i in 0..yuv.y.len() {
            yuv.y[i] = 200;
        }

        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 90).unwrap();
        let mut decoder = JpegDecoder::new(&jpeg);
        let (_w, _h, y_decoded, _cb, _cr) = decoder.decode().unwrap();

        let mut max_error = 0i32;
        for &y_val in &y_decoded {
            max_error = max_error.max((y_val as i32 - 200).abs());
        }
        assert!(
            max_error <= 3,
            "solid color roundtrip max error should be <= 3, got {}",
            max_error
        );
    }

    #[test]
    fn test_jpeg_roundtrip_32x32() {
        let mut yuv = video_codec::YuvFrame::black(32, 32, 0);
        for i in 0..yuv.y.len() {
            yuv.y[i] = ((i * 17 + 3) % 256) as u8;
        }

        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 95).unwrap();
        let mut decoder = JpegDecoder::new(&jpeg);
        let (w, h, y_decoded, _cb, _cr) = decoder.decode().unwrap();

        assert_eq!(w, 32);
        assert_eq!(h, 32);

        let padded_w = 32usize;
        let mut total_error = 0i64;
        let mut max_error = 0i32;
        for row in 0..32 {
            for col in 0..32 {
                let orig = yuv.y[row * 32 + col] as i32;
                let decoded = y_decoded[row * padded_w + col] as i32;
                let err = (orig - decoded).abs();
                total_error += err as i64;
                max_error = max_error.max(err);
            }
        }
        let avg_error = total_error as f64 / (32 * 32) as f64;
        assert!(
            avg_error < 10.0,
            "average error should be < 10, got {}",
            avg_error
        );
        assert!(
            max_error < 30,
            "max error should be < 30, got {}",
            max_error
        );
    }

    #[test]
    fn test_jpeg_roundtrip_non_aligned() {
        let mut yuv = video_codec::YuvFrame::black(17, 13, 0);
        for i in 0..yuv.y.len() {
            yuv.y[i] = ((i * 23 + 7) % 256) as u8;
        }

        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 95).unwrap();
        let mut decoder = JpegDecoder::new(&jpeg);
        let (w, h, y_decoded, _cb, _cr) = decoder.decode().unwrap();

        assert_eq!(w, 17);
        assert_eq!(h, 13);

        let padded_w = 32usize;
        let mut max_error = 0i32;
        for row in 0..13 {
            for col in 0..17 {
                let orig = yuv.y[row * 17 + col] as i32;
                let decoded = y_decoded[row * padded_w + col] as i32;
                max_error = max_error.max((orig - decoded).abs());
            }
        }
        assert!(
            max_error < 30,
            "non-aligned roundtrip max error should be < 30, got {}",
            max_error
        );
    }

    // ─── BitWriter 测试 ────────────────────────────────────────────

    #[test]
    fn test_bit_writer_basic() {
        let mut writer = BitWriter::new();
        writer.write_bits(0b1010_1010, 8);
        writer.flush();
        assert_eq!(writer.data, vec![0b1010_1010]);
    }

    #[test]
    fn test_bit_writer_multi_byte() {
        let mut writer = BitWriter::new();
        writer.write_bits(0xFF, 8);
        writer.write_bits(0x00, 8);
        writer.write_bits(0xFF, 8);
        writer.flush();
        assert_eq!(writer.data, vec![0xFF, 0x00, 0x00, 0xFF, 0x00]);
    }

    #[test]
    fn test_bit_writer_byte_stuffing() {
        let mut writer = BitWriter::new();
        writer.write_bits(0xFF, 8);
        writer.flush();
        assert_eq!(
            writer.data,
            vec![0xFF, 0x00],
            "0xFF must be followed by 0x00 stuff byte"
        );
    }

    #[test]
    fn test_bit_writer_partial_byte() {
        let mut writer = BitWriter::new();
        writer.write_bits(0b101, 3);
        writer.flush();
        assert_eq!(writer.data.len(), 1);
        assert_eq!(
            writer.data[0] & 0b11100000,
            0b10100000,
            "partial bits should be left-aligned"
        );
    }

    #[test]
    fn test_bit_writer_consecutive_ff() {
        let mut writer = BitWriter::new();
        writer.write_bits(0xFFFF, 16);
        writer.flush();
        assert_eq!(writer.data, vec![0xFF, 0x00, 0xFF, 0x00]);
    }
}
