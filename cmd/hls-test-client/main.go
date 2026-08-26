// hls-test-client: 端到端测试 WebRTC → HLS 转封装
//
// 1. 通过 gRPC 创建 session + video track
// 2. 推送模拟 VP8 RTP 包（关键帧 + P 帧）
// 3. 调用 HTTP /hls/{stream_id}/start 启动 HLS 转封装
// 4. 等待几秒后拉取 playlist.m3u8 验证
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/LingByte/LingVoice/proto/media/v1"
)

func main() {
	grpcAddr := flag.String("grpc", "127.0.0.1:50051", "Rust media-node gRPC 地址")
	httpAddr := flag.String("http", "127.0.0.1:8082", "Rust media-node HTTP 地址")
	sessionID := flag.String("session", "hls-test-session", "测试 session ID")
	trackID := flag.String("track", "video", "测试 track ID")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. 连接 gRPC
	conn, err := grpc.NewClient(*grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Printf("ERROR: connect gRPC: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := pb.NewMediaNodeClient(conn)

	// 2. 创建 session
	_, err = client.CreateSession(ctx, &pb.CreateSessionRequest{
		SessionId: *sessionID,
		RoomId:    "test-room",
	})
	if err != nil {
		fmt.Printf("ERROR: create session: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ session created: %s\n", *sessionID)

	// 3. 添加 video track (VP8)
	_, err = client.AddTrack(ctx, &pb.AddTrackRequest{
		SessionId: *sessionID,
		EndpointId: "test-endpoint",
		Track: &pb.TrackInfo{
			TrackId: *trackID,
			Kind:    "video",
			Codec:   "vp8",
			Ssrc:    12345,
		},
	})
	if err != nil {
		fmt.Printf("ERROR: add track: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ track added: %s (vp8)\n", *trackID)

	// 4. 启动 HLS 转封装
	streamID := *sessionID + "/" + *trackID
	hlsStartURL := fmt.Sprintf("http://%s/hls/start?stream_id=%s", *httpAddr, streamID)
	resp, err := http.Post(hlsStartURL, "text/plain", nil)
	if err != nil {
		fmt.Printf("ERROR: start HLS: %v\n", err)
		os.Exit(1)
	}
	resp.Body.Close()
	fmt.Printf("✓ HLS output started: %s\n", hlsStartURL)

	// 5. 推送模拟 VP8 RTP 包
	stream, err := client.PushRtp(ctx)
	if err != nil {
		fmt.Printf("ERROR: push rtp: %v\n", err)
		os.Exit(1)
	}

	// 生成 VP8 keyframe payload
	// VP8 RTP descriptor (3 bytes): X=1, S=1, PID=0; ext I=1; PictureID=0x00
	// VP8 keyframe: frame_tag(3) + sync(3: 0x9d 0x01 0x2a) + width(2 LE) + height(2 LE)
	vp8Keyframe := []byte{
		0x90, 0x80, 0x00, // VP8 descriptor
		0xf0, 0x51, 0x00, // frame_tag (keyframe)
		0x9d, 0x01, 0x2a, // sync code
		0x80, 0x02, // width=640
		0xe0, 0x01, // height=480
		// 一些伪 payload
		0x39, 0x5f, 0x00, 0x23, 0x39, 0x5f, 0x00, 0x23,
	}

	vp8PFrame := []byte{
		0x80, 0x80, 0x01, // VP8 descriptor (S=0, continuation)
		0x39, 0x5f, 0x00, 0x23, // P frame payload
	}

	var seq uint32 = 1000
	var ts uint32 = 0

	// 推送 150 帧（约 5 秒的 30fps 视频，超过 1 秒分段时长）
	// 每 30 帧插入一个关键帧（模拟 GOP）
	for i := 0; i < 150; i++ {
		var payload []byte
		var marker bool

		if i%30 == 0 {
			// keyframe（每 1 秒一个）
			payload = vp8Keyframe
			marker = true
		} else {
			// P frame
			payload = vp8PFrame
			marker = true
		}
		ts += 3000 // 90kHz / 30fps = 3000 ticks per frame

		pkt := &pb.PushRtpRequest{
			SessionId: *sessionID,
			TrackId:   *trackID,
			Packet: &pb.RtpPacket{
				Ssrc:            12345,
				PayloadType:     96,
				SequenceNumber:  seq,
				Timestamp:       ts,
				Marker:          marker,
				Payload:         payload,
				ClockRate:       90000,
			},
		}

		if err := stream.Send(pkt); err != nil {
			fmt.Printf("ERROR: send packet %d: %v\n", i, err)
			break
		}
		seq++
	}

	// 关闭推流
	resp2, err := stream.CloseAndRecv()
	if err != nil {
		fmt.Printf("WARN: close push stream: %v\n", err)
	} else {
		fmt.Printf("✓ pushed %d packets\n", resp2.PacketsReceived)
	}

	// 6. 等待 HLS 处理
	fmt.Println("waiting 2s for HLS processing...")
	time.Sleep(2 * time.Second)

	// 7. 拉取 playlist
	playlistURL := fmt.Sprintf("http://%s/hls/playlist.m3u8?stream_id=%s", *httpAddr, streamID)
	resp, err = http.Get(playlistURL)
	if err != nil {
		fmt.Printf("ERROR: get playlist: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	playlist, _ := io.ReadAll(resp.Body)
	fmt.Printf("✓ playlist (HTTP %d):\n%s\n", resp.StatusCode, string(playlist))

	// 8. 检查 API
	apiURL := fmt.Sprintf("http://%s/api/streams", *httpAddr)
	resp, err = http.Get(apiURL)
	if err != nil {
		fmt.Printf("ERROR: get api: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	apiResp, _ := io.ReadAll(resp.Body)
	fmt.Printf("✓ API: %s\n", string(apiResp))

	// 9. 清理
	_, err = client.RemoveTrack(ctx, &pb.RemoveTrackRequest{
		SessionId: *sessionID,
		TrackId:   *trackID,
	})
	fmt.Printf("✓ track removed\n")

	_, err = client.DestroySession(ctx, &pb.DestroySessionRequest{
		SessionId: *sessionID,
	})
	fmt.Printf("✓ session destroyed\n")

	fmt.Println("\n✅ End-to-end test completed!")
}

// 防止 unused import
var _ = bytes.NewReader
var _ = binary.LittleEndian
