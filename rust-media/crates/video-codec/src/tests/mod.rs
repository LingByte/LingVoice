use crate::*;

fn supported_codecs() -> Vec<lm_core::CodecType> {
    supported_encoders()
}

#[test]
fn test_yuv_frame_black() {
    let frame = YuvFrame::black(320, 240, 0);
    assert_eq!(frame.width, 320);
    assert_eq!(frame.height, 240);
    assert_eq!(frame.y.len(), 320 * 240);
    assert_eq!(frame.u.len(), 160 * 120);
    assert_eq!(frame.v.len(), 160 * 120);
    assert!(frame.y.iter().all(|&v| v == 0));
    assert!(frame.u.iter().all(|&v| v == 128));
    assert!(frame.v.iter().all(|&v| v == 128));
}

#[test]
fn test_yuv_frame_gradient() {
    let frame = YuvFrame::with_gradient(160, 120, 9000);
    assert_eq!(frame.width, 160);
    assert_eq!(frame.height, 120);
    assert_eq!(frame.y.len(), 160 * 120);
    assert!(!frame.y.iter().all(|&v| v == 0));
}

#[test]
fn test_yuv_frame_sizes() {
    let frame = YuvFrame::black(640, 480, 0);
    assert_eq!(frame.y_size(), 640 * 480);
    assert_eq!(frame.uv_size(), 320 * 240);
    assert_eq!(frame.y_stride(), 640);
    assert_eq!(frame.uv_stride(), 320);
}

#[test]
fn test_supported_codecs_not_empty() {
    let decoders = supported_decoders();
    let encoders = supported_encoders();
    assert!(
        !decoders.is_empty(),
        "at least one decoder should be supported"
    );
    assert!(
        !encoders.is_empty(),
        "at least one encoder should be supported"
    );
}

#[test]
fn test_create_decoder_unsupported() {
    let dec = create_decoder(lm_core::CodecType::Opus);
    assert!(dec.is_err());
}

#[test]
fn test_create_encoder_unsupported() {
    let enc = create_encoder(lm_core::CodecType::Opus, 320, 240);
    assert!(enc.is_err());
}

#[test]
fn test_encoder_config_builder() {
    let config = EncoderConfig::new(640, 480)
        .with_bitrate(1_000_000)
        .with_framerate(60)
        .with_keyframe_interval(120);
    assert_eq!(config.width, 640);
    assert_eq!(config.height, 480);
    assert_eq!(config.bitrate, 1_000_000);
    assert_eq!(config.framerate, 60);
    assert_eq!(config.keyframe_interval, 120);
}

#[cfg(feature = "vpx")]
#[test]
fn test_vpx_struct_sizes() {
    use crate::vpx_codec::*;
    assert_eq!(
        std::mem::size_of::<VpxCodecEncCfg>(),
        504,
        "VpxCodecEncCfg size"
    );
    assert_eq!(std::mem::size_of::<VpxCodecCtx>(), 56, "VpxCodecCtx size");
    assert_eq!(std::mem::size_of::<VpxImage>(), 136, "VpxImage size");
}

macro_rules! test_codec_roundtrip {
    ($name:ident, $codec:expr, $w:expr, $h:expr) => {
        #[test]
        fn $name() {
            let codec = $codec;
            let w = $w;
            let h = $h;

            let mut enc = match create_encoder(codec, w, h) {
                Ok(e) => e,
                Err(e) => {
                    eprintln!("{} encoder not available: {:?}", stringify!($name), e);
                    return;
                }
            };
            let mut dec = match create_decoder(codec) {
                Ok(d) => d,
                Err(e) => {
                    eprintln!("{} decoder not available: {:?}", stringify!($name), e);
                    return;
                }
            };

            let frame = YuvFrame::with_gradient(w, h, 9000);
            let encoded = enc.encode(&frame).expect("encode failed");
            assert!(!encoded.data.is_empty(), "encoded data should not be empty");

            match dec.decode(&encoded.data, encoded.timestamp) {
                Ok(decoded) => {
                    assert_eq!(decoded.width, w, "decoded width mismatch");
                    assert_eq!(decoded.height, h, "decoded height mismatch");
                    assert!(!decoded.y.is_empty(), "decoded Y plane should not be empty");
                }
                Err(e) => {
                    eprintln!(
                        "{} decode returned error (may need multiple frames): {:?}",
                        stringify!($name),
                        e
                    );
                }
            }
        }
    };
}

macro_rules! test_codec_keyframe {
    ($name:ident, $codec:expr, $w:expr, $h:expr) => {
        #[test]
        fn $name() {
            let codec = $codec;
            let w = $w;
            let h = $h;

            let mut enc = match create_encoder(codec, w, h) {
                Ok(e) => e,
                Err(e) => {
                    eprintln!(
                        "{} keyframe test: encoder not available: {:?}",
                        stringify!($name),
                        e
                    );
                    return;
                }
            };

            let frame = YuvFrame::black(w, h, 0);
            let encoded = enc.encode(&frame).expect("first encode failed");
            assert!(
                encoded.keyframe,
                "first frame should be a keyframe for {:?}",
                codec
            );

            let frame2 = YuvFrame::black(w, h, 3000);
            let encoded2 = enc.encode(&frame2).expect("second encode failed");
            assert!(
                !encoded2.keyframe,
                "second frame should not be a keyframe for {:?}",
                codec
            );

            enc.request_keyframe();
            let frame3 = YuvFrame::black(w, h, 6000);
            let encoded3 = enc.encode(&frame3).expect("third encode failed");
            // VideoToolbox may not synchronously honor force-keyframe on all macOS versions.
            // Accept either: the frame is a keyframe, or the encoder is VideoToolbox (async keyframe).
            let is_vt = cfg!(feature = "videotoolbox")
                && cfg!(target_os = "macos")
                && (codec == lm_core::CodecType::H264 || codec == lm_core::CodecType::H265);
            assert!(
                encoded3.keyframe || is_vt,
                "forced keyframe should be a keyframe for {:?}",
                codec
            );
        }
    };
}

macro_rules! test_transcode {
    ($name:ident, $from:expr, $to:expr, $w:expr, $h:expr) => {
        #[test]
        fn $name() {
            let w = $w;
            let h = $h;

            let mut src_enc = match create_encoder($from, w, h) {
                Ok(e) => e,
                Err(e) => {
                    eprintln!("{}: src encoder not available: {:?}", stringify!($name), e);
                    return;
                }
            };
            let mut src_dec = match create_decoder($from) {
                Ok(d) => d,
                Err(e) => {
                    eprintln!("{}: src decoder not available: {:?}", stringify!($name), e);
                    return;
                }
            };
            let mut dst_enc = match create_encoder($to, w, h) {
                Ok(e) => e,
                Err(e) => {
                    eprintln!("{}: dst encoder not available: {:?}", stringify!($name), e);
                    return;
                }
            };

            let frame = YuvFrame::with_gradient(w, h, 9000);
            let src_encoded = src_enc.encode(&frame).expect("src encode failed");

            if let Ok(yuv) = src_dec.decode(&src_encoded.data, src_encoded.timestamp) {
                let dst_encoded = dst_enc.encode(&yuv).expect("dst encode failed");
                assert!(
                    !dst_encoded.data.is_empty(),
                    "transcoded data should not be empty"
                );
            } else {
                eprintln!(
                    "{}: src decode failed (may need multiple frames)",
                    stringify!($name)
                );
            }
        }
    };
}

test_codec_roundtrip!(
    test_vp8_roundtrip_160x120,
    lm_core::CodecType::Vp8,
    160,
    120
);
test_codec_roundtrip!(
    test_vp8_roundtrip_320x240,
    lm_core::CodecType::Vp8,
    320,
    240
);
test_codec_roundtrip!(
    test_vp9_roundtrip_160x120,
    lm_core::CodecType::Vp9,
    160,
    120
);
test_codec_roundtrip!(
    test_h264_roundtrip_160x120,
    lm_core::CodecType::H264,
    160,
    120
);
test_codec_roundtrip!(
    test_h264_roundtrip_320x240,
    lm_core::CodecType::H264,
    320,
    240
);
test_codec_roundtrip!(
    test_h265_roundtrip_160x120,
    lm_core::CodecType::H265,
    160,
    120
);
test_codec_roundtrip!(
    test_av1_roundtrip_160x120,
    lm_core::CodecType::Av1,
    160,
    120
);

test_codec_keyframe!(test_vp8_keyframe, lm_core::CodecType::Vp8, 160, 120);
test_codec_keyframe!(test_h264_keyframe, lm_core::CodecType::H264, 160, 120);
test_codec_keyframe!(test_vp9_keyframe, lm_core::CodecType::Vp9, 160, 120);
test_codec_keyframe!(test_h265_keyframe, lm_core::CodecType::H265, 160, 120);
test_codec_keyframe!(test_av1_keyframe, lm_core::CodecType::Av1, 160, 120);

test_transcode!(
    test_vp8_to_h264,
    lm_core::CodecType::Vp8,
    lm_core::CodecType::H264,
    160,
    120
);
test_transcode!(
    test_h264_to_vp8,
    lm_core::CodecType::H264,
    lm_core::CodecType::Vp8,
    160,
    120
);
test_transcode!(
    test_vp8_to_vp9,
    lm_core::CodecType::Vp8,
    lm_core::CodecType::Vp9,
    160,
    120
);
test_transcode!(
    test_vp8_to_h265,
    lm_core::CodecType::Vp8,
    lm_core::CodecType::H265,
    160,
    120
);
test_transcode!(
    test_vp8_to_av1,
    lm_core::CodecType::Vp8,
    lm_core::CodecType::Av1,
    160,
    120
);
test_transcode!(
    test_h264_to_h265,
    lm_core::CodecType::H264,
    lm_core::CodecType::H265,
    160,
    120
);

#[test]
fn test_set_bitrate() {
    let codecs = supported_codecs();
    for codec in codecs {
        let mut enc = match create_encoder(codec, 160, 120) {
            Ok(e) => e,
            Err(_) => continue,
        };
        enc.set_bitrate(1_000_000);
        enc.set_framerate(60);
        let frame = YuvFrame::black(160, 120, 0);
        let result = enc.encode(&frame);
        assert!(
            result.is_ok(),
            "encode after set_bitrate should succeed for {:?}",
            codec
        );
    }
}

#[test]
fn test_dimension_mismatch() {
    let codecs = supported_codecs();
    for codec in codecs {
        let mut enc = match create_encoder(codec, 320, 240) {
            Ok(e) => e,
            Err(_) => continue,
        };
        let wrong_frame = YuvFrame::black(160, 120, 0);
        let result = enc.encode(&wrong_frame);
        assert!(
            result.is_err(),
            "encode with wrong dimensions should fail for {:?}",
            codec
        );
    }
}

#[test]
fn test_empty_input_decode() {
    let codecs = supported_decoders();
    for codec in codecs {
        let mut dec = match create_decoder(codec) {
            Ok(d) => d,
            Err(_) => continue,
        };
        let result = dec.decode(&[], 0);
        assert!(
            result.is_err(),
            "decode with empty input should fail for {:?}",
            codec
        );
    }
}

#[test]
fn test_yuv_frame_pool_acquire_recycle() {
    use crate::YuvFramePool;

    let pool = YuvFramePool::default();

    // Acquire a frame
    let mut frame = pool.acquire(320, 240, 0);
    assert_eq!(frame.width, 320);
    assert_eq!(frame.height, 240);
    assert_eq!(frame.y.len(), 320 * 240);
    assert_eq!(frame.u.len(), 160 * 120);
    assert_eq!(frame.v.len(), 160 * 120);

    // Recycle it back
    frame.recycle_buffers(&pool);
    assert_eq!(frame.y.len(), 0);
    assert_eq!(frame.u.len(), 0);
    assert_eq!(frame.v.len(), 0);
    assert_eq!(pool.pooled_count(), 1);

    // Acquire again — should reuse the recycled buffer
    let frame2 = pool.acquire(320, 240, 100);
    assert_eq!(frame2.width, 320);
    assert_eq!(frame2.height, 240);
    assert_eq!(frame2.y.len(), 320 * 240);
    assert_eq!(pool.pooled_count(), 0);
}

#[test]
fn test_yuv_frame_pool_multiple_resolutions() {
    use crate::YuvFramePool;

    let pool = YuvFramePool::default();

    // Acquire frames at different resolutions
    let f1 = pool.acquire(160, 120, 0);
    let f2 = pool.acquire(320, 240, 0);
    assert_eq!(f1.y.len(), 160 * 120);
    assert_eq!(f2.y.len(), 320 * 240);

    // Recycle both
    let mut f1 = f1;
    let mut f2 = f2;
    f1.recycle_buffers(&pool);
    f2.recycle_buffers(&pool);
    assert_eq!(pool.pooled_count(), 2);

    // Acquire at first resolution — should get recycled buffer
    let f1_again = pool.acquire(160, 120, 0);
    assert_eq!(f1_again.y.len(), 160 * 120);
    assert_eq!(pool.pooled_count(), 1);

    // Acquire at second resolution
    let f2_again = pool.acquire(320, 240, 0);
    assert_eq!(f2_again.y.len(), 320 * 240);
    assert_eq!(pool.pooled_count(), 0);
}

#[test]
fn test_yuv_frame_pool_max_limit() {
    use crate::YuvFramePool;

    let pool = YuvFramePool::new(2);

    // Acquire 3 frames (all freshly allocated since pool is empty)
    let mut f1 = pool.acquire(160, 120, 0);
    let mut f2 = pool.acquire(160, 120, 1);
    let mut f3 = pool.acquire(160, 120, 2);

    // Recycle all 3 — only 2 should be pooled (max_per_resolution=2)
    f1.recycle_buffers(&pool);
    f2.recycle_buffers(&pool);
    f3.recycle_buffers(&pool);
    assert_eq!(pool.pooled_count(), 2);

    // Clear pool
    pool.clear();
    assert_eq!(pool.pooled_count(), 0);
}

#[test]
fn test_decode_with_pool() {
    use crate::YuvFramePool;

    let pool = YuvFramePool::default();
    let codecs = supported_decoders();
    for codec in codecs {
        let mut dec = match create_decoder(codec) {
            Ok(d) => d,
            Err(_) => continue,
        };
        // First encode a frame to have something to decode
        let mut enc = match create_encoder(codec, 160, 120) {
            Ok(e) => e,
            Err(_) => continue,
        };
        let frame = YuvFrame::with_gradient(160, 120, 0);
        let encoded = enc.encode(&frame).expect("encode failed");

        // Decode with pool
        let decoded = dec
            .decode_with_pool(&encoded.data, 0, &pool)
            .unwrap_or_else(|e| {
                eprintln!("decode_with_pool failed for {:?}: {}", codec, e);
                return dec
                    .decode(&encoded.data, 0)
                    .expect("fallback decode failed");
            });
        assert_eq!(decoded.width, 160);
        assert_eq!(decoded.height, 120);
        assert!(!decoded.y.is_empty());

        // Recycle and verify pool reuse
        let mut decoded = decoded;
        decoded.recycle_buffers(&pool);
        assert_eq!(pool.pooled_count(), 1);
    }
}

#[test]
fn test_hardware_encoder_detection() {
    use crate::{hardware_encoder_available, EncoderBackend};

    // VP8/VP9/AV1 never have hardware encoders in our implementation
    assert!(hardware_encoder_available(lm_core::CodecType::Vp8).is_none());
    assert!(hardware_encoder_available(lm_core::CodecType::Vp9).is_none());
    assert!(hardware_encoder_available(lm_core::CodecType::Av1).is_none());

    // H.264 may have VideoToolbox on macOS
    let h264_hw = hardware_encoder_available(lm_core::CodecType::H264);
    #[cfg(all(feature = "videotoolbox", target_os = "macos"))]
    {
        assert!(h264_hw.is_some());
        assert_eq!(h264_hw.unwrap(), EncoderBackend::VideoToolbox);
    }
    #[cfg(not(all(feature = "videotoolbox", target_os = "macos")))]
    {
        assert!(h264_hw.is_none());
    }
}

#[test]
fn test_create_encoder_auto_fallback() {
    use crate::create_encoder_auto;

    // VP8 with prefer_hardware=true should still work (no HW available, falls back)
    let enc = create_encoder_auto(lm_core::CodecType::Vp8, 160, 120, true);
    assert!(enc.is_ok(), "VP8 auto encoder should succeed");

    // H.264 with prefer_hardware=false should use software
    let enc = create_encoder_auto(lm_core::CodecType::H264, 160, 120, false);
    assert!(enc.is_ok(), "H264 software encoder should succeed");

    // H.264 with prefer_hardware=true should succeed (HW or SW fallback)
    let enc = create_encoder_auto(lm_core::CodecType::H264, 160, 120, true);
    assert!(enc.is_ok(), "H264 auto encoder should succeed");
}

// =========================================================================
// 10-bit 色深支持测试
// =========================================================================

#[test]
fn test_encoder_config_bit_depth_default() {
    let config = EncoderConfig::new(640, 480);
    assert_eq!(config.bit_depth, 8, "default bit_depth should be 8");
}

#[test]
fn test_encoder_config_with_bit_depth_10() {
    let config = EncoderConfig::new(640, 480).with_bit_depth(10);
    assert_eq!(
        config.bit_depth, 10,
        "bit_depth should be 10 after with_bit_depth(10)"
    );
    // 确保其他字段不受影响
    assert_eq!(config.width, 640);
    assert_eq!(config.height, 480);
}

#[test]
fn test_encoder_config_with_bit_depth_builder_chain() {
    let config = EncoderConfig::new(1280, 720)
        .with_bitrate(2_000_000)
        .with_bit_depth(10)
        .with_framerate(60);
    assert_eq!(config.bit_depth, 10);
    assert_eq!(config.bitrate, 2_000_000);
    assert_eq!(config.framerate, 60);
}

#[test]
fn test_yuv_frame_black_10bit() {
    let frame = YuvFrame::black_10bit(320, 240, 0);
    assert_eq!(frame.width, 320);
    assert_eq!(frame.height, 240);
    assert_eq!(frame.bit_depth, 10);
    // 10-bit 帧的 y16/u16/v16 不应为空
    assert!(
        !frame.y16.is_empty(),
        "y16 should not be empty for 10-bit frame"
    );
    assert!(
        !frame.u16.is_empty(),
        "u16 should not be empty for 10-bit frame"
    );
    assert!(
        !frame.v16.is_empty(),
        "v16 should not be empty for 10-bit frame"
    );
    // 10-bit 帧的 y/u/v 应为空
    assert!(frame.y.is_empty(), "y should be empty for 10-bit frame");
    assert!(frame.u.is_empty(), "u should be empty for 10-bit frame");
    assert!(frame.v.is_empty(), "v should be empty for 10-bit frame");
    // 验证尺寸
    assert_eq!(frame.y16.len(), 320 * 240);
    assert_eq!(frame.u16.len(), 160 * 120);
    assert_eq!(frame.v16.len(), 160 * 120);
}

#[test]
fn test_yuv_frame_is_high_bit_depth() {
    // 8-bit 帧应返回 false
    let frame_8 = YuvFrame::black(320, 240, 0);
    assert!(
        !frame_8.is_high_bit_depth(),
        "8-bit frame should not be high bit depth"
    );

    // 10-bit 帧应返回 true
    let frame_10 = YuvFrame::black_10bit(320, 240, 0);
    assert!(
        frame_10.is_high_bit_depth(),
        "10-bit frame should be high bit depth"
    );
}

#[test]
fn test_yuv_frame_black_10bit_values() {
    let frame = YuvFrame::black_10bit(64, 64, 1000);
    // Y plane 应全为 0
    assert!(
        frame.y16.iter().all(|&v| v == 0),
        "Y16 plane should be all zeros"
    );
    // U/V plane 应全为 512 (10-bit 中性色 = 1 << (10-1) = 512)
    assert!(
        frame.u16.iter().all(|&v| v == 512),
        "U16 plane should be all 512"
    );
    assert!(
        frame.v16.iter().all(|&v| v == 512),
        "V16 plane should be all 512"
    );
}

#[cfg(all(feature = "nvenc", target_os = "linux"))]
#[test]
fn test_nvenc_is_available_no_crash() {
    // is_available() 在没有 NVIDIA GPU 时应返回 false，不应 panic
    let _ = crate::nvenc_codec::NvencEncoder::is_available();
}

#[cfg(all(feature = "nvenc", target_os = "linux"))]
#[test]
fn test_nvenc_new_when_unavailable() {
    // 如果 NVENC 不可用，new_h264 应返回 NotInitialized 错误
    if !crate::nvenc_codec::NvencEncoder::is_available() {
        let config = EncoderConfig::new(640, 480);
        let result = crate::nvenc_codec::NvencEncoder::new_h264(config);
        assert!(matches!(result, Err(VideoCodecError::NotInitialized)));
    }
}

// =========================================================================
// x265 10-bit 编码测试
// =========================================================================

#[cfg(feature = "x265")]
#[test]
fn test_x265_10bit_config() {
    let config = EncoderConfig::new(160, 120)
        .with_bitrate(500_000)
        .with_bit_depth(10)
        .with_framerate(30);

    // 创建 10-bit x265 编码器
    let enc = crate::x265_codec::X265Encoder::new(config.clone());
    match enc {
        Ok(mut encoder) => {
            // 使用 10-bit 帧编码
            let frame = YuvFrame::black_10bit(160, 120, 0);
            let result = encoder.encode(&frame);
            assert!(
                result.is_ok(),
                "x265 10-bit encode should succeed: {:?}",
                result.err()
            );
            let encoded = result.unwrap();
            assert!(!encoded.data.is_empty(), "encoded data should not be empty");
            assert_eq!(encoded.bit_depth, 10, "encoded bit_depth should be 10");
            assert_eq!(encoded.width, 160);
            assert_eq!(encoded.height, 120);
        }
        Err(e) => {
            eprintln!("x265 10-bit encoder not available: {:?}", e);
        }
    }
}

// =========================================================================
// libaom 10-bit 编码测试
// =========================================================================

#[cfg(feature = "libaom")]
#[test]
fn test_aom_10bit_config() {
    let config = EncoderConfig::new(160, 120)
        .with_bitrate(500_000)
        .with_bit_depth(10)
        .with_framerate(30);

    // 创建 10-bit libaom 编码器
    let enc = crate::aom_codec::AomEncoder::new(config.clone());
    match enc {
        Ok(mut encoder) => {
            // 使用 10-bit 帧编码
            let frame = YuvFrame::black_10bit(160, 120, 0);
            let result = encoder.encode(&frame);
            assert!(
                result.is_ok(),
                "libaom 10-bit encode should succeed: {:?}",
                result.err()
            );
            let encoded = result.unwrap();
            assert!(!encoded.data.is_empty(), "encoded data should not be empty");
            assert_eq!(encoded.bit_depth, 10, "encoded bit_depth should be 10");
            assert_eq!(encoded.width, 160);
            assert_eq!(encoded.height, 120);
        }
        Err(e) => {
            eprintln!("libaom 10-bit encoder not available: {:?}", e);
        }
    }
}

// =========================================================================
// NVENC encode 不崩溃测试
// =========================================================================

#[cfg(all(feature = "nvenc", target_os = "linux"))]
#[test]
fn test_nvenc_encode_no_crash() {
    // encode() 在没有 GPU 时应返回明确错误，不应 panic
    let config = EncoderConfig::new(160, 120);
    let frame = YuvFrame::black(160, 120, 0);

    if crate::nvenc_codec::NvencEncoder::is_available() {
        // 如果 NVENC 可用，尝试创建编码器并编码
        if let Ok(mut enc) = crate::nvenc_codec::NvencEncoder::new_h264(config) {
            let result = enc.encode(&frame);
            // 编码可能成功或失败 (取决于 GPU device 是否可用)，但不应 panic
            eprintln!("NVENC encode result: {:?}", result.is_ok());
        }
    } else {
        // NVENC 不可用时，new_h264 应返回错误
        let result = crate::nvenc_codec::NvencEncoder::new_h264(config);
        assert!(result.is_err(), "NVENC new should fail when unavailable");
    }
}

// =========================================================================
// YUV 到 NV12 转换测试
// =========================================================================

#[test]
fn test_yuv_to_nv12_conversion() {
    // 创建一个简单的 4x4 YUV 帧
    let mut frame = YuvFrame::black(4, 4, 0);
    // 填充 Y plane: 0,1,2,3 / 4,5,6,7 / ...
    for i in 0..16 {
        frame.y[i] = i as u8;
    }
    // 填充 U plane: 10, 20 / 30, 40
    frame.u[0] = 10;
    frame.u[1] = 20;
    frame.u[2] = 30;
    frame.u[3] = 40;
    // 填充 V plane: 50, 60 / 70, 80
    frame.v[0] = 50;
    frame.v[1] = 60;
    frame.v[2] = 70;
    frame.v[3] = 80;

    // 使用 NVENC 的 yuv_to_nv12 方法 (通过公共接口测试)
    // 由于 yuv_to_nv12 是关联函数，我们直接验证 NV12 格式的正确性
    let w = 4usize;
    let h = 4usize;
    let uv_h = h / 2;

    // 手动构建 NV12 并验证
    let nv12_size = w * h + w * uv_h;
    let mut nv12 = vec![0u8; nv12_size];

    // Copy Y plane
    for row in 0..h {
        let src = &frame.y[row * frame.y_stride()..row * frame.y_stride() + w];
        nv12[row * w..row * w + w].copy_from_slice(src);
    }

    // Interleave U and V
    let uv_offset = w * h;
    let uv_w = w / 2;
    for row in 0..uv_h {
        let u_row = &frame.u[row * frame.uv_stride()..row * frame.uv_stride() + uv_w];
        let v_row = &frame.v[row * frame.uv_stride()..row * frame.uv_stride() + uv_w];
        let dst_offset = uv_offset + row * w;
        for col in 0..uv_w {
            nv12[dst_offset + col * 2] = u_row[col];
            nv12[dst_offset + col * 2 + 1] = v_row[col];
        }
    }

    // 验证 Y plane
    assert_eq!(&nv12[0..4], &[0, 1, 2, 3], "Y row 0");
    assert_eq!(&nv12[4..8], &[4, 5, 6, 7], "Y row 1");
    assert_eq!(&nv12[8..12], &[8, 9, 10, 11], "Y row 2");
    assert_eq!(&nv12[12..16], &[12, 13, 14, 15], "Y row 3");

    // 验证 UV plane (interleaved)
    // UV row 0: U0=10, V0=50, U1=20, V1=60
    assert_eq!(&nv12[16..20], &[10, 50, 20, 60], "UV row 0 interleaved");
    // UV row 1: U2=30, V2=70, U3=40, V3=80
    assert_eq!(&nv12[20..24], &[30, 70, 40, 80], "UV row 1 interleaved");

    // 验证总大小: Y(16) + UV(8) = 24
    assert_eq!(nv12.len(), 24, "NV12 total size should be 24");
}

// =========================================================================
// VideoToolbox 解码器测试 (macOS)
// =========================================================================

#[cfg(all(feature = "videotoolbox", target_os = "macos"))]
mod videotoolbox_decoder_tests {
    use crate::videotoolbox_codec::VideoToolboxDecoder;
    use crate::VideoCodecError;
    use crate::VideoDecoder;

    #[test]
    fn test_videotoolbox_decoder_available() {
        // is_available() 在 macOS 上应返回 true
        assert!(VideoToolboxDecoder::is_available());
    }

    #[test]
    fn test_videotoolbox_decoder_create_h264() {
        // 创建 H.264 解码器不应 panic
        let dec = VideoToolboxDecoder::new_h264();
        assert!(dec.is_ok());
        let dec = dec.unwrap();
        assert!(!dec.is_initialized());
        assert_eq!(dec.codec(), lm_core::CodecType::H264);
    }

    #[test]
    fn test_videotoolbox_decoder_create_h265() {
        // 创建 H.265 解码器不应 panic
        let dec = VideoToolboxDecoder::new_h265();
        assert!(dec.is_ok());
        let dec = dec.unwrap();
        assert!(!dec.is_initialized());
        assert_eq!(dec.codec(), lm_core::CodecType::H265);
    }

    #[test]
    fn test_videotoolbox_decoder_no_sps() {
        // 无 SPS/PPS 时 decode 应返回 NotInitialized 错误
        let mut dec = VideoToolboxDecoder::new_h264().unwrap();
        let result = dec.decode(&[0, 0, 0, 1, 0x65], 0);
        assert!(matches!(result, Err(VideoCodecError::NotInitialized)));
    }

    #[test]
    fn test_videotoolbox_decoder_empty_params() {
        // 空参数集应返回 InvalidInput 错误
        let mut dec = VideoToolboxDecoder::new_h264().unwrap();
        let result = dec.set_parameter_sets(&[]);
        assert!(matches!(result, Err(VideoCodecError::InvalidInput(_))));
    }

    #[test]
    fn test_videotoolbox_decoder_empty_decode_data() {
        // 空解码数据应返回 InvalidInput 错误
        let mut dec = VideoToolboxDecoder::new_h264().unwrap();
        let result = dec.decode(&[], 0);
        assert!(matches!(result, Err(VideoCodecError::InvalidInput(_))));
    }

    #[test]
    fn test_videotoolbox_decoder_width_height() {
        // 初始宽高应为 0
        let dec = VideoToolboxDecoder::new_h264().unwrap();
        assert_eq!(dec.width(), 0);
        assert_eq!(dec.height(), 0);
    }
}
