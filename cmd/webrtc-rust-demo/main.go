// Command webrtc-rust-demo 启动 WebRTC 信令服务器 + Rust 媒体节点，
// 演示完整的 A→Rust→B 音频转发链路。
//
// 用法：
//
//	1. 先启动 Rust 媒体节点：
//	  cd rust-media && cargo run -p media-node
//
//	2. 再启动本 demo：
//	  go run ./cmd/webrtc-rust-demo
//
//	3. 用两个浏览器打开 http://localhost:8081：
//	  - 浏览器 A：发布音频（麦克风）
//	  - 浏览器 B：订阅音频（耳机）
//	  - A 的音频 → Go/Pion → gRPC PushRtp → Rust → gRPC PullRtp → Go/Pion → B
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/LingByte/LingVoice/pkg/media/rustbridge"
	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/LingVoice/pkg/protocol/webrtc"
	"github.com/LingByte/ling-base/common/logger"
	mediav1 "github.com/LingByte/LingVoice/proto/media/v1"
	pionwebrtc "github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

func normalizeAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	return addr
}

// rustHandler 实现 protocol.EventHandler，将媒体帧桥接到 Rust 媒体节点
type rustHandler struct {
	log    *zap.Logger
	bridge *rustbridge.Client
	srv    *webrtc.Server

	// 跟踪每个 session 对应的 Rust session 状态
	mu       sync.Mutex
	sessions map[string]*sessionState

	// 混音模式
	mixThreshold int   // 超过此人数自动启用混音（0=禁用自动切换）
	forceMix     bool  // 强制启用混音模式（所有人从一开始就用混音）
	maxSpeakers  int   // Top-K 最大发言者数（0=混所有人，5=只混Top5）
	outputCodec  string // 混音输出编码：opus（浏览器）或 pcmu（SIP/极致性能）
	roomMixes    map[string]string // roomID → mixID

	// 录制
	recordEnabled bool   // 是否启用录制
	recordDir     string // 录制文件输出目录
}

type sessionState struct {
	sessionID string
	roomID    string
	created   bool
	// per-track push/pull 管理（支持音频+视频多 track）
	tracks map[common.TrackID]*trackState
	// 自己的 publisher SSRC 集合（用于排除自己，避免自回声）
	mySSRCs sync.Map // map[uint32]struct{}
	// 是否在混音中
	inMix       bool
	mixTrackID  common.TrackID // 混音输出 track ID（"mix-{sessionID}"）
	mixPullCancel context.CancelFunc // 混音 pull loop 的 cancel
	// 录制 ID（如果正在录制）
	recordingID string
}

type trackState struct {
	kind        common.TrackKind
	codec       common.CodecType
	pushStarted bool
	pushCancel  context.CancelFunc
	pullStarted bool
	pullCancel  context.CancelFunc
	subTrackID  common.TrackID // subscriber 侧对应的 track（用于写回）

	// 按 SSRC 分流：每个远端参与者的 SSRC → 独立的 subscriber track
	subTracksMu sync.Mutex
	subTracks   map[uint32]common.TrackID // ssrc → subTrackID
}

func newRustHandler(log *zap.Logger, bridge *rustbridge.Client, srv *webrtc.Server, mixThreshold int, forceMix bool, maxSpeakers int, outputCodec string, recordEnabled bool, recordDir string) *rustHandler {
	return &rustHandler{
		log:           log,
		bridge:        bridge,
		srv:           srv,
		sessions:      make(map[string]*sessionState),
		mixThreshold:  mixThreshold,
		forceMix:      forceMix,
		maxSpeakers:   maxSpeakers,
		outputCodec:   outputCodec,
		roomMixes:     make(map[string]string),
		recordEnabled: recordEnabled,
		recordDir:     recordDir,
	}
}

func (h *rustHandler) getOrCreateSession(sessionID string) *sessionState {
	h.mu.Lock()
	defer h.mu.Unlock()
	ss, ok := h.sessions[sessionID]
	if !ok {
		ss = &sessionState{
			sessionID: sessionID,
			roomID:    "demo-room",
			tracks:    make(map[common.TrackID]*trackState),
		}
		h.sessions[sessionID] = ss
	}
	return ss
}

func (h *rustHandler) getOrCreateTrack(ss *sessionState, trackID common.TrackID, kind common.TrackKind, codec common.CodecType) *trackState {
	ts, ok := ss.tracks[trackID]
	if !ok {
		ts = &trackState{
			kind:      kind,
			codec:     codec,
			subTracks: make(map[uint32]common.TrackID),
		}
		ss.tracks[trackID] = ts
	}
	return ts
}

// countRoomAudioParticipants 统计 room 中有音频 track 的 session 数量
func (h *rustHandler) countRoomAudioParticipants(roomID string) int {
	count := 0
	h.mu.Lock()
	for _, ss := range h.sessions {
		if ss.roomID == roomID && ss.created {
			for _, ts := range ss.tracks {
				if ts.kind == common.TrackAudio {
					count++
					break
				}
			}
		}
	}
	h.mu.Unlock()
	return count
}

// getRoomAudioSessions 返回 room 中有音频 track 的 session 列表
func (h *rustHandler) getRoomAudioSessions(roomID string) []string {
	var result []string
	h.mu.Lock()
	for sid, ss := range h.sessions {
		if ss.roomID == roomID && ss.created {
			for _, ts := range ss.tracks {
				if ts.kind == common.TrackAudio {
					result = append(result, sid)
					break
				}
			}
		}
	}
	h.mu.Unlock()
	return result
}

// ensureMixStarted 确保 room 的混音已启动，返回 mixID
func (h *rustHandler) ensureMixStarted(roomID string) (string, error) {
	h.mu.Lock()
	mixID, exists := h.roomMixes[roomID]
	h.mu.Unlock()
	if exists {
		return mixID, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mixID, err := h.bridge.StartMix(ctx, roomID, 48000, 960, uint32(h.maxSpeakers), h.outputCodec)
	if err != nil {
		return "", fmt.Errorf("start mix for room %s: %w", roomID, err)
	}

	h.mu.Lock()
	h.roomMixes[roomID] = mixID
	h.mu.Unlock()

	h.log.Info(">> 混音模式已启动",
		zap.String("room", roomID),
		zap.String("mixID", mixID))
	return mixID, nil
}

// addSessionToMix 将 session 加入 room 的混音
func (h *rustHandler) addSessionToMix(roomID, sessionID string, audioTrackID common.TrackID) error {
	mixID, err := h.ensureMixStarted(roomID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.bridge.AddMixParticipant(ctx, mixID, sessionID, string(audioTrackID), false); err != nil {
		return fmt.Errorf("add mix participant: %w", err)
	}

	ss := h.getOrCreateSession(sessionID)
	ss.inMix = true
	ss.mixTrackID = common.TrackID(fmt.Sprintf("mix-%s", sessionID))

	h.log.Info(">> 参与者加入混音",
		zap.String("session", sessionID),
		zap.String("mixTrack", string(ss.mixTrackID)))
	return nil
}

// removeSessionFromMix 将 session 从混音中移除
func (h *rustHandler) removeSessionFromMix(roomID, sessionID string) {
	h.mu.Lock()
	mixID, hasMix := h.roomMixes[roomID]
	h.mu.Unlock()
	if !hasMix {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.bridge.RemoveMixParticipant(ctx, mixID, sessionID); err != nil {
		h.log.Warn("remove mix participant failed", zap.Error(err))
	}

	ss := h.getOrCreateSession(sessionID)
	ss.inMix = false
}

func (h *rustHandler) OnEvent(event common.ProtocolEvent) error {
	switch event.Type {
	case common.EventIncomingCall:
		h.log.Info(">> 来电",
			zap.String("session", event.SessionID),
			zap.String("from", event.From))
		// 在 Rust 创建 session，所有 participant 加入同一个 room
		ss := h.getOrCreateSession(event.SessionID)
		if !ss.created {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := h.bridge.CreateSession(ctx, event.SessionID, "demo-room", ""); err != nil {
				h.log.Error("rust CreateSession failed", zap.Error(err))
			} else {
				ss.created = true
				h.log.Info("rust session created",
					zap.String("session", event.SessionID),
					zap.String("room", "demo-room"))
			}
			cancel()
		}

		// 通知同 room 已有的参与者：有新人加入
		h.mu.Lock()
		var existingPeers []string
		for sid, pss := range h.sessions {
			if sid != event.SessionID && pss.roomID == ss.roomID && pss.created {
				existingPeers = append(existingPeers, sid)
			}
		}
		h.mu.Unlock()

		joinMsg := fmt.Sprintf(`{"type":"participant_joined","session":"%s","request_keyframe":true}`, event.SessionID)
		for _, peerID := range existingPeers {
			if sess, ok := h.srv.GetSession(peerID); ok {
				_ = sess.SendData("reliable", []byte(joinMsg))
				h.log.Info(">> 通知 peer participant joined",
					zap.String("peer", peerID),
					zap.String("new", event.SessionID))
			}
		}

	case common.EventAnswered:
		h.log.Info(">> ICE 连接成功", zap.String("session", event.SessionID))

	case common.EventTrackAdded:
		if event.Track != nil && event.Track.Direction == common.TrackRecv {
			// Publisher 的轨道就绪（音频或视频）
			kind := event.Track.Kind
			if kind != common.TrackAudio && kind != common.TrackVideo {
				break
			}

			ss := h.getOrCreateSession(event.SessionID)
			ts := h.getOrCreateTrack(ss, event.Track.ID, kind, event.Track.Codec)

			// 记录自己的 SSRC，pull 时排除
			if event.Track.SSRC != 0 {
				ss.mySSRCs.Store(event.Track.SSRC, struct{}{})
				h.log.Info(">> 记录自己的 SSRC",
					zap.String("session", event.SessionID),
					zap.Uint32("ssrc", event.Track.SSRC),
					zap.String("kind", kind.String()))

				// 通知同 room 的其他参与者：我的 track SSRC（用于前端按 session 分组媒体）
				h.mu.Lock()
				var peers []string
				for sid, pss := range h.sessions {
					if sid != event.SessionID && pss.roomID == ss.roomID && pss.created {
						peers = append(peers, sid)
					}
				}
				h.mu.Unlock()

				ssrcMsg := fmt.Sprintf(`{"type":"participant_ssrc","session":"%s","ssrc":%d,"kind":"%s"}`,
					event.SessionID, event.Track.SSRC, kind.String())
				for _, peerID := range peers {
					if peerSess, ok := h.srv.GetSession(peerID); ok {
						_ = peerSess.SendData("reliable", []byte(ssrcMsg))
					}
				}
			}

			h.log.Info(">> 发布者轨道就绪",
				zap.String("session", event.SessionID),
				zap.String("trackID", string(event.Track.ID)),
				zap.String("kind", kind.String()),
				zap.String("codec", event.Track.Codec.String()))

			// 在 Rust 注册 track
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := h.bridge.AddTrack(ctx, event.SessionID, event.SessionID, *event.Track); err != nil {
				h.log.Error("rust AddTrack failed", zap.Error(err))
			}
			cancel()

			// 启动 PushRtp 流（把自己的媒体推给 Rust）
			if !ts.pushStarted {
				ctx2, cancel2 := context.WithCancel(context.Background())
				if err := h.bridge.StartPushRtp(ctx2, event.SessionID, event.Track.ID); err != nil {
					h.log.Error("rust StartPushRtp failed", zap.Error(err))
					cancel2()
				} else {
					ts.pushStarted = true
					ts.pushCancel = cancel2
					h.log.Info("rust push rtp stream started",
						zap.String("session", event.SessionID),
						zap.String("track", string(event.Track.ID)),
						zap.String("kind", kind.String()))
				}
			}

			// 音频 track：检查是否需要混音模式
			if kind == common.TrackAudio {
				if h.forceMix || (h.mixThreshold > 0 && h.countRoomAudioParticipants(ss.roomID) >= h.mixThreshold) {
					// 启用混音模式
					if !ss.inMix {
						if err := h.addSessionToMix(ss.roomID, event.SessionID, event.Track.ID); err != nil {
							h.log.Error("add session to mix failed", zap.Error(err))
						}
					}
				}
			}

			// 录制：在 track 注册后启动（只启动一次，音频 track 到来时触发）
			if h.recordEnabled && ss.recordingID == "" && kind == common.TrackAudio {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				recID, err := h.bridge.StartRecording(ctx, event.SessionID, "wav", h.recordDir, 1)
				if err != nil {
					h.log.Error("start recording failed", zap.Error(err))
				} else {
					ss.recordingID = recID
					h.log.Info("recording started",
						zap.String("session", event.SessionID),
						zap.String("recordingID", recID),
						zap.String("dir", h.recordDir))
				}
				cancel()
			}

			// 启动 PullRtp 流
			// 混音模式：音频从 mix track 拉取（单路混音流），视频仍从 normal track 拉取
			// SFU 模式：从 normal track 拉取（多路按 SSRC 分流）
			if !ts.pullStarted {
				ts.pullStarted = true
				if kind == common.TrackAudio && ss.inMix {
					// 混音模式：从 mix track 拉取混音
					go h.startMixPullLoop(event.SessionID, ss.mixTrackID, kind, event.Track.Codec)
				} else {
					// SFU 模式：从 normal track 拉取
					go h.startPullLoop(event.SessionID, event.Track.ID, kind, event.Track.Codec)
				}
			}
		}

	case common.EventTrackRemoved:
		h.log.Info(">> 轨道移除", zap.String("session", event.SessionID))

	case common.EventHangup:
		h.log.Info(">> 挂断", zap.String("session", event.SessionID))
		// 取消所有 track 的 push/pull，收集清理信息
		h.mu.Lock()
		hangingSS, ok := h.sessions[event.SessionID]
		// 提前提取录制 ID（session 会被删除）
		var recordingIDToStop string
		if ok && hangingSS != nil {
			recordingIDToStop = hangingSS.recordingID
		}
		if ok {
			for _, ts := range hangingSS.tracks {
				if ts.pushCancel != nil {
					ts.pushCancel()
				}
				if ts.pullCancel != nil {
					ts.pullCancel()
				}
			}
			// 取消混音 pull loop
			if hangingSS.mixPullCancel != nil {
				hangingSS.mixPullCancel()
			}
			delete(h.sessions, event.SessionID)
		}
		// 检查是否需要停止混音（room 空了）+ 收集 peers
		var remainingInRoom int
		var roomID string
		var peers []string
		if ok && hangingSS != nil {
			roomID = hangingSS.roomID
			for sid, ss := range h.sessions {
				if sid != event.SessionID && ss.roomID == roomID && ss.created {
					remainingInRoom++
					peers = append(peers, sid)
				}
			}
		}
		// 提前取出 mixID（如果需要停止混音）
		var mixIDToStop string
		var hasMixToStop bool
		if ok && hangingSS != nil && remainingInRoom == 0 && roomID != "" {
			mixIDToStop, hasMixToStop = h.roomMixes[roomID]
			delete(h.roomMixes, roomID)
		}
		h.mu.Unlock()

		// 从混音移除（在锁外做 gRPC 调用）
		if ok && hangingSS != nil && hangingSS.inMix {
			h.removeSessionFromMix(roomID, event.SessionID)
		}

		// 如果 room 空了，停止混音
		if hasMixToStop {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := h.bridge.StopMix(ctx, mixIDToStop); err != nil {
				h.log.Warn("stop mix failed", zap.Error(err))
			}
			cancel()
			h.log.Info(">> 混音已停止（room 空）",
				zap.String("room", roomID),
				zap.String("mixID", mixIDToStop))
		}

		// 通知同 room 的其他 participant：有人离开了
		leaveMsg := fmt.Sprintf(`{"type":"participant_left","session":"%s"}`, event.SessionID)
		for _, peerID := range peers {
			if sess, ok := h.srv.GetSession(peerID); ok {
				_ = sess.SendData("reliable", []byte(leaveMsg))
				h.log.Info(">> 通知 peer participant left",
					zap.String("peer", peerID),
					zap.String("left", event.SessionID))
			}
		}

		// 停止录制
		if recordingIDToStop != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			resp, err := h.bridge.StopRecording(ctx, event.SessionID, recordingIDToStop)
			if err != nil {
				h.log.Error("stop recording failed", zap.Error(err))
			} else {
				h.log.Info("recording stopped",
					zap.String("session", event.SessionID),
					zap.String("file", resp.FilePath),
					zap.Uint64("duration_ms", resp.DurationMs),
					zap.Uint64("file_size", resp.FileSize))
			}
			cancel()
		}

		// 清理 Rust session
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := h.bridge.DestroySession(ctx, event.SessionID); err != nil {
			h.log.Error("rust DestroySession failed", zap.Error(err))
		}
		cancel()

	case common.EventError:
		h.log.Error(">> 错误", zap.String("session", event.SessionID), zap.Error(event.Err))
	}
	return nil
}

func (h *rustHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	// 将 RTP 帧推送到 Rust 媒体节点
	if err := h.bridge.PushRtpPacket(sessionID, trackID, frame); err != nil {
		// 非致命：偶尔推送失败不中断
		h.log.Debug("push rtp packet failed",
			zap.String("session", sessionID),
			zap.Error(err))
	}
	return nil
}

func (h *rustHandler) OnData(sessionID string, msg common.DataMessage) error {
	h.log.Info("<< 数据通道消息",
		zap.String("session", sessionID),
		zap.String("channel", msg.Channel),
		zap.String("data", string(msg.Data)))

	// echo 回发送者
	if sess, ok := h.srv.GetSession(sessionID); ok {
		_ = sess.SendData(msg.Channel, []byte(fmt.Sprintf("[echo] %s", string(msg.Data))))
	}

	// 广播给同 room 其他 session
	h.mu.Lock()
	for sid := range h.sessions {
		if sid == sessionID {
			continue
		}
		if sess, ok := h.srv.GetSession(sid); ok {
			_ = sess.SendData(msg.Channel, []byte(fmt.Sprintf("[来自 %s] %s", sessionID[:8], string(msg.Data))))
		}
	}
	h.mu.Unlock()

	return nil
}

// startPullLoop 从 Rust 拉取同 room 其他 participant 的媒体，按 SSRC 分流到独立的 subscriber track。
//
// 流程：
//  1. 从 Rust pull 自己的 publisher track（Rust 把同 room 其他人的包转发到这里）
//  2. 收到包后按 SSRC 判断来自哪个 participant
//  3. 每个 SSRC 对应一个独立的 subscriber track（首次见到时动态创建）
//  4. 通过 SendMediaFrame 写到对应的 subscriber track
//  5. 前端 ontrack 为每个 track 触发，动态创建媒体元素
func (h *rustHandler) startPullLoop(sessionID string, pubTrackID common.TrackID, kind common.TrackKind, codec common.CodecType) {
	if h.srv == nil {
		h.log.Error("srv not set, cannot start pull loop")
		return
	}

	sess, ok := h.srv.GetSession(sessionID)
	if !ok {
		h.log.Error("session not found for pull loop", zap.String("session", sessionID))
		return
	}

	ss := h.getOrCreateSession(sessionID)

	// 从 Rust pull 自己的 publisher track
	ctx, cancel := context.WithCancel(context.Background())
	if ts, ok := ss.tracks[pubTrackID]; ok {
		ts.pullCancel = cancel
	} else {
		cancel()
	}

	err := h.bridge.StartPullRtp(ctx, sessionID, pubTrackID, kind, codec, func(frame common.MediaFrame) error {
		// 按 SSRC 分流：每个远端参与者有独立的 SSRC
		ssrc := frame.SSRC
		if ssrc == 0 {
			return nil
		}

		// 排除自己的 SSRC（避免自回声）
		if _, isMine := ss.mySSRCs.Load(ssrc); isMine {
			return nil
		}

		// 查找或创建该 SSRC 对应的 subscriber track
		ts := ss.tracks[pubTrackID]
		if ts == nil {
			return fmt.Errorf("track state not found")
		}

		ts.subTracksMu.Lock()
		subTrackID, exists := ts.subTracks[ssrc]
		if !exists {
			// 首次见到这个 SSRC，创建新的 subscriber track
			trackCfg := common.TrackConfig{
				Kind:     kind,
				Codec:    codec,
				Label:    fmt.Sprintf("sub-%s-ssrc-%d", kind.String(), ssrc),
				StreamID: fmt.Sprintf("peer-%d", ssrc),
			}
			if kind == common.TrackAudio {
				trackCfg.SampleRate = 48000
				trackCfg.Channels = 2
			} else {
				trackCfg.SampleRate = 90000
			}

			newSubTrackID, err := sess.AddTrack(trackCfg)
			if err != nil {
				ts.subTracksMu.Unlock()
				return fmt.Errorf("add subscriber track for ssrc %d: %w", ssrc, err)
			}
			ts.subTracks[ssrc] = newSubTrackID
			subTrackID = newSubTrackID
			ts.subTracksMu.Unlock()

			h.log.Info(">> 新远端参与者 track 创建（按 SSRC 分流）",
				zap.String("session", sessionID),
				zap.String("kind", kind.String()),
				zap.Uint32("ssrc", ssrc),
				zap.String("subTrackID", string(newSubTrackID)))

			// 通知前端有新参与者
			peerJoinMsg := fmt.Sprintf(`{"type":"track_added","ssrc":%d,"kind":"%s","subTrackID":"%s"}`, ssrc, kind.String(), string(newSubTrackID))
			_ = sess.SendData("reliable", []byte(peerJoinMsg))

			// 如果是视频 track，向源参与者请求关键帧
			if kind == common.TrackVideo {
				h.mu.Lock()
				for peerSID, pss := range h.sessions {
					if peerSID == sessionID || pss.roomID != ss.roomID {
						continue
					}
					// 检查这个 peer 是否有该 SSRC
					if _, has := pss.mySSRCs.Load(ssrc); has {
						if peerSess, ok := h.srv.GetSession(peerSID); ok {
							_ = peerSess.RequestKeyFrame(ssrc)
							h.log.Info(">> 请求关键帧",
								zap.String("from", peerSID),
								zap.Uint32("ssrc", ssrc))
						}
						break
					}
				}
				h.mu.Unlock()
			}
		} else {
			ts.subTracksMu.Unlock()
		}

		// 重写 SSRC 为固定值，让浏览器认为这是单一来源
		// 每个 subscriber track 用自己的 SSRC，避免浏览器混淆不同来源的包
		rewrittenFrame := frame
		rewrittenFrame.SSRC = ssrc // 保持原始 SSRC，因为每个 track 只收一个 SSRC 的包

		// 写到对应的 subscriber track
		return sess.SendMediaFrame(subTrackID, rewrittenFrame)
	})
	if err != nil {
		h.log.Error("StartPullRtp failed",
			zap.String("session", sessionID),
			zap.String("kind", kind.String()),
			zap.Error(err))
	}
}

// startMixPullLoop 从 Rust 拉取混音后的单路音频流。
//
// 混音模式下，Rust 将同 room 所有其他参与者的音频混音后编码为单路 Opus，
// 通过 "mix-{sessionID}" track 的 pull_rtp 返回。
// 不需要按 SSRC 分流，因为只有一路混合音频。
func (h *rustHandler) startMixPullLoop(sessionID string, mixTrackID common.TrackID, kind common.TrackKind, codec common.CodecType) {
	if h.srv == nil {
		h.log.Error("srv not set, cannot start mix pull loop")
		return
	}

	sess, ok := h.srv.GetSession(sessionID)
	if !ok {
		h.log.Error("session not found for mix pull loop", zap.String("session", sessionID))
		return
	}

	// 创建一个 subscriber track 用于接收混音
	trackCfg := common.TrackConfig{
		Kind:       kind,
		Codec:      codec,
		Label:      fmt.Sprintf("mix-audio-%s", sessionID),
		StreamID:   "mixed-audio",
		SampleRate: 48000,
		Channels:   2, // 必须与浏览器协商的 Opus 一致（stereo）
	}
	subTrackID, err := sess.AddTrack(trackCfg)
	if err != nil {
		h.log.Error("add mix subscriber track failed", zap.Error(err))
		return
	}

	h.log.Info(">> 混音 subscriber track 创建",
		zap.String("session", sessionID),
		zap.String("subTrackID", string(subTrackID)),
		zap.String("mixTrackID", string(mixTrackID)))

	// 通知前端
	joinMsg := fmt.Sprintf(`{"type":"mix_track_created","subTrackID":"%s"}`, string(subTrackID))
	_ = sess.SendData("reliable", []byte(joinMsg))

	ctx, cancel := context.WithCancel(context.Background())
	// Store cancel in sessionState for hangup cleanup (cannot defer, would cancel immediately)
	ss2 := h.getOrCreateSession(sessionID)
	ss2.mixPullCancel = cancel


	err = h.bridge.StartPullRtp(ctx, sessionID, mixTrackID, kind, codec, func(frame common.MediaFrame) error {
		// 混音是单路流，直接写到 subscriber track
		// SSRC 重写为固定值（mix SSRC）
		mixFrame := frame
		mixFrame.SSRC = 0x4D495800 // "MIX\0"，与 Rust egress bridge 一致
		return sess.SendMediaFrame(subTrackID, mixFrame)
	})
	if err != nil {
		h.log.Error("StartPullRtp (mix) failed",
			zap.String("session", sessionID),
			zap.String("mixTrack", string(mixTrackID)),
			zap.Error(err))
	}
}

func main() {
	var (
		addr       = flag.String("addr", ":8081", "WebRTC 信令监听地址")
		path       = flag.String("path", "/webrtc/signal", "信令路径")
		stun       = flag.String("stun", "stun:stun.l.google.com:19302", "STUN 服务器")
		rustAddr   = flag.String("rust", "127.0.0.1:50051", "Rust 媒体节点 gRPC 地址")
		statsInt   = flag.Int("stats-interval", 5, "QoS 统计打印间隔（秒），0=禁用")
		tls        = flag.Bool("tls", false, "启用 HTTPS（局域网联调用，自动生成自签证书）")
		tlsCert    = flag.String("tls-cert", "", "TLS 证书文件（为空且 --tls 时自动生成）")
		tlsKey     = flag.String("tls-key", "", "TLS 私钥文件（为空且 --tls 时自动生成）")
		mixThresh  = flag.Int("mix-threshold", 0, "音频混音人数阈值（超过此人数自动启用混音，0=禁用自动切换）")
		forceMix   = flag.Bool("mix", false, "强制启用音频混音模式（所有人从一开始就用 MCU 混音而非 SFU 转发）")
		maxSpeak   = flag.Int("max-speakers", 0, "Top-K 最大发言者数（0=混所有人，5=只混能量最高的5路）")
		outCodec   = flag.String("output-codec", "opus", "混音输出编码：opus（浏览器兼容）或 pcmu（SIP/极致性能）")
		record     = flag.Bool("record", false, "启用房间级录制（音视频都录制，音频WAV+视频H.264）")
		recordDir  = flag.String("record-dir", "./recordings", "录制文件输出目录")
	)
	flag.Parse()

	_ = logger.Init(&logger.LogConfig{
		Level:    "debug",
		Filename: "logs/webrtc-rust-demo.log",
		MaxSize:  100,
		MaxAge:   30,
		Daily:    true,
	}, "dev")
	defer logger.Sync()

	log := logger.Lg

	// 1. 连接 Rust 媒体节点
	log.Info("connecting to rust media node", zap.String("addr", *rustAddr))
	bridge, err := rustbridge.NewClient(*rustAddr, log)
	if err != nil {
		log.Fatal("failed to connect rust media node", zap.Error(err))
	}
	defer bridge.Close()

	// 等待 Rust 节点就绪
	if err := bridge.WaitForReady(10 * time.Second); err != nil {
		log.Fatal("rust media node not ready", zap.Error(err))
	}
	log.Info("connected to rust media node")

	// 2. 创建 WebRTC 服务器
	// 注意：handler 和 srv 循环依赖，先创建 handler 再回填 srv
	handler := newRustHandler(log, bridge, nil, *mixThresh, *forceMix, *maxSpeak, *outCodec, *record, *recordDir)

	cfg := webrtc.DefaultConfig()
	cfg.Addr = *addr
	cfg.Path = *path
	cfg.ICEServers = []pionwebrtc.ICEServer{{URLs: []string{*stun}}}

	srv := webrtc.NewServer(cfg, handler, log)
	handler.srv = srv

	// 3. 启动 QoS 监控
	if *statsInt > 0 {
		go qosMonitor(srv, bridge, log, time.Duration(*statsInt)*time.Second)
	}

	// 4. 启动 WebRTC 服务器
	go func() {
		if *tls {
			certFile, keyFile, err := ensureTLSCert(*tlsCert, *tlsKey, log)
			if err != nil {
				log.Error("TLS cert prepare failed", zap.Error(err))
				os.Exit(1)
			}
			log.Info("starting HTTPS server", zap.String("addr", *addr), zap.String("cert", certFile))
			if err := http.ListenAndServeTLS(*addr, certFile, keyFile, srv.Handler()); err != nil {
				log.Error("webrtc server stopped", zap.Error(err))
				os.Exit(1)
			}
		} else {
			if err := srv.Start(); err != nil {
				log.Error("webrtc server stopped", zap.Error(err))
				os.Exit(1)
			}
		}
	}()

	// 5. 启动事件订阅（从 Rust 接收 VAD/DTMF 等事件）
	go func() {
		ctx := context.Background()
		if err := bridge.StartEvents(ctx, "", func(event *mediav1.MediaEvent) {
			log.Info("rust event",
				zap.String("session", event.SessionId),
				zap.String("type", fmt.Sprintf("%T", event.Event)))
		}); err != nil {
			log.Warn("StartEvents failed", zap.Error(err))
		}
	}()

	log.Info("WebRTC + Rust demo server started")
	fmt.Printf("\n")
	fmt.Printf("========================================\n")
	fmt.Printf("  LingVoice WebRTC + Rust Media Demo\n")
	fmt.Printf("========================================\n")
	fmt.Printf("\n")
	scheme := "http"
	wsScheme := "ws"
	if *tls {
		scheme = "https"
		wsScheme = "wss"
	}
	fmt.Printf("  WebRTC 信令: %s://%s\n", scheme, normalizeAddr(*addr))
	fmt.Printf("  WebSocket:   %s://%s%s\n", wsScheme, normalizeAddr(*addr), *path)
	fmt.Printf("  Rust 媒体:   %s\n", *rustAddr)
	fmt.Printf("  STUN:        %s\n", *stun)
	if *forceMix {
		fmt.Printf("  混音模式:    强制启用（MCU N-1 混音）\n")
	} else if *mixThresh > 0 {
		fmt.Printf("  混音模式:    自动切换（≥%d 人启用 MCU 混音）\n", *mixThresh)
	} else {
		fmt.Printf("  混音模式:    禁用（纯 SFU 转发）\n")
	}
	if *maxSpeak > 0 {
		fmt.Printf("  Top-K:       只混能量最高的 %d 路音频\n", *maxSpeak)
	}
	fmt.Printf("  输出编码:    %s\n", *outCodec)
	if *record {
		fmt.Printf("  录制:        已启用 (输出到 %s)\n", *recordDir)
	}
	if *tls {
		fmt.Printf("  TLS:         已启用（自签证书，浏览器需点\"继续访问\"）\n")
	}
	fmt.Printf("\n")
	fmt.Printf("  媒体链路 (音频+视频):\n")
	fmt.Printf("    浏览器A → Go/Pion → gRPC PushRtp → Rust room 路由 → gRPC PullRtp → Go/Pion → 浏览器B\n")
	fmt.Printf("\n")
	if *tls {
		fmt.Printf("  局域网联调:\n")
		fmt.Printf("    1. 查本机 IP: ifconfig | grep 'inet ' | grep -v 127\n")
		fmt.Printf("    2. 另一台电脑浏览器打开 https://<本机IP>:8081\n")
		fmt.Printf("    3. 浏览器会提示证书不安全，点\"高级\"→\"继续访问\"即可\n")
		fmt.Printf("\n")
	}
	fmt.Printf("  按 Ctrl+C 退出\n")
	fmt.Printf("\n")

	// 等待退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Info("shutting down")
}

func qosMonitor(srv *webrtc.Server, bridge *rustbridge.Client, log *zap.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		count := 0
		srv.RangeSessions(func(sess *webrtc.Session) bool {
			count++
			stats := sess.QoS()
			log.Info("QoS",
				zap.String("session", sess.ID()),
				zap.Duration("duration", stats.Duration),
				zap.Int("tracks", len(stats.Tracks)),
			)
			return true
		})
		if count > 0 {
			log.Info("QoS monitor", zap.Int("activeSessions", count))
		}
	}
}

// ensureTLSCert 确保有 TLS 证书。如果 certFile/keyFile 为空，自动生成自签证书。
// 自签证书包含本机所有网卡的 IP 作为 SAN，方便局域网联调。
func ensureTLSCert(certFile, keyFile string, log *zap.Logger) (string, string, error) {
	if certFile != "" && keyFile != "" {
		return certFile, keyFile, nil
	}

	// 收集本机所有 IP
	var ips []net.IP
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				ips = append(ips, ipNet.IP)
			}
		}
	}
	// 确保 127.0.0.1 在里面
	ips = append(ips, net.IPv4(127, 0, 0, 1))

	// 生成 ECDSA 私钥
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate key: %w", err)
	}

	// 证书模板
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"LingVoice"},
			CommonName:   "LingVoice Dev",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
	}
	for _, ip := range ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}

	// 写入临时文件
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	certFile = "certs/webrtc-dev.crt"
	keyFile = "certs/webrtc-dev.key"
	os.MkdirAll("certs", 0755)
	if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
		return "", "", fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		return "", "", fmt.Errorf("write key: %w", err)
	}

	log.Info("self-signed TLS certificate generated",
		zap.String("cert", certFile),
		zap.Int("ipSANs", len(ips)))
	for _, ip := range ips {
		log.Info("  SAN IP", zap.String("ip", ip.String()))
	}

	return certFile, keyFile, nil
}
