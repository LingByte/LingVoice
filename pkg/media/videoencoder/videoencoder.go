// Package videoencoder provides VP8/H.264 video decode/encode via CGO + FFmpeg.
//
// 架构：
//   encoded frame (H.264 NALU / VP8 payload)
//     → avcodec_send_packet
//     → avcodec_receive_frame (YUV420p)
//     → avcodec_send_frame
//     → avcodec_receive_packet (target codec)
//
// 用途：
//   - 协议转换（RTMP H.264 → WebRTC VP8）
//   - 录制转码（H.264 → VP8 for WebM）
//   - 缩略图生成（decode → PNG）
package videoencoder

/*
#cgo pkg-config: libavcodec libavutil libavformat
#include <libavcodec/avcodec.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <libavutil/frame.h>
#include <libavutil/pixfmt.h>
#include <stdlib.h>
#include <string.h>

// 辅助函数：创建解码器
AVCodecContext* create_decoder(int codec_id) {
    const AVCodec* codec = avcodec_find_decoder(codec_id);
    if (!codec) return NULL;
    AVCodecContext* ctx = avcodec_alloc_context3(codec);
    if (!ctx) return NULL;
    if (avcodec_open2(ctx, codec, NULL) < 0) {
        avcodec_free_context(&ctx);
        return NULL;
    }
    return ctx;
}

// 辅助函数：创建编码器
AVCodecContext* create_encoder(int codec_id, int width, int height, int bitrate) {
    const AVCodec* codec = avcodec_find_encoder(codec_id);
    if (!codec) return NULL;
    AVCodecContext* ctx = avcodec_alloc_context3(codec);
    if (!ctx) return NULL;
    ctx->width = width;
    ctx->height = height;
    ctx->bit_rate = bitrate;
    ctx->time_base = (AVRational){1, 90000}; // 90kHz RTP 时钟
    ctx->framerate = (AVRational){30, 1};
    ctx->gop_size = 300;
    ctx->max_b_frames = 0; // 零延迟
    ctx->pix_fmt = AV_PIX_FMT_YUV420P;
    if (codec_id == AV_CODEC_ID_H264) {
        av_opt_set(ctx->priv_data, "preset", "ultrafast", 0);
        av_opt_set(ctx->priv_data, "tune", "zerolatency", 0);
    }
    if (avcodec_open2(ctx, codec, NULL) < 0) {
        avcodec_free_context(&ctx);
        return NULL;
    }
    return ctx;
}

// 辅助函数：解码一个包
// 返回：>0 = 成功，frame 中有 YUV420p 数据；0 = 需要更多包；<0 = 错误
int decode_packet(AVCodecContext* ctx, const uint8_t* data, int size, AVFrame* frame) {
    AVPacket pkt;
    memset(&pkt, 0, sizeof(pkt));
    pkt.data = (uint8_t*)data;
    pkt.size = size;
    int ret = avcodec_send_packet(ctx, &pkt);
    if (ret < 0) return ret;
    ret = avcodec_receive_frame(ctx, frame);
    return ret; // 0 = got frame, AVERROR(EAGAIN) = need more, <0 = error
}

// 辅助函数：编码一帧
// 返回写入 out 的字节数，<0 = 错误
int encode_frame(AVCodecContext* ctx, AVFrame* frame, uint8_t* out, int out_size) {
    int ret = avcodec_send_frame(ctx, frame);
    if (ret < 0) return ret;
    AVPacket pkt;
    memset(&pkt, 0, sizeof(pkt));
    ret = avcodec_receive_packet(ctx, &pkt);
    if (ret < 0) return ret;
    int n = pkt.size;
    if (n > out_size) n = out_size;
    memcpy(out, pkt.data, n);
    av_packet_unref(&pkt);
    return n;
}

// 辅助函数：创建 YUV420p 帧
AVFrame* create_yuv_frame(int width, int height) {
    AVFrame* frame = av_frame_alloc();
    if (!frame) return NULL;
    frame->format = AV_PIX_FMT_YUV420P;
    frame->width = width;
    frame->height = height;
    if (av_frame_get_buffer(frame, 32) < 0) {
        av_frame_free(&frame);
        return NULL;
    }
    return frame;
}

// 辅助函数：填充 YUV 数据
void fill_yuv_data(AVFrame* frame, const uint8_t* y, const uint8_t* u, const uint8_t* v,
                   int width, int height) {
    av_frame_make_writable(frame);
    // Y 平面
    for (int i = 0; i < height; i++) {
        memcpy(frame->data[0] + i * frame->linesize[0], y + i * width, width);
    }
    // U/V 平面
    int uv_w = width / 2;
    int uv_h = height / 2;
    for (int i = 0; i < uv_h; i++) {
        memcpy(frame->data[1] + i * frame->linesize[1], u + i * uv_w, uv_w);
        memcpy(frame->data[2] + i * frame->linesize[2], v + i * uv_w, uv_w);
    }
}

// 辅助函数：获取帧的 YUV 数据
void get_yuv_data(AVFrame* frame, uint8_t* y, uint8_t* u, uint8_t* v,
                  int width, int height) {
    for (int i = 0; i < height; i++) {
        memcpy(y + i * width, frame->data[0] + i * frame->linesize[0], width);
    }
    int uv_w = width / 2;
    int uv_h = height / 2;
    for (int i = 0; i < uv_h; i++) {
        memcpy(u + i * uv_w, frame->data[1] + i * frame->linesize[1], uv_w);
        memcpy(v + i * uv_w, frame->data[2] + i * frame->linesize[2], uv_w);
    }
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Codec 类型
type Codec int

const (
	CodecH264 Codec = iota
	CodecVP8
)

// 对应 FFmpeg 的 codec_id
func (c Codec) cID() C.int {
	switch c {
	case CodecH264:
		return C.AV_CODEC_ID_H264
	case CodecVP8:
		return C.AV_CODEC_ID_VP8
	default:
		return -1
	}
}

func (c Codec) String() string {
	switch c {
	case CodecH264:
		return "h264"
	case CodecVP8:
		return "vp8"
	default:
		return "unknown"
	}
}

// YuvFrame YUV420p 原始帧
type YuvFrame struct {
	Y        []byte
	U        []byte
	V        []byte
	Width    int
	Height   int
	Timestamp uint64
	Keyframe bool
}

// EncodedFrame 编码后的视频帧
type EncodedFrame struct {
	Data      []byte
	Width     int
	Height    int
	Keyframe  bool
	Timestamp uint64
}

// Decoder 视频解码器
type Decoder struct {
	ctx   *C.AVCodecContext
	frame *C.AVFrame
	codec Codec
}

// NewDecoder 创建视频解码器
func NewDecoder(codec Codec) (*Decoder, error) {
	cID := codec.cID()
	if cID < 0 {
		return nil, fmt.Errorf("unsupported codec: %s", codec)
	}
	ctx := C.create_decoder(cID)
	if ctx == nil {
		return nil, fmt.Errorf("failed to create %s decoder", codec)
	}
	frame := C.av_frame_alloc()
	if frame == nil {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("failed to allocate frame")
	}
	return &Decoder{ctx: ctx, frame: frame, codec: codec}, nil
}

// Decode 解码一个完整的编码帧 → YUV420p
func (d *Decoder) Decode(data []byte, timestamp uint64) (*YuvFrame, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}
	ret := C.decode_packet(d.ctx, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.int(len(data)), d.frame)
	if ret < 0 {
		return nil, fmt.Errorf("decode failed: %d", ret)
	}
	if ret != 0 {
		// EAGAIN = 需要更多包
		return nil, nil
	}

	width := int(d.frame.width)
	height := int(d.frame.height)
	ySize := width * height
	uvW := width / 2
	uvH := height / 2
	uvSize := uvW * uvH

	y := make([]byte, ySize)
	u := make([]byte, uvSize)
	v := make([]byte, uvSize)

	C.get_yuv_data(d.frame,
		(*C.uint8_t)(unsafe.Pointer(&y[0])),
		(*C.uint8_t)(unsafe.Pointer(&u[0])),
		(*C.uint8_t)(unsafe.Pointer(&v[0])),
		C.int(width), C.int(height))

	return &YuvFrame{
		Y: y, U: u, V: v,
		Width: width, Height: height,
		Timestamp: timestamp,
	}, nil
}

// Close 释放解码器
func (d *Decoder) Close() {
	if d.frame != nil {
		C.av_frame_free(&d.frame)
	}
	if d.ctx != nil {
		C.avcodec_free_context(&d.ctx)
	}
}

// Codec 返回编解码类型
func (d *Decoder) Codec() Codec { return d.codec }

// Encoder 视频编码器
type Encoder struct {
	ctx       *C.AVCodecContext
	frame     *C.AVFrame
	codec     Codec
	width     int
	height    int
	forceKF   bool
}

// NewEncoder 创建视频编码器
func NewEncoder(codec Codec, width, height int) (*Encoder, error) {
	cID := codec.cID()
	if cID < 0 {
		return nil, fmt.Errorf("unsupported codec: %s", codec)
	}
	ctx := C.create_encoder(cID, C.int(width), C.int(height), 500000)
	if ctx == nil {
		return nil, fmt.Errorf("failed to create %s encoder", codec)
	}
	frame := C.create_yuv_frame(C.int(width), C.int(height))
	if frame == nil {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("failed to allocate frame")
	}
	return &Encoder{
		ctx: ctx, frame: frame, codec: codec,
		width: width, height: height,
	}, nil
}

// Encode 编码一帧 YUV420p → 编码帧
func (e *Encoder) Encode(frame *YuvFrame) (*EncodedFrame, error) {
	if frame.Width != e.width || frame.Height != e.height {
		return nil, fmt.Errorf("dimension mismatch: got %dx%d, want %dx%d",
			frame.Width, frame.Height, e.width, e.height)
	}

	// 填充 YUV 数据到 AVFrame
	C.fill_yuv_data(e.frame,
		(*C.uint8_t)(unsafe.Pointer(&frame.Y[0])),
		(*C.uint8_t)(unsafe.Pointer(&frame.U[0])),
		(*C.uint8_t)(unsafe.Pointer(&frame.V[0])),
		C.int(frame.Width), C.int(frame.Height))

	e.frame.pts = C.int64_t(frame.Timestamp)

	if e.forceKF {
		e.frame.flags |= C.AV_FRAME_FLAG_KEY
		e.forceKF = false
	} else {
		e.frame.flags &^= C.AV_FRAME_FLAG_KEY
	}

	// 编码（输出缓冲区足够大）
	outBuf := make([]byte, frame.Width*frame.Height*4)
	n := C.encode_frame(e.ctx, e.frame,
		(*C.uint8_t)(unsafe.Pointer(&outBuf[0])),
		C.int(len(outBuf)))
	if n < 0 {
		return nil, fmt.Errorf("encode failed: %d", n)
	}

	result := &EncodedFrame{
		Data:      outBuf[:int(n)],
		Width:     e.width,
		Height:    e.height,
		Keyframe:  (e.frame.flags & C.AV_FRAME_FLAG_KEY) != 0,
		Timestamp: frame.Timestamp,
	}
	return result, nil
}

// RequestKeyframe 请求下一帧为关键帧
func (e *Encoder) RequestKeyframe() {
	e.forceKF = true
}

// SetBitrate 设置目标码率
func (e *Encoder) SetBitrate(bps int) {
	e.ctx.bit_rate = C.int64_t(bps)
}

// Close 释放编码器
func (e *Encoder) Close() {
	if e.frame != nil {
		C.av_frame_free(&e.frame)
	}
	if e.ctx != nil {
		C.avcodec_free_context(&e.ctx)
	}
}

// Codec 返回编解码类型
func (e *Encoder) Codec() Codec { return e.codec }

// Transcode 转码一个完整的编码帧（便捷函数）
func Transcode(data []byte, srcCodec, dstCodec Codec, timestamp uint64) (*EncodedFrame, error) {
	dec, err := NewDecoder(srcCodec)
	if err != nil {
		return nil, fmt.Errorf("decoder: %w", err)
	}
	defer dec.Close()

	yuv, err := dec.Decode(data, timestamp)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if yuv == nil {
		return nil, errors.New("no output frame yet")
	}

	enc, err := NewEncoder(dstCodec, yuv.Width, yuv.Height)
	if err != nil {
		return nil, fmt.Errorf("encoder: %w", err)
	}
	defer enc.Close()

	return enc.Encode(yuv)
}
