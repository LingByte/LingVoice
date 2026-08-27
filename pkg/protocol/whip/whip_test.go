package whip

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

// mockHandler 测试用 EventHandler
type mockHandler struct {
	events      []common.ProtocolEvent
	mediaFrames []common.MediaFrame
	dataMsgs    []common.DataMessage
}

func (h *mockHandler) OnEvent(event common.ProtocolEvent) error {
	h.events = append(h.events, event)
	return nil
}

func (h *mockHandler) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	h.mediaFrames = append(h.mediaFrames, frame)
	return nil
}

func (h *mockHandler) OnData(sessionID string, msg common.DataMessage) error {
	h.dataMsgs = append(h.dataMsgs, msg)
	return nil
}

func newTestHandler() *mockHandler {
	return &mockHandler{}
}

func newTestServer() *Server {
	cfg := DefaultConfig()
	cfg.ICEServers = nil // 测试不依赖外部 STUN
	handler := newTestHandler()
	return NewServer(cfg, handler, zap.NewNop())
}

// createTestSession 创建一个真实的 WHIP 会话（通过内部 PeerConnection 模拟客户端 offer）
func createTestSession(t *testing.T, s *Server) (sessionID string, offerSDP string) {
	t.Helper()

	// 模拟客户端 PeerConnection 生成 offer
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create client PC: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	// 添加一个音频 track 以生成有效 SDP
	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "lingvoice",
	)
	if err != nil {
		t.Fatalf("create track: %v", err)
	}
	if _, err := pc.AddTrack(track); err != nil {
		t.Fatalf("add track: %v", err)
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local desc: %v", err)
	}

	// 等待 ICE gathering
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	offerSDP = pc.LocalDescription().SDP

	// 通过 handleWHIP 创建服务端会话
	req := httptest.NewRequest(http.MethodPost, s.config.Path, strings.NewReader(offerSDP))
	req.Header.Set("Content-Type", "application/sdp")
	rr := httptest.NewRecorder()

	s.handleWHIP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create session: expected %d, got %d, body=%s", http.StatusCreated, rr.Code, rr.Body.String())
	}

	// 从 Location header 提取 sessionID
	location := rr.Header().Get("Location")
	parts := strings.Split(location, "/")
	sessionID = parts[len(parts)-1]
	if sessionID == "" {
		t.Fatal("empty session ID from Location header")
	}

	// 将客户端 PC 的 remote 描述设为服务端 answer
	answer := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: rr.Body.String()}
	if err := pc.SetRemoteDescription(answer); err != nil {
		t.Fatalf("client set remote desc: %v", err)
	}

	return sessionID, offerSDP
}

// TestPATCHICERestart 测试通过 PATCH 请求执行 ICE restart
func TestPATCHICERestart(t *testing.T) {
	s := newTestServer()

	// 1. 创建会话
	sessionID, _ := createTestSession(t, s)

	sess, ok := s.GetSession(sessionID)
	if !ok {
		t.Fatalf("session %s not found", sessionID)
	}
	if sess == nil {
		t.Fatal("session is nil")
	}

	// 2. 生成一个新的 offer 用于 ICE restart（新的 ICE ufrag）
	restartPC, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	})
	if err != nil {
		t.Fatalf("create restart PC: %v", err)
	}
	t.Cleanup(func() { _ = restartPC.Close() })

	// 添加音频 track
	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "lingvoice",
	)
	if err != nil {
		t.Fatalf("create restart track: %v", err)
	}
	if _, err := restartPC.AddTrack(track); err != nil {
		t.Fatalf("add restart track: %v", err)
	}

	// 创建 ICE restart offer
	offer, err := restartPC.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create restart offer: %v", err)
	}
	if err := restartPC.SetLocalDescription(offer); err != nil {
		t.Fatalf("set restart local desc: %v", err)
	}

	// 等待 ICE gathering
	gatherComplete := webrtc.GatheringCompletePromise(restartPC)
	<-gatherComplete

	restartOfferSDP := restartPC.LocalDescription().SDP

	// 3. 发送 PATCH 请求
	patchURL := s.config.Path + "/" + sessionID
	req := httptest.NewRequest(http.MethodPatch, patchURL, strings.NewReader(restartOfferSDP))
	req.Header.Set("Content-Type", "application/sdp")
	rr := httptest.NewRecorder()

	s.handleSession(rr, req)

	// 4. 验证 200 响应
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH ICE restart: expected %d, got %d, body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}

	// 5. 验证返回的 SDP answer 非空
	answerSDP := rr.Body.String()
	if answerSDP == "" {
		t.Fatal("PATCH ICE restart: empty answer SDP")
	}
	if !strings.Contains(answerSDP, "v=0") {
		t.Errorf("PATCH ICE restart: answer SDP does not start with v=0, got: %s", answerSDP[:min(50, len(answerSDP))])
	}

	// 6. 验证 Content-Type
	ct := rr.Header().Get("Content-Type")
	if ct != "application/sdp" {
		t.Errorf("PATCH ICE restart: Content-Type = %s, want application/sdp", ct)
	}

	t.Logf("ICE restart successful, answer SDP length: %d", len(answerSDP))
}

// TestPATCHNotFound 测试 PATCH 不存在的 session
func TestPATCHNotFound(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest(http.MethodPatch, s.config.Path+"/nonexistent", strings.NewReader("v=0"))
	rr := httptest.NewRecorder()

	s.handleSession(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("PATCH nonexistent: expected %d, got %d", http.StatusNotFound, rr.Code)
	}
}

// TestPATCHEmptyBody 测试 PATCH 空请求体
func TestPATCHEmptyBody(t *testing.T) {
	s := newTestServer()

	sessionID, _ := createTestSession(t, s)

	req := httptest.NewRequest(http.MethodPatch, s.config.Path+"/"+sessionID, strings.NewReader(""))
	rr := httptest.NewRecorder()

	s.handleSession(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("PATCH empty body: expected %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

// TestPATCHSessionClosed 测试 PATCH 已关闭的 session
func TestPATCHSessionClosed(t *testing.T) {
	s := newTestServer()

	sessionID, _ := createTestSession(t, s)
	sess, _ := s.GetSession(sessionID)

	// 关闭会话
	_ = sess.Close()

	req := httptest.NewRequest(http.MethodPatch, s.config.Path+"/"+sessionID, strings.NewReader("v=0\r\n"))
	rr := httptest.NewRecorder()

	s.handleSession(rr, req)

	if rr.Code != http.StatusGone {
		t.Errorf("PATCH closed session: expected %d, got %d", http.StatusGone, rr.Code)
	}
}

// TestWHIPCreateSession 测试 WHIP POST 创建会话
func TestWHIPCreateSession(t *testing.T) {
	s := newTestServer()

	// 模拟客户端
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create PC: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "lingvoice",
	)
	if err != nil {
		t.Fatalf("create track: %v", err)
	}
	if _, err := pc.AddTrack(track); err != nil {
		t.Fatalf("add track: %v", err)
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local desc: %v", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	<-gatherComplete

	req := httptest.NewRequest(http.MethodPost, s.config.Path, strings.NewReader(pc.LocalDescription().SDP))
	req.Header.Set("Content-Type", "application/sdp")
	rr := httptest.NewRecorder()

	s.handleWHIP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("create session: expected %d, got %d", http.StatusCreated, rr.Code)
	}

	location := rr.Header().Get("Location")
	if location == "" {
		t.Error("missing Location header")
	}

	answerBody := rr.Body.String()
	if !strings.Contains(answerBody, "v=0") {
		t.Error("answer SDP does not contain v=0")
	}

	// 验证 session 存在
	parts := strings.Split(location, "/")
	sessionID := parts[len(parts)-1]
	if _, ok := s.GetSession(sessionID); !ok {
		t.Errorf("session %s not found after creation", sessionID)
	}
}

// TestWHIPDeleteSession 测试 DELETE 关闭会话
func TestWHIPDeleteSession(t *testing.T) {
	s := newTestServer()

	sessionID, _ := createTestSession(t, s)

	req := httptest.NewRequest(http.MethodDelete, s.config.Path+"/"+sessionID, nil)
	rr := httptest.NewRecorder()

	s.handleSession(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("DELETE: expected %d, got %d", http.StatusOK, rr.Code)
	}

	// 验证 session 已删除
	if _, ok := s.GetSession(sessionID); ok {
		t.Error("session still exists after DELETE")
	}
}

// TestWHIPMethodNotAllowed 测试不支持的 HTTP 方法
func TestWHIPMethodNotAllowed(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest(http.MethodGet, s.config.Path, nil)
	rr := httptest.NewRecorder()

	s.handleWHIP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET on WHIP: expected %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}

// 确保导入被使用
var _ = io.EOF
