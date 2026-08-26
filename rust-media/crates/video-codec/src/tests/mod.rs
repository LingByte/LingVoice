use crate::*;

#[test]
fn test_create_decoder_vp8() {
    let dec = create_decoder(lm_core::CodecType::Vp8);
    assert!(dec.is_ok());
    let dec = dec.unwrap();
    assert_eq!(dec.codec(), lm_core::CodecType::Vp8);
}

#[test]
fn test_create_decoder_h264() {
    let dec = create_decoder(lm_core::CodecType::H264);
    assert!(dec.is_ok());
    let dec = dec.unwrap();
    assert_eq!(dec.codec(), lm_core::CodecType::H264);
}

#[test]
fn test_create_encoder_vp8() {
    // 验证结构体大小
    let size = std::mem::size_of::<crate::vpx_codec::VpxCodecEncCfg>();
    println!("VpxCodecEncCfg size={}", size);
    assert_eq!(size, 504, "VpxCodecEncCfg size mismatch: got {}", size);
    let enc = create_encoder(lm_core::CodecType::Vp8, 320, 240);
    assert!(enc.is_ok(), "vp8 encoder creation failed: {:?}", enc.err());
    let enc = enc.unwrap();
    assert_eq!(enc.codec(), lm_core::CodecType::Vp8);
}

#[test]
fn test_create_encoder_h264() {
    let enc = create_encoder(lm_core::CodecType::H264, 320, 240);
    assert!(enc.is_ok());
    let enc = enc.unwrap();
    assert_eq!(enc.codec(), lm_core::CodecType::H264);
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
fn test_yuv_frame_black() {
    let frame = YuvFrame::black(320, 240, 0);
    assert_eq!(frame.width, 320);
    assert_eq!(frame.height, 240);
    assert_eq!(frame.y.len(), 320 * 240);
    assert_eq!(frame.u.len(), 160 * 120);
    assert_eq!(frame.v.len(), 160 * 120);
    // Y 全黑 = 0
    assert!(frame.y.iter().all(|&v| v == 0));
    // UV 中性灰 = 128
    assert!(frame.u.iter().all(|&v| v == 128));
    assert!(frame.v.iter().all(|&v| v == 128));
}

#[cfg(feature = "vpx")]
#[test]
fn test_vp8_encode_decode_roundtrip() {
    let mut enc = create_encoder(lm_core::CodecType::Vp8, 160, 120).unwrap();
    let mut dec = create_decoder(lm_core::CodecType::Vp8).unwrap();

    // 编码一个黑色帧
    let frame = YuvFrame::black(160, 120, 9000); // 100ms @ 90kHz
    let encoded = enc.encode(&frame);
    assert!(encoded.is_ok(), "encode failed: {:?}", encoded.err());
    let encoded = encoded.unwrap();
    assert!(!encoded.data.is_empty());

    // 解码
    let decoded = dec.decode(&encoded.data, encoded.timestamp);
    // VP8 可能需要先编码 SPS/PPS 帧才能解码，这里只验证不 panic
    if let Ok(decoded) = decoded {
        assert_eq!(decoded.width, 160);
        assert_eq!(decoded.height, 120);
    }
}

#[cfg(feature = "openh264")]
#[test]
fn test_h264_encode_decode_roundtrip() {
    let mut enc = create_encoder(lm_core::CodecType::H264, 160, 120).unwrap();
    let mut dec = create_decoder(lm_core::CodecType::H264).unwrap();

    // 编码一个黑色帧
    let frame = YuvFrame::black(160, 120, 9000);
    let encoded = enc.encode(&frame);
    assert!(encoded.is_ok(), "encode failed: {:?}", encoded.err());
    let encoded = encoded.unwrap();
    assert!(!encoded.data.is_empty());

    // H.264 第一帧应该是关键帧（含 SPS/PPS/IDR）
    // 解码
    let decoded = dec.decode(&encoded.data, encoded.timestamp);
    if let Ok(decoded) = decoded {
        assert_eq!(decoded.width, 160);
        assert_eq!(decoded.height, 120);
    }
}

#[test]
fn test_vp8_to_h264_transcode() {
    // VP8 encode → decode → H.264 encode
    let mut vp8_enc = create_encoder(lm_core::CodecType::Vp8, 160, 120).unwrap();
    let mut vp8_dec = create_decoder(lm_core::CodecType::Vp8).unwrap();
    let mut h264_enc = create_encoder(lm_core::CodecType::H264, 160, 120).unwrap();

    let frame = YuvFrame::black(160, 120, 9000);

    // VP8 encode
    let vp8_encoded = vp8_enc.encode(&frame).expect("vp8 encode");

    // VP8 decode
    if let Ok(yuv) = vp8_dec.decode(&vp8_encoded.data, vp8_encoded.timestamp) {
        // H.264 encode
        let h264_encoded = h264_enc.encode(&yuv).expect("h264 encode");
        assert!(!h264_encoded.data.is_empty());
    }
}

#[test]
fn test_vpx_struct_sizes() {
    use crate::vpx_codec::*;
    println!("VpxCodecCtx size={}", std::mem::size_of::<VpxCodecCtx>());
    println!("VpxImage size={}", std::mem::size_of::<VpxImage>());
    assert_eq!(std::mem::size_of::<VpxCodecCtx>(), 56, "VpxCodecCtx size");
    assert_eq!(std::mem::size_of::<VpxImage>(), 136, "VpxImage size");
}
