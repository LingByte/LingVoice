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
    pub fn from_yuv(yuv: &video_codec::YuvFrame, format: ThumbnailFormat, quality: u8) -> Result<Vec<u8>> {
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
                "-ss", timestamp,
                "-i", file_path,
                "-frames:v", "1",
                "-vf", &scale_filter,
                "-f", "image2pipe",
                "-vcodec", format.ffmpeg_codec(),
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
                "-i", file_path,
                "-vf", &format!("fps=1/{interval_sec},{scale_filter}"),
                "-f", "image2",
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
        let dir = std::fs::read_dir(output_dir)
            .map_err(|e| anyhow!("read output dir: {e}"))?;
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
    /// - RGB → YCbCr 色彩空间转换
    /// - 8x8 DCT 变换
    /// - 量化（使用标准量化表，按 quality 缩放）
    /// - Zigzag 扫描
    /// - Huffman 编码
    fn yuv_to_jpeg(yuv: &video_codec::YuvFrame, quality: u8) -> Result<Vec<u8>> {
        let width = yuv.width as usize;
        let height = yuv.height as usize;

        // 确保宽高是 8 的倍数（pad if needed）
        let padded_w = (width + 7) & !7;
        let padded_h = (height + 7) & !7;

        // 从 YUV 直接获取 YCbCr 分量（YUV420p ≈ YCbCr 4:2:0）
        // 补齐到 8 的倍数
        let mut y_plane = vec![0u8; padded_w * padded_h];
        let mut cb_plane = vec![128u8; (padded_w / 2) * (padded_h / 2)];
        let mut cr_plane = vec![128u8; (padded_w / 2) * (padded_h / 2)];

        for row in 0..height {
            for col in 0..width {
                y_plane[row * padded_w + col] = yuv.y[row * width + col];
            }
            // pad 最后一列
            if padded_w > width {
                let last = y_plane[row * padded_w + width - 1];
                for col in width..padded_w {
                    y_plane[row * padded_w + col] = last;
                }
            }
        }
        // pad 最后一行
        if padded_h > height {
            for col in 0..padded_w {
                y_plane[height * padded_w + col] = y_plane[(height - 1) * padded_w + col];
            }
            for row in (height + 1)..padded_h {
                for col in 0..padded_w {
                    y_plane[row * padded_w + col] = y_plane[(height - 1) * padded_w + col];
                }
            }
        }

        // Cb/Cr planes (4:2:0 subsampled)
        let chroma_w = width / 2;
        let chroma_h = height / 2;
        for row in 0..chroma_h {
            for col in 0..chroma_w {
                cb_plane[row * (padded_w / 2) + col] = yuv.u[row * chroma_w + col];
                cr_plane[row * (padded_w / 2) + col] = yuv.v[row * chroma_w + col];
            }
        }

        encode_jpeg(
            &y_plane,
            &cb_plane,
            &cr_plane,
            padded_w,
            padded_h,
            width,
            height,
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

/// 标准亮度量化表
const STD_LUMA_QT: [u8; 64] = [
    16, 11, 10, 16, 24, 40, 51, 61,
    12, 12, 14, 19, 26, 58, 60, 55,
    14, 13, 16, 24, 40, 57, 69, 56,
    14, 17, 22, 29, 51, 87, 80, 62,
    18, 22, 37, 56, 68,109,103, 77,
    24, 35, 55, 64, 81,104,113, 92,
    49, 64, 78, 87,103,121,120,101,
    72, 92, 95, 98,112,100,103, 99,
];

/// 标准色度量化表
const STD_CHROMA_QT: [u8; 64] = [
    17, 18, 24, 47, 99, 99, 99, 99,
    18, 21, 26, 66, 99, 99, 99, 99,
    24, 26, 56, 99, 99, 99, 99, 99,
    47, 66, 99, 99, 99, 99, 99, 99,
    99, 99, 99, 99, 99, 99, 99, 99,
    99, 99, 99, 99, 99, 99, 99, 99,
    99, 99, 99, 99, 99, 99, 99, 99,
    99, 99, 99, 99, 99, 99, 99, 99,
];

/// Zigzag 扫描顺序
const ZIGZAG: [u8; 64] = [
     0,  1,  8, 16,  9,  2,  3, 10,
    17, 24, 32, 25, 18, 11,  4,  5,
    12, 19, 26, 33, 40, 48, 41, 34,
    27, 20, 13,  6,  7, 14, 21, 28,
    35, 42, 49, 56, 57, 50, 43, 36,
    29, 22, 15, 23, 30, 37, 44, 51,
    58, 59, 52, 45, 38, 31, 39, 46,
    53, 60, 61, 54, 47, 55, 62, 63,
];

/// 标准 Huffman 表 — 亮度 DC
const LUMA_DC_BITS: [u8; 16] = [0,1,5,1,1,1,1,1,1,0,0,0,0,0,0,0];
const LUMA_DC_VALS: [u8; 12] = [0,1,2,3,4,5,6,7,8,9,10,11];

/// 标准 Huffman 表 — 亮度 AC
const LUMA_AC_BITS: [u8; 16] = [0,2,1,3,3,2,4,3,5,5,4,4,0,0,1,0x7d];
const LUMA_AC_VALS: [u8; 162] = [
    0x01,0x02,0x03,0x00,0x04,0x11,0x05,0x12,0x21,0x31,0x41,0x06,0x13,0x51,0x61,0x07,
    0x22,0x71,0x14,0x32,0x81,0x91,0xa1,0x08,0x23,0x42,0xb1,0xc1,0x15,0x52,0xd1,0xf0,
    0x24,0x33,0x62,0x72,0x82,0x09,0x0a,0x16,0x17,0x18,0x19,0x1a,0x25,0x26,0x27,0x28,
    0x29,0x2a,0x34,0x35,0x36,0x37,0x38,0x39,0x3a,0x43,0x44,0x45,0x46,0x47,0x48,0x49,
    0x4a,0x53,0x54,0x55,0x56,0x57,0x58,0x59,0x5a,0x63,0x64,0x65,0x66,0x67,0x68,0x69,
    0x6a,0x73,0x74,0x75,0x76,0x77,0x78,0x79,0x7a,0x83,0x84,0x85,0x86,0x87,0x88,0x89,
    0x8a,0x92,0x93,0x94,0x95,0x96,0x97,0x98,0x99,0x9a,0xa2,0xa3,0xa4,0xa5,0xa6,0xa7,
    0xa8,0xa9,0xaa,0xb2,0xb3,0xb4,0xb5,0xb6,0xb7,0xb8,0xb9,0xba,0xc2,0xc3,0xc4,0xc5,
    0xc6,0xc7,0xc8,0xc9,0xca,0xd2,0xd3,0xd4,0xd5,0xd6,0xd7,0xd8,0xd9,0xda,0xe1,0xe2,
    0xe3,0xe4,0xe5,0xe6,0xe7,0xe8,0xe9,0xea,0xf1,0xf2,0xf3,0xf4,0xf5,0xf6,0xf7,0xf8,
    0xf9,0xfa,
];

/// 标准 Huffman 表 — 色度 DC
const CHROMA_DC_BITS: [u8; 16] = [0,3,1,1,1,1,1,1,1,1,1,0,0,0,0,0];
const CHROMA_DC_VALS: [u8; 12] = [0,1,2,3,4,5,6,7,8,9,10,11];

/// 标准 Huffman 表 — 色度 AC
const CHROMA_AC_BITS: [u8; 16] = [0,2,1,2,4,4,3,4,7,5,4,4,0,1,2,0x77];
const CHROMA_AC_VALS: [u8; 140] = [
    0x00,0x01,0x02,0x03,0x11,0x04,0x05,0x21,0x31,0x06,0x12,0x41,0x51,0x07,0x61,0x71,
    0x13,0x22,0x32,0x81,0x08,0x14,0x42,0x91,0xa1,0xb1,0xc1,0x09,0x23,0x33,0x52,0xd0,
    0x15,0x62,0x72,0xe1,0xf0,0x16,0x24,0x34,0x25,0xe2,0x35,0x43,0x46,0x53,0x55,0x56,
    0x57,0x58,0x59,0x5a,0x63,0x64,0x65,0x66,0x67,0x68,0x69,0x6a,0x73,0x74,0x75,0x76,
    0x77,0x78,0x79,0x7a,0x82,0x83,0x84,0x85,0x86,0x87,0x88,0x89,0x8a,0x92,0x93,0x94,
    0x95,0x96,0x97,0x98,0x99,0x9a,0xa2,0xa3,0xa4,0xa5,0xa6,0xa7,0xa8,0xa9,0xaa,0xb2,
    0xb3,0xb4,0xb5,0xb6,0xb7,0xb8,0xb9,0xba,0xc2,0xc3,0xc4,0xc5,0xc6,0xc7,0xc8,0xc9,
    0xca,0xd2,0xd3,0xd4,0xd5,0xd6,0xd7,0xd8,0xd9,0xda,0xe2,0xe3,0xe4,0xe5,0xe6,0xe7,
    0xe8,0xe9,0xea,0xf2,0xf3,0xf4,0xf5,0xf6,0xf7,0xf8,0xf9,0xfa,
];

/// Huffman 编码表
struct HuffTable {
    /// symbol → (code, length)
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

/// 比特写入器
struct BitWriter {
    data: Vec<u8>,
    current_byte: u8,
    bit_pos: u8, // 0..8, number of bits written in current byte
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
        for i in (0..n_bits).rev() {
            let bit = ((value >> i) & 1) as u8;
            self.current_byte = (self.current_byte << 1) | bit;
            self.bit_pos += 1;
            if self.bit_pos == 8 {
                self.data.push(self.current_byte);
                self.current_byte = 0;
                self.bit_pos = 0;
            }
        }
    }

    fn flush(&mut self) {
        if self.bit_pos > 0 {
            self.current_byte <<= 8 - self.bit_pos;
            // pad with 1s
            self.current_byte |= (1 << (8 - self.bit_pos)) - 1;
            self.data.push(self.current_byte);
            self.current_byte = 0;
            self.bit_pos = 0;
        }
    }
}

/// 8x8 DCT (Type-II)
fn dct_8x8(block: &mut [i32; 64]) {
    let mut tmp = [0i64; 64];

    // 行变换
    for row in 0..8 {
        let offset = row * 8;
        for k in 0..8 {
            let mut sum: i64 = 0;
            for n in 0..8 {
                let cos = ((n as f64 + 0.5) * k as f64 * std::f64::consts::PI / 8.0).cos();
                sum += block[offset + n] as i64 * (cos * 8192.0) as i64;
            }
            tmp[offset + k] = sum;
        }
    }

    // 列变换
    for col in 0..8 {
        for k in 0..8 {
            let mut sum: i64 = 0;
            for n in 0..8 {
                let cos = ((n as f64 + 0.5) * k as f64 * std::f64::consts::PI / 8.0).cos();
                sum += tmp[n * 8 + col] * (cos * 8192.0) as i64;
            }
            block[k * 8 + col] = (sum >> 26) as i32; // scale down
        }
    }
}

/// 生成量化表（按 quality 缩放）
fn make_qt(std_qt: &[u8; 64], quality: u8) -> [u16; 64] {
    let q = quality.clamp(1, 100) as i32;
    let scale = if q < 50 {
        5000 / q
    } else {
        200 - 2 * q
    };

    let mut qt = [0u16; 64];
    for i in 0..64 {
        let val = (std_qt[i] as i32 * scale + 50) / 100;
        qt[i] = val.clamp(1, 255) as u16;
    }
    qt
}

/// 编码一个 8x8 块
fn encode_block(
    writer: &mut BitWriter,
    block: &[u8; 64],
    qt: &[u16; 64],
    dc_table: &HuffTable,
    ac_table: &HuffTable,
    prev_dc: &mut i32,
) {
    // 1. level shift → DCT → 量化
    let mut dct_block = [0i32; 64];
    for i in 0..64 {
        dct_block[i] = block[i] as i32 - 128;
    }
    dct_8x8(&mut dct_block);

    // 量化
    let mut quantized = [0i32; 64];
    for i in 0..64 {
        quantized[i] = dct_block[i] / (qt[i] as i32);
    }

    // 2. Zigzag 扫描
    let mut zz = [0i32; 64];
    for i in 0..64 {
        zz[i] = quantized[ZIGZAG[i] as usize];
    }

    // 3. DC 编码（差分）
    let dc_val = zz[0];
    let dc_diff = dc_val - *prev_dc;
    *prev_dc = dc_val;

    let (dc_mag, dc_bits) = encode_vlc(dc_diff);
    let (code, len) = dc_table.encode(dc_mag as u8);
    writer.write_bits(code as u32, len);
    if dc_bits > 0 {
        writer.write_bits(dc_bits as u32, dc_bits);
    }

    // 4. AC 编码（游程编码）
    let mut run = 0;
    for i in 1..64 {
        if zz[i] == 0 {
            run += 1;
        } else {
            while run > 15 {
                // ZRL (16 zeros)
                let (code, len) = ac_table.encode(0xF0);
                writer.write_bits(code as u32, len);
                run -= 16;
            }
            let (ac_mag, ac_bits) = encode_vlc(zz[i]);
            let sym = ((run << 4) | ac_mag as usize) as u8;
            let (code, len) = ac_table.encode(sym);
            writer.write_bits(code as u32, len);
            if ac_bits > 0 {
                writer.write_bits(ac_bits as u32, ac_bits);
            }
            run = 0;
        }
    }
    // EOB if remaining are zeros
    if run > 0 {
        let (code, len) = ac_table.encode(0x00);
        writer.write_bits(code as u32, len);
    }
}

/// VLC 编码：返回 (magnitude_category, additional_bits)
fn encode_vlc(value: i32) -> (u8, u8) {
    if value == 0 {
        return (0, 0);
    }
    let abs_val = value.unsigned_abs();
    let mag = 32 - abs_val.leading_zeros() as u8;
    (mag, mag)
}

/// Baseline JPEG 编码
fn encode_jpeg(
    y_plane: &[u8],
    cb_plane: &[u8],
    cr_plane: &[u8],
    padded_w: usize,
    padded_h: usize,
    orig_w: usize,
    orig_h: usize,
    quality: u8,
) -> Result<Vec<u8>> {
    let mut jpeg = Vec::new();

    // SOI
    jpeg.extend_from_slice(&[0xFF, 0xD8]);

    // DQT 标记 — 亮度
    write_dqt(&mut jpeg, 0, &make_qt(&STD_LUMA_QT, quality));
    // DQT 标记 — 色度
    write_dqt(&mut jpeg, 1, &make_qt(&STD_CHROMA_QT, quality));

    // SOF0 标记 (Baseline)
    write_sof0(&mut jpeg, orig_w as u16, orig_h as u16);

    // DHT 标记
    write_dht(&mut jpeg, 0, 0, &LUMA_DC_BITS, &LUMA_DC_VALS);
    write_dht(&mut jpeg, 0, 1, &LUMA_AC_BITS, &LUMA_AC_VALS);
    write_dht(&mut jpeg, 1, 0, &CHROMA_DC_BITS, &CHROMA_DC_VALS);
    write_dht(&mut jpeg, 1, 1, &CHROMA_AC_BITS, &CHROMA_AC_VALS);

    // SOS 标记
    write_sos(&mut jpeg);

    // 编码图像数据
    let luma_dc = HuffTable::build(&LUMA_DC_BITS, &LUMA_AC_VALS[..12]);
    let luma_ac = HuffTable::build(&LUMA_AC_BITS, &LUMA_AC_VALS);
    let chroma_dc = HuffTable::build(&CHROMA_DC_BITS, &CHROMA_DC_VALS);
    let chroma_ac = HuffTable::build(&CHROMA_AC_BITS, &CHROMA_AC_VALS);

    let mut writer = BitWriter::new();
    let mut prev_dc_y = 0i32;
    let mut prev_dc_cb = 0i32;
    let mut prev_dc_cr = 0i32;

    let luma_qt = make_qt(&STD_LUMA_QT, quality);
    let chroma_qt = make_qt(&STD_CHROMA_QT, quality);

    // 按 MCU 编码（每个 MCU = 4 Y blocks + 1 Cb + 1 Cr for 4:2:0）
    for mcu_y in (0..padded_h).step_by(16) {
        for mcu_x in (0..padded_w).step_by(16) {
            // 4 Y blocks
            for by in 0..2 {
                for bx in 0..2 {
                    let mut block = [0u8; 64];
                    let base_x = mcu_x + bx * 8;
                    let base_y = mcu_y + by * 8;
                    for row in 0..8 {
                        for col in 0..8 {
                            let px = (base_x + col).min(padded_w - 1);
                            let py = (base_y + row).min(padded_h - 1);
                            block[row * 8 + col] = y_plane[py * padded_w + px];
                        }
                    }
                    encode_block(&mut writer, &block, &luma_qt, &luma_dc, &luma_ac, &mut prev_dc_y);
                }
            }
            // 1 Cb block
            let mut block = [0u8; 64];
            let cb_base_x = mcu_x / 2;
            let cb_base_y = mcu_y / 2;
            let cb_w = padded_w / 2;
            for row in 0..8 {
                for col in 0..8 {
                    let px = (cb_base_x + col).min(cb_w - 1);
                    let py = (cb_base_y + row).min(padded_h / 2 - 1);
                    block[row * 8 + col] = cb_plane[py * cb_w + px];
                }
            }
            encode_block(&mut writer, &block, &chroma_qt, &chroma_dc, &chroma_ac, &mut prev_dc_cb);

            // 1 Cr block
            let mut block = [0u8; 64];
            for row in 0..8 {
                for col in 0..8 {
                    let px = (cb_base_x + col).min(cb_w - 1);
                    let py = (cb_base_y + row).min(padded_h / 2 - 1);
                    block[row * 8 + col] = cr_plane[py * cb_w + px];
                }
            }
            encode_block(&mut writer, &block, &chroma_qt, &chroma_dc, &chroma_ac, &mut prev_dc_cr);
        }
    }

    writer.flush();
    jpeg.extend_from_slice(&writer.data);

    // EOI
    jpeg.extend_from_slice(&[0xFF, 0xD9]);

    Ok(jpeg)
}

/// 写 DQT 标记
fn write_dqt(jpeg: &mut Vec<u8>, table_id: u8, qt: &[u16; 64]) {
    jpeg.extend_from_slice(&[0xFF, 0xDB]);
    jpeg.extend_from_slice(&65u16.to_be_bytes()); // length = 2 + 1 + 64
    jpeg.push(table_id);
    for &val in qt {
        jpeg.push(val as u8);
    }
}

/// 写 SOF0 标记
fn write_sof0(jpeg: &mut Vec<u8>, width: u16, height: u16) {
    jpeg.extend_from_slice(&[0xFF, 0xC0]);
    jpeg.extend_from_slice(&17u16.to_be_bytes()); // length
    jpeg.push(8); // precision
    jpeg.extend_from_slice(&height.to_be_bytes());
    jpeg.extend_from_slice(&width.to_be_bytes());
    jpeg.push(3); // 3 components (Y, Cb, Cr)
    // Y: id=1, sampling=2x2 (0x22), qt=0
    jpeg.extend_from_slice(&[1, 0x22, 0]);
    // Cb: id=2, sampling=1x1 (0x11), qt=1
    jpeg.extend_from_slice(&[2, 0x11, 1]);
    // Cr: id=3, sampling=1x1 (0x11), qt=1
    jpeg.extend_from_slice(&[3, 0x11, 1]);
}

/// 写 DHT 标记
fn write_dht(jpeg: &mut Vec<u8>, table_id: u8, class: u8, bits: &[u8; 16], vals: &[u8]) {
    let length = 2 + 1 + 16 + vals.len() as u16;
    jpeg.extend_from_slice(&[0xFF, 0xC4]);
    jpeg.extend_from_slice(&length.to_be_bytes());
    jpeg.push((class << 4) | table_id);
    jpeg.extend_from_slice(bits);
    jpeg.extend_from_slice(vals);
}

/// 写 SOS 标记
fn write_sos(jpeg: &mut Vec<u8>) {
    jpeg.extend_from_slice(&[0xFF, 0xDA]);
    jpeg.extend_from_slice(&12u16.to_be_bytes()); // length
    jpeg.push(3); // 3 components
    jpeg.extend_from_slice(&[1, 0x00]); // Y: dc=0, ac=0
    jpeg.extend_from_slice(&[2, 0x11]); // Cb: dc=1, ac=1
    jpeg.extend_from_slice(&[3, 0x11]); // Cr: dc=1, ac=1
    jpeg.extend_from_slice(&[0, 63, 0]); // Ss=0, Se=63, AhAl=0
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
    bmp.extend_from_slice(&2835u32.to_le_bytes()); // x ppm
    bmp.extend_from_slice(&2835u32.to_le_bytes()); // y ppm
    bmp.extend_from_slice(&0u32.to_le_bytes()); // colors
    bmp.extend_from_slice(&0u32.to_le_bytes()); // important colors

    // Pixel data (bottom-up, BGR)
    for y in (0..height).rev() {
        let row = &rgb[y * width * 3..(y + 1) * width * 3];
        for x in 0..width {
            bmp.push(row[x * 3 + 2]); // B
            bmp.push(row[x * 3 + 1]); // G
            bmp.push(row[x * 3]);     // R
        }
        // padding
        let padding = row_size - width * 3;
        for _ in 0..padding {
            bmp.push(0);
        }
    }

    Ok(bmp)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_yuv_to_rgb() {
        let yuv = video_codec::YuvFrame::black(4, 4, 0);
        let rgb = yuv_to_rgb(&yuv, 4, 4);
        assert_eq!(rgb.len(), 4 * 4 * 3);
        // Y=0, U=128, V=128 → R=0, G=0, B=0 (black)
        assert_eq!(rgb[0], 0);
        assert_eq!(rgb[1], 0);
        assert_eq!(rgb[2], 0);
    }

    #[test]
    fn test_encode_png() {
        let rgb = vec![255, 0, 0, 0, 255, 0, 0, 0, 255, 255, 255, 255]; // 2x2
        let png = encode_png(&rgb, 2, 2).unwrap();
        // PNG signature
        assert_eq!(&png[0..8], &[137, 80, 78, 71, 13, 10, 26, 10]);
    }

    #[test]
    fn test_encode_bmp() {
        let rgb = vec![255, 0, 0]; // 1x1
        let bmp = encode_bmp(&rgb, 1, 1).unwrap();
        // BMP signature
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
    fn test_thumbnail_from_yuv_jpeg() {
        let yuv = video_codec::YuvFrame::black(16, 16, 0);
        let jpeg = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        assert!(!jpeg.is_empty());
        // JPEG SOI marker
        assert_eq!(&jpeg[0..2], &[0xFF, 0xD8]);
        // JPEG EOI marker
        assert_eq!(&jpeg[jpeg.len()-2..], &[0xFF, 0xD9]);
    }

    #[test]
    fn test_adler32() {
        let data = b"hello";
        let adler = adler32(data);
        // Known value for "hello"
        assert_eq!(adler, 0x062c0215);
    }

    #[test]
    fn test_crc32() {
        let data = b"IEND";
        let crc = crc32(data);
        // Known CRC for IEND
        assert_eq!(crc, 0xAE426082);
    }
}
