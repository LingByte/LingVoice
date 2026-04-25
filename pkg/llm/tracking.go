package llm

import (
	"time"

	"github.com/LingByte/LingVoice/pkg/utils"
)

// LLMRequestTracker LLM请求跟踪器
type LLMRequestTracker struct {
	requestID       string
	sessionID       string
	userID          string
	provider        string
	model           string
	baseURL         string
	requestType     string
	channelID       int
	channelAttempts []UsageChannelAttempt
	startTime       time.Time
	startedAt       time.Time
	firstTokenAt    time.Time
	requestContent  string
	responseContent string
	userAgent       string
	ipAddress       string
	statusCode      int
}

// SetChannelUsageMeta 可选：绑定本次调用命中的上游渠道 id 与多渠道路由尝试明细（如经网关轮询）。
func (t *LLMRequestTracker) SetChannelUsageMeta(channelID int, attempts []UsageChannelAttempt) {
	t.channelID = channelID
	if len(attempts) > 0 {
		t.channelAttempts = append([]UsageChannelAttempt(nil), attempts...)
	}
}

// NewLLMRequestTracker 创建LLM请求跟踪器
func NewLLMRequestTracker(sessionID, userID, provider, model, baseURL, requestType string) *LLMRequestTracker {
	requestID := "ling-chatimpl-" + utils.SnowflakeUtil.GenID()
	now := time.Now()
	tracker := &LLMRequestTracker{
		requestID:   requestID,
		sessionID:   sessionID,
		userID:      userID,
		provider:    provider,
		model:       model,
		baseURL:     baseURL,
		requestType: requestType,
		startTime:   now,
		startedAt:   now,
	}
	startData := LLMRequestStartData{
		RequestID:   requestID,
		SessionID:   sessionID,
		UserID:      userID,
		Provider:    provider,
		Model:       model,
		RequestType: requestType,
		RequestedAt: tracker.startTime.UnixMilli(),
	}
	utils.Sig().Emit(SignalLLMRequestStart, tracker, startData)
	return tracker
}

// GetRequestID 获取请求ID
func (t *LLMRequestTracker) GetRequestID() string {
	return t.requestID
}

// SetRequestContent 设置请求内容
func (t *LLMRequestTracker) SetRequestContent(content string) {
	t.requestContent = content
}

// SetResponseContent 设置响应内容
func (t *LLMRequestTracker) SetResponseContent(content string) {
	t.responseContent = content
}

// SetUserAgent 设置用户代理
func (t *LLMRequestTracker) SetUserAgent(userAgent string) {
	t.userAgent = userAgent
}

// SetIPAddress 设置IP地址
func (t *LLMRequestTracker) SetIPAddress(ip string) {
	t.ipAddress = ip
}

// SetStatusCode 设置HTTP状态码
func (t *LLMRequestTracker) SetStatusCode(code int) {
	t.statusCode = code
}

// MarkStarted 标记实际开始处理时间
func (t *LLMRequestTracker) MarkStarted() {
	t.startedAt = time.Now()
}

// MarkFirstToken 标记首个token时间
func (t *LLMRequestTracker) MarkFirstToken() {
	t.firstTokenAt = time.Now()
}

// Complete 完成请求并记录成功信息
func (t *LLMRequestTracker) Complete(response *QueryResponse) {
	endTime := time.Now()
	latencyMs := endTime.Sub(t.startTime).Milliseconds()

	response.RequestID = t.requestID
	response.SessionID = t.sessionID
	response.UserID = t.userID
	response.RequestedAt = t.startTime.UnixMilli()
	response.CompletedAt = endTime.UnixMilli()
	response.LatencyMs = latencyMs

	// 计算token使用量
	inputTokens := 0
	outputTokens := 0
	if response.Usage != nil {
		inputTokens = response.Usage.PromptTokens
		outputTokens = response.Usage.CompletionTokens
	}
	totalTokens := inputTokens + outputTokens

	// 获取输出内容
	output := ""
	if len(response.Choices) > 0 {
		output = response.Choices[0].Content
	}

	// 计算性能指标
	var ttftMs int64 = 0
	var tps float64 = 0
	var queueTimeMs int64 = 0

	if !t.firstTokenAt.IsZero() {
		ttftMs = t.firstTokenAt.Sub(t.startedAt).Milliseconds()
	}

	if outputTokens > 0 && latencyMs > 0 {
		tps = float64(outputTokens) / (float64(latencyMs) / 1000.0)
	}

	if !t.startedAt.IsZero() {
		queueTimeMs = t.startedAt.Sub(t.startTime).Milliseconds()
	}

	// 发送请求结束信号
	endData := LLMRequestEndData{
		RequestID:    t.requestID,
		SessionID:    t.sessionID,
		UserID:       t.userID,
		Provider:     t.provider,
		Model:        t.model,
		RequestType:  t.requestType,
		Success:      true,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
		LatencyMs:    latencyMs,
		Output:       output,
		RequestedAt:  t.startTime.UnixMilli(),
		CompletedAt:  endTime.UnixMilli(),
	}

	utils.Sig().Emit(SignalLLMRequestEnd, t, endData)

	payload := LLMUsageSignalPayload{
		RequestID:       t.requestID,
		UserID:          t.userID,
		Provider:        t.provider,
		Model:           t.model,
		BaseURL:         t.baseURL,
		RequestType:     t.requestType,
		ChannelID:       t.channelID,
		ChannelAttempts: t.channelAttempts,
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		TotalTokens:     totalTokens,
		LatencyMs:       latencyMs,
		TTFTMs:          ttftMs,
		TPS:             tps,
		QueueTimeMs:     queueTimeMs,
		RequestContent:  t.requestContent,
		ResponseContent: output,
		UserAgent:       t.userAgent,
		IPAddress:       t.ipAddress,
		StatusCode:      t.statusCode,
		Success:         true,
		RequestedAtMs:   t.startTime.UnixMilli(),
		StartedAtMs:     t.startedAt.UnixMilli(),
		FirstTokenAtMs:  t.firstTokenAt.UnixMilli(),
		CompletedAtMs:   endTime.UnixMilli(),
	}
	utils.Sig().Emit(SignalLLMUsage, &payload)
}

// Error 记录请求错误
func (t *LLMRequestTracker) Error(errCode, errorMessage string) {
	endTime := time.Now()
	latencyMs := endTime.Sub(t.startTime).Milliseconds()

	// 计算排队时间
	var queueTimeMs int64 = 0
	if !t.startedAt.IsZero() {
		queueTimeMs = t.startedAt.Sub(t.startTime).Milliseconds()
	}

	errorData := LLMRequestErrorData{
		RequestID:    t.requestID,
		SessionID:    t.sessionID,
		UserID:       t.userID,
		Provider:     t.provider,
		Model:        t.model,
		RequestType:  t.requestType,
		ErrorCode:    errCode,
		ErrorMessage: errorMessage,
		LatencyMs:    latencyMs,
		RequestedAt:  t.startTime.UnixMilli(),
		CompletedAt:  endTime.UnixMilli(),
	}
	utils.Sig().Emit(SignalLLMRequestError, t, errorData)

	payload := LLMUsageSignalPayload{
		RequestID:       t.requestID,
		UserID:          t.userID,
		Provider:        t.provider,
		Model:           t.model,
		BaseURL:         t.baseURL,
		RequestType:     t.requestType,
		ChannelID:       t.channelID,
		ChannelAttempts: t.channelAttempts,
		LatencyMs:       latencyMs,
		QueueTimeMs:     queueTimeMs,
		RequestContent:  t.requestContent,
		ResponseContent: t.responseContent,
		UserAgent:       t.userAgent,
		IPAddress:       t.ipAddress,
		StatusCode:      t.statusCode,
		Success:         false,
		ErrorCode:       errCode,
		ErrorMessage:    errorMessage,
		RequestedAtMs:   t.startTime.UnixMilli(),
		StartedAtMs:     t.startedAt.UnixMilli(),
		FirstTokenAtMs:  t.firstTokenAt.UnixMilli(),
		CompletedAtMs:   endTime.UnixMilli(),
	}
	utils.Sig().Emit(SignalLLMUsage, &payload)
}

// CreateSession 创建会话并发送信号
func CreateSession(sessionID, userID, title, provider, model, systemPrompt string) {
	startData := SessionCreatedData{
		SessionID:    sessionID,
		UserID:       userID,
		Title:        title,
		Provider:     provider,
		Model:        model,
		SystemPrompt: systemPrompt,
		CreatedAt:    time.Now().UnixMilli(),
	}
	utils.Sig().Emit(SignalSessionCreated, nil, startData)
}

// CreateMessage 创建消息并发送信号
func CreateMessage(messageID, sessionID, role, content string, tokenCount int, model, provider, requestID string) {
	startData := MessageCreatedData{
		MessageID:  messageID,
		SessionID:  sessionID,
		Role:       role,
		Content:    content,
		TokenCount: tokenCount,
		Model:      model,
		Provider:   provider,
		RequestID:  requestID,
		CreatedAt:  time.Now().UnixMilli(),
	}
	utils.Sig().Emit(SignalMessageCreated, nil, startData)
}
