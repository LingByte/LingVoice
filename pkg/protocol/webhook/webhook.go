package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/LingByte/LingVoice/pkg/protocol/common"
	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// Config 描述 Webhook 发送器的配置。
type Config struct {
	// URLs 是所有需要接收事件的 Webhook 端点地址。
	URLs []string
	// Secret 用于计算 HMAC-SHA256 签名的密钥。为空时不附加签名头。
	Secret string
	// Timeout 是单次 HTTP 请求的超时时间。
	Timeout time.Duration
	// MaxRetries 是发送失败后的最大重试次数（不含首次发送）。
	MaxRetries int
	// EventTypes 是需要转发的事件类型过滤器。为空表示转发所有事件。
	EventTypes []common.EventType
}

// trackPayload 是 Webhook 负载中用于描述媒体轨道的部分。
type trackPayload struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Direction string `json:"direction"`
	Codec     string `json:"codec"`
	SSRC      uint32 `json:"ssrc"`
	StreamID  string `json:"stream_id"`
}

// Payload 是发送给 Webhook 端点的 JSON 负载。
type Payload struct {
	EventType string       `json:"event_type"`
	Protocol  string       `json:"protocol"`
	SessionID string       `json:"session_id"`
	From      string       `json:"from"`
	To        string       `json:"to"`
	Timestamp time.Time    `json:"timestamp"`
	Track     *trackPayload `json:"track,omitempty"`
}

// Sender 实现 common.EventHandler，将协议事件以 Webhook 方式推送到外部 HTTP 端点。
type Sender struct {
	client *http.Client
	config Config
	log    *zap.Logger
}

// NewSender 创建一个新的 Webhook 发送器。
// 若 log 为 nil，则使用全局 logger.Lg。
func NewSender(config Config, log *zap.Logger) *Sender {
	if log == nil {
		log = logger.Lg
	}
	if log == nil {
		log = zap.NewNop()
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Sender{
		client: &http.Client{Timeout: timeout},
		config: config,
		log:    log,
	}
}

// OnEvent 处理协议信令事件，异步将 JSON POST 发送到所有已配置的 Webhook URL。
// 满足 common.EventHandler 接口。
func (s *Sender) OnEvent(event common.ProtocolEvent) error {
	if !s.eventAllowed(event.Type) {
		return nil
	}
	payload := s.buildPayload(event)
	body, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("webhook: marshal payload failed", zap.Error(err))
		return err
	}
	for _, url := range s.config.URLs {
		go s.sendWithRetry(url, body)
	}
	return nil
}

// OnMediaFrame 满足 common.EventHandler 接口。
// 媒体帧频率过高，Webhook 不转发，直接丢弃。
func (s *Sender) OnMediaFrame(sessionID string, trackID common.TrackID, frame common.MediaFrame) error {
	return nil
}

// OnData 满足 common.EventHandler 接口。
// Webhook 当前不转发数据通道消息，直接丢弃。
func (s *Sender) OnData(sessionID string, msg common.DataMessage) error {
	return nil
}

// eventAllowed 判断事件类型是否在过滤器允许范围内。
func (s *Sender) eventAllowed(t common.EventType) bool {
	if len(s.config.EventTypes) == 0 {
		return true
	}
	for _, allowed := range s.config.EventTypes {
		if allowed == t {
			return true
		}
	}
	return false
}

// buildPayload 将 ProtocolEvent 转换为 Webhook 负载。
func (s *Sender) buildPayload(event common.ProtocolEvent) Payload {
	p := Payload{
		EventType: eventTypeName(event.Type),
		Protocol:  string(event.Protocol),
		SessionID: event.SessionID,
		From:      event.From,
		To:        event.To,
		Timestamp: event.Timestamp,
	}
	if event.Track != nil {
		p.Track = &trackPayload{
			ID:        string(event.Track.ID),
			Kind:      event.Track.Kind.String(),
			Direction: directionName(event.Track.Direction),
			Codec:     event.Track.Codec.String(),
			SSRC:      event.Track.SSRC,
			StreamID:  event.Track.StreamID,
		}
	}
	return p
}

// sendWithRetry 以指数退避方式重试发送。
func (s *Sender) sendWithRetry(url string, body []byte) {
	backoff := time.Second
	var lastErr error
	for attempt := 0; attempt <= s.config.MaxRetries; attempt++ {
		if attempt > 0 {
			s.log.Debug("webhook: retrying send",
				zap.String("url", url),
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff))
			time.Sleep(backoff)
			backoff *= 2
		}
		if err := s.sendOnce(url, body); err != nil {
			lastErr = err
			s.log.Warn("webhook: send failed",
				zap.String("url", url),
				zap.Int("attempt", attempt),
				zap.Error(err))
			continue
		}
		return
	}
	if lastErr != nil {
		s.log.Error("webhook: send exhausted retries",
			zap.String("url", url),
			zap.Int("max_retries", s.config.MaxRetries),
			zap.Error(lastErr))
	}
}

// sendOnce 执行单次 HTTP POST。
func (s *Sender) sendOnce(url string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "LingVoice-Webhook/1.0")
	if s.config.Secret != "" {
		sig := computeSignature(s.config.Secret, body)
		req.Header.Set("X-LingVoice-Signature", sig)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return &httpError{status: resp.StatusCode}
}

// computeSignature 计算 HMAC-SHA256 签名并返回十六进制编码字符串。
func computeSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// httpError 表示非 2xx 响应状态码错误。
type httpError struct {
	status int
}

func (e *httpError) Error() string {
	return "webhook: unexpected status code: " + http.StatusText(e.status)
}

// eventTypeName 返回事件类型的人类可读名称。
func eventTypeName(t common.EventType) string {
	switch t {
	case common.EventIncomingCall:
		return "incoming_call"
	case common.EventRinging:
		return "ringing"
	case common.EventAnswered:
		return "answered"
	case common.EventHangup:
		return "hangup"
	case common.EventTransfer:
		return "transfer"
	case common.EventTrackAdded:
		return "track_added"
	case common.EventTrackRemoved:
		return "track_removed"
	case common.EventDataChannel:
		return "data_channel"
	case common.EventReconnect:
		return "reconnect"
	case common.EventError:
		return "error"
	default:
		return "unknown"
	}
}

// directionName 返回轨道方向的人类可读名称。
func directionName(d common.TrackDirection) string {
	switch d {
	case common.TrackRecv:
		return "recv"
	case common.TrackSend:
		return "send"
	default:
		return "unknown"
	}
}
