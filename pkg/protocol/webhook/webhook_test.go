package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"go.uber.org/zap"
)

// newTestSender 创建一个用于测试的 Sender，使用较短的超时和退避。
func newTestSender(urls []string, secret string, maxRetries int, eventTypes []common.EventType) *Sender {
	return NewSender(Config{
		URLs:       urls,
		Secret:     secret,
		Timeout:    2 * time.Second,
		MaxRetries: maxRetries,
		EventTypes: eventTypes,
	}, zap.NewNop())
}

// TestWebhookSender 验证事件被正确发送到端点，且负载字段匹配。
func TestWebhookSender(t *testing.T) {
	var (
		mu      sync.Mutex
		gotBody []byte
		gotSig  string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBody = body
		gotSig = r.Header.Get("X-LingVoice-Signature")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := newTestSender([]string{srv.URL}, "topsecret", 0, nil)

	evt := common.ProtocolEvent{
		Type:      common.EventIncomingCall,
		Protocol:  common.ProtocolSIP,
		SessionID: "sess-123",
		From:      "alice@example.com",
		To:        "bob@example.com",
		Timestamp: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
		Track: &common.TrackInfo{
			ID:       "track-1",
			Kind:     common.TrackAudio,
			Codec:    common.CodecOpus,
			SSRC:     12345,
			StreamID: "stream-a",
		},
	}

	if err := sender.OnEvent(evt); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	// 等待异步发送完成
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		done := len(gotBody) > 0
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(gotBody) == 0 {
		t.Fatal("did not receive webhook payload")
	}

	var p Payload
	if err := json.Unmarshal(gotBody, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.EventType != "incoming_call" {
		t.Errorf("EventType = %q, want %q", p.EventType, "incoming_call")
	}
	if p.Protocol != "sip" {
		t.Errorf("Protocol = %q, want %q", p.Protocol, "sip")
	}
	if p.SessionID != "sess-123" {
		t.Errorf("SessionID = %q, want %q", p.SessionID, "sess-123")
	}
	if p.From != "alice@example.com" {
		t.Errorf("From = %q, want %q", p.From, "alice@example.com")
	}
	if p.To != "bob@example.com" {
		t.Errorf("To = %q, want %q", p.To, "bob@example.com")
	}
	if p.Track == nil {
		t.Fatal("Track is nil")
	}
	if p.Track.ID != "track-1" {
		t.Errorf("Track.ID = %q, want %q", p.Track.ID, "track-1")
	}
	if p.Track.Codec != "opus" {
		t.Errorf("Track.Codec = %q, want %q", p.Track.Codec, "opus")
	}

	// 验证签名头存在
	if gotSig == "" {
		t.Error("X-LingVoice-Signature header missing")
	}
}

// TestWebhookRetry 验证失败端点会触发重试。
func TestWebhookRetry(t *testing.T) {
	var attempts int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		// 始终返回 500，触发重试
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// MaxRetries=2 => 共 3 次尝试。使用较短超时。
	sender := newTestSender([]string{srv.URL}, "", 2, nil)

	evt := common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolWebRTC,
		SessionID: "sess-retry",
		Timestamp: time.Now(),
	}

	if err := sender.OnEvent(evt); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	// 等待重试完成 (1s + 2s = 3s 退避 + 请求时间)
	deadline := time.Now().Add(6 * time.Second)
	for {
		if atomic.LoadInt32(&attempts) >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got := atomic.LoadInt32(&attempts)
	if got != 3 {
		t.Errorf("attempts = %d, want 3 (1 initial + 2 retries)", got)
	}
}

// TestWebhookFilter 验证事件类型过滤：未在 EventTypes 中的事件不会被发送。
func TestWebhookFilter(t *testing.T) {
	var received int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&received, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// 只允许 EventHangup
	sender := newTestSender([]string{srv.URL}, "", 0, []common.EventType{common.EventHangup})

	// 发送一个被过滤掉的事件 (EventRinging)
	filtered := common.ProtocolEvent{
		Type:      common.EventRinging,
		Protocol:  common.ProtocolSIP,
		SessionID: "sess-filtered",
		Timestamp: time.Now(),
	}
	if err := sender.OnEvent(filtered); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	// 发送一个允许的事件 (EventHangup)
	allowed := common.ProtocolEvent{
		Type:      common.EventHangup,
		Protocol:  common.ProtocolSIP,
		SessionID: "sess-allowed",
		Timestamp: time.Now(),
	}
	if err := sender.OnEvent(allowed); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if atomic.LoadInt32(&received) >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := atomic.LoadInt32(&received)
	if got != 1 {
		t.Errorf("received = %d, want 1 (filtered event should not be sent)", got)
	}
}

// TestWebhookSignature 验证 HMAC-SHA256 签名的正确性。
func TestWebhookSignature(t *testing.T) {
	var gotSig string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		gotSig = r.Header.Get("X-LingVoice-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := "my-secret-key"
	sender := newTestSender([]string{srv.URL}, secret, 0, nil)

	evt := common.ProtocolEvent{
		Type:      common.EventAnswered,
		Protocol:  common.ProtocolWS,
		SessionID: "sess-sig",
		From:      "a",
		To:        "b",
		Timestamp: time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
	}

	if err := sender.OnEvent(evt); err != nil {
		t.Fatalf("OnEvent returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if gotSig != "" || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if gotSig == "" {
		t.Fatal("signature header missing")
	}

	// 计算期望签名
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	want := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(gotSig), []byte(want)) {
		t.Errorf("signature mismatch:\n got  %s\n want %s", gotSig, want)
	}

	// 额外验证 computeSignature 函数本身
	body := bytes.Repeat([]byte("x"), 10)
	if computeSignature(secret, body) == "" {
		t.Error("computeSignature returned empty")
	}
}
