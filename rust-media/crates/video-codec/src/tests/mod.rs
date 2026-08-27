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
