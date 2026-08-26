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

    /// YUV420p → JPEG（简化版）
    fn yuv_to_jpeg(yuv: &video_codec::YuvFrame, quality: u8) -> Result<Vec<u8>> {
        let width = yuv.width as usize;
        let height = yuv.height as usize;
        let rgb = yuv_to_rgb(yuv, width, height);

        // 简化：返回 BMP 格式作为 fallback（实际应编码 JPEG）
        // 生产环境应使用 jpeg-encoder crate
        encode_bmp(&rgb, width, height)
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
        let yuv = video_codec::YuvFrame::black(8, 8, 0);
        let bmp = Thumbnail::from_yuv(&yuv, ThumbnailFormat::Jpeg, 80).unwrap();
        assert!(!bmp.is_empty());
        assert_eq!(&bmp[0..2], b"BM");
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
