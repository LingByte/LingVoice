// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package llm

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/utils"
)

const maxOpenAPIUsageBodyClip = 512 * 1024

func clipForOpenAPIUsageStore(s string) string {
	if len(s) <= maxOpenAPIUsageBodyClip {
		return s
	}
	b := []byte(s)
	n := maxOpenAPIUsageBodyClip
	for n > 0 && n < len(b) && b[n-1]&0xC0 == 0x80 {
		n--
	}
	return string(b[:n]) + "…(truncated)"
}

// OpenAPIUsageEmitMeta 由 HTTP 层填入，用于 OpenAPI 代理用量信号（不含 gin / models）。
type OpenAPIUsageEmitMeta struct {
	UserIDStr string
	UserAgent string
	ClientIP  string
}

func openapiUsageFailRequestID(prefix string) string {
	if utils.SnowflakeUtil != nil {
		return prefix + utils.SnowflakeUtil.GenID()
	}
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func tpsFromOutputTokens(outTok int, hopMs int64) float64 {
	if outTok <= 0 || hopMs <= 0 {
		return 0
	}
	return float64(outTok) / (float64(hopMs) / 1000.0)
}

// EmitOpenAPIOpenAIUsageSuccess 非流式 OpenAI 兼容 chat completion 成功后的用量信号。
func EmitOpenAPIOpenAIUsageSuccess(reqBody []byte, res *OpenAPIProxyResult, meta OpenAPIUsageEmitMeta) {
	if res == nil || res.WinChannelID <= 0 || len(res.FinalBody) == 0 {
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(res.FinalBody, &raw); err != nil {
		return
	}
	if _, hasErr := raw["error"]; hasErr {
		return
	}
	idBytes, ok := raw["id"]
	if !ok {
		return
	}
	var idStr string
	if err := json.Unmarshal(idBytes, &idStr); err != nil || strings.TrimSpace(idStr) == "" {
		return
	}
	modelStr := ""
	if m, ok := raw["model"]; ok {
		_ = json.Unmarshal(m, &modelStr)
	}
	var usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	}
	if u, ok := raw["usage"]; ok {
		_ = json.Unmarshal(u, &usage)
	}
	inTok := usage.PromptTokens
	outTok := usage.CompletionTokens
	tot := usage.TotalTokens
	if tot == 0 {
		tot = inTok + outTok
	}
	now := time.Now()
	ms := now.UnixMilli()
	reqMs := ms
	if cr, ok := raw["created"]; ok {
		var sec int64
		if json.Unmarshal(cr, &sec) == nil && sec > 0 {
			reqMs = sec * 1000
		}
	}
	tps := tpsFromOutputTokens(outTok, res.WinHopMs)
	payload := &LLMUsageSignalPayload{
		RequestID:       idStr,
		UserID:          strings.TrimSpace(meta.UserIDStr),
		Provider:        "openai",
		Model:           strings.TrimSpace(modelStr),
		BaseURL:         strings.TrimSpace(res.WinBaseURL),
		RequestType:     "openapi_openai_chat_completions",
		ChannelID:       res.WinChannelID,
		ChannelAttempts: res.Attempts,
		InputTokens:     inTok,
		OutputTokens:    outTok,
		TotalTokens:     tot,
		LatencyMs:       res.WallLatencyMs,
		TTFTMs:          res.WinHopMs,
		TPS:             tps,
		QueueTimeMs:     res.QueueMs,
		RequestContent:  clipForOpenAPIUsageStore(string(reqBody)),
		ResponseContent: clipForOpenAPIUsageStore(string(res.FinalBody)),
		UserAgent:       meta.UserAgent,
		IPAddress:       meta.ClientIP,
		StatusCode:      res.FinalStatus,
		Success:         true,
		RequestedAtMs:   reqMs,
		StartedAtMs:     ms,
		FirstTokenAtMs:  0,
		CompletedAtMs:   ms,
	}
	utils.Sig().Emit(SignalLLMUsage, payload)
}

// EmitOpenAPIOpenAIUsageFailure 非流式或流式入口失败时的用量信号。
func EmitOpenAPIOpenAIUsageFailure(reqBody []byte, res *OpenAPIProxyResult, meta OpenAPIUsageEmitMeta, extraMsg string) {
	rid := openapiUsageFailRequestID("ling-openapi-openai-fail-")
	msg := strings.TrimSpace(extraMsg)
	if msg == "" && res != nil && len(res.Attempts) > 0 {
		last := res.Attempts[len(res.Attempts)-1]
		msg = strings.TrimSpace(last.ErrorMessage)
		if msg == "" {
			msg = strings.TrimSpace(last.ErrorCode)
		}
	}
	if msg == "" {
		msg = "openai upstream failed"
	}
	var wall, queue int64
	var atts []UsageChannelAttempt
	respClip := ""
	httpCode := 502
	if res != nil {
		wall = res.WallLatencyMs
		queue = res.QueueMs
		atts = res.Attempts
		if res.FinalStatus > 0 {
			httpCode = res.FinalStatus
		}
		if len(res.FinalBody) > 0 {
			respClip = clipForOpenAPIUsageStore(string(res.FinalBody))
		}
	}
	ms := time.Now().UnixMilli()
	payload := &LLMUsageSignalPayload{
		RequestID:       rid,
		UserID:          strings.TrimSpace(meta.UserIDStr),
		Provider:        "openai",
		Model:           "",
		BaseURL:         "",
		RequestType:     "openapi_openai_chat_completions",
		ChannelID:       0,
		ChannelAttempts: atts,
		LatencyMs:       wall,
		TTFTMs:          0,
		TPS:             0,
		QueueTimeMs:     queue,
		RequestContent:  clipForOpenAPIUsageStore(string(reqBody)),
		ResponseContent: respClip,
		UserAgent:       meta.UserAgent,
		IPAddress:       meta.ClientIP,
		StatusCode:      httpCode,
		Success:         false,
		ErrorCode:       "all_channels_exhausted",
		ErrorMessage:    truncateOpenAPIAttemptMsg(msg, maxOpenAPIAttemptErrBytes),
		RequestedAtMs:   ms,
		StartedAtMs:     ms,
		FirstTokenAtMs:  0,
		CompletedAtMs:   ms,
	}
	utils.Sig().Emit(SignalLLMUsage, payload)
}

// EmitOpenAPIAnthropicUsageSuccess 非流式 Anthropic /v1/messages 成功。
func EmitOpenAPIAnthropicUsageSuccess(reqBody []byte, res *OpenAPIProxyResult, meta OpenAPIUsageEmitMeta) {
	if res == nil || res.WinChannelID <= 0 {
		return
	}
	var a struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(res.FinalBody, &a); err != nil || a.Type == "error" || strings.TrimSpace(a.ID) == "" {
		return
	}
	inTok, outTok := 0, 0
	if a.Usage != nil {
		inTok = a.Usage.InputTokens
		outTok = a.Usage.OutputTokens
	}
	ms := time.Now().UnixMilli()
	tps := tpsFromOutputTokens(outTok, res.WinHopMs)
	payload := &LLMUsageSignalPayload{
		RequestID:       a.ID,
		UserID:          strings.TrimSpace(meta.UserIDStr),
		Provider:        "anthropic",
		Model:           strings.TrimSpace(a.Model),
		BaseURL:         strings.TrimSpace(res.WinBaseURL),
		RequestType:     "openapi_anthropic_messages",
		ChannelID:       res.WinChannelID,
		ChannelAttempts: res.Attempts,
		InputTokens:     inTok,
		OutputTokens:    outTok,
		TotalTokens:     inTok + outTok,
		LatencyMs:       res.WallLatencyMs,
		TTFTMs:          res.WinHopMs,
		TPS:             tps,
		QueueTimeMs:     res.QueueMs,
		RequestContent:  clipForOpenAPIUsageStore(string(reqBody)),
		ResponseContent: clipForOpenAPIUsageStore(string(res.FinalBody)),
		UserAgent:       meta.UserAgent,
		IPAddress:       meta.ClientIP,
		StatusCode:      res.FinalStatus,
		Success:         true,
		RequestedAtMs:   ms,
		StartedAtMs:     ms,
		FirstTokenAtMs:  0,
		CompletedAtMs:   ms,
	}
	utils.Sig().Emit(SignalLLMUsage, payload)
}

// EmitOpenAPIAnthropicUsageFailure 非流式或流式入口失败。
func EmitOpenAPIAnthropicUsageFailure(reqBody []byte, res *OpenAPIProxyResult, meta OpenAPIUsageEmitMeta, extraMsg string) {
	rid := openapiUsageFailRequestID("ling-openapi-anthropic-fail-")
	msg := strings.TrimSpace(extraMsg)
	if msg == "" && res != nil && len(res.Attempts) > 0 {
		last := res.Attempts[len(res.Attempts)-1]
		msg = strings.TrimSpace(last.ErrorMessage)
		if msg == "" {
			msg = strings.TrimSpace(last.ErrorCode)
		}
	}
	if msg == "" {
		msg = "anthropic upstream failed"
	}
	var wall, queue int64
	var atts []UsageChannelAttempt
	respClip := ""
	httpCode := 502
	if res != nil {
		wall = res.WallLatencyMs
		queue = res.QueueMs
		atts = res.Attempts
		if res.FinalStatus > 0 {
			httpCode = res.FinalStatus
		}
		if len(res.FinalBody) > 0 {
			respClip = clipForOpenAPIUsageStore(string(res.FinalBody))
		}
	}
	ms := time.Now().UnixMilli()
	payload := &LLMUsageSignalPayload{
		RequestID:       rid,
		UserID:          strings.TrimSpace(meta.UserIDStr),
		Provider:        "anthropic",
		Model:           "",
		BaseURL:         "",
		RequestType:     "openapi_anthropic_messages",
		ChannelID:       0,
		ChannelAttempts: atts,
		LatencyMs:       wall,
		QueueTimeMs:     queue,
		RequestContent:  clipForOpenAPIUsageStore(string(reqBody)),
		ResponseContent: respClip,
		UserAgent:       meta.UserAgent,
		IPAddress:       meta.ClientIP,
		StatusCode:      httpCode,
		Success:         false,
		ErrorCode:       "all_channels_exhausted",
		ErrorMessage:    truncateOpenAPIAttemptMsg(msg, maxOpenAPIAttemptErrBytes),
		RequestedAtMs:   ms,
		StartedAtMs:     ms,
		FirstTokenAtMs:  0,
		CompletedAtMs:   ms,
	}
	utils.Sig().Emit(SignalLLMUsage, payload)
}

// EmitOpenAPIOpenAIUsageStreamSuccess 流式 chat completion 成功后的用量（建议请求体含 stream_options.include_usage）。
func EmitOpenAPIOpenAIUsageStreamSuccess(reqBody []byte, meta OpenAPIUsageEmitMeta, cap *OpenAIStreamCapture, channelID int, baseURL string, attempts []UsageChannelAttempt) {
	if cap == nil || strings.TrimSpace(meta.UserIDStr) == "" {
		return
	}
	rid := cap.effectiveRequestID()
	ttft := int64(0)
	if cap.FirstTokenAtMs > 0 && cap.StartedAtMs > 0 {
		ttft = cap.FirstTokenAtMs - cap.StartedAtMs
	}
	tps := tpsFromOutputTokens(cap.CompletionTokens, cap.WallLatencyMs)
	reqMs := cap.StartedAtMs
	if reqMs <= 0 {
		reqMs = time.Now().UnixMilli()
	}
	payload := &LLMUsageSignalPayload{
		RequestID:       rid,
		UserID:          strings.TrimSpace(meta.UserIDStr),
		Provider:        "openai",
		Model:           strings.TrimSpace(cap.Model),
		BaseURL:         strings.TrimSpace(baseURL),
		RequestType:     "openapi_openai_chat_completions_stream",
		ChannelID:       channelID,
		ChannelAttempts: attempts,
		InputTokens:     cap.PromptTokens,
		OutputTokens:    cap.CompletionTokens,
		TotalTokens:     cap.TotalTokens,
		LatencyMs:       cap.WallLatencyMs,
		TTFTMs:          ttft,
		TPS:             tps,
		QueueTimeMs:     0,
		RequestContent:  clipForOpenAPIUsageStore(string(reqBody)),
		ResponseContent: "",
		UserAgent:       meta.UserAgent,
		IPAddress:       meta.ClientIP,
		StatusCode:      cap.StatusCode,
		Success:         true,
		RequestedAtMs:   reqMs,
		StartedAtMs:     cap.StartedAtMs,
		FirstTokenAtMs:  cap.FirstTokenAtMs,
		CompletedAtMs:   cap.CompletedAtMs,
	}
	utils.Sig().Emit(SignalLLMUsage, payload)
}

// EmitOpenAPIAnthropicUsageStreamSuccess Anthropic 流式 messages 成功。
func EmitOpenAPIAnthropicUsageStreamSuccess(reqBody []byte, meta OpenAPIUsageEmitMeta, cap *AnthropicStreamCapture, channelID int, baseURL string, attempts []UsageChannelAttempt) {
	if cap == nil || strings.TrimSpace(meta.UserIDStr) == "" {
		return
	}
	rid := cap.effectiveRequestID()
	ttft := int64(0)
	if cap.FirstTokenAtMs > 0 && cap.StartedAtMs > 0 {
		ttft = cap.FirstTokenAtMs - cap.StartedAtMs
	}
	tps := tpsFromOutputTokens(cap.CompletionTokens, cap.WallLatencyMs)
	reqMs := cap.StartedAtMs
	if reqMs <= 0 {
		reqMs = time.Now().UnixMilli()
	}
	tot := cap.TotalTokens
	if tot == 0 {
		tot = cap.PromptTokens + cap.CompletionTokens
	}
	payload := &LLMUsageSignalPayload{
		RequestID:       rid,
		UserID:          strings.TrimSpace(meta.UserIDStr),
		Provider:        "anthropic",
		Model:           strings.TrimSpace(cap.Model),
		BaseURL:         strings.TrimSpace(baseURL),
		RequestType:     "openapi_anthropic_messages_stream",
		ChannelID:       channelID,
		ChannelAttempts: attempts,
		InputTokens:     cap.PromptTokens,
		OutputTokens:    cap.CompletionTokens,
		TotalTokens:     tot,
		LatencyMs:       cap.WallLatencyMs,
		TTFTMs:          ttft,
		TPS:             tps,
		QueueTimeMs:     0,
		RequestContent:  clipForOpenAPIUsageStore(string(reqBody)),
		ResponseContent: "",
		UserAgent:       meta.UserAgent,
		IPAddress:       meta.ClientIP,
		StatusCode:      cap.StatusCode,
		Success:         true,
		RequestedAtMs:   reqMs,
		StartedAtMs:     cap.StartedAtMs,
		FirstTokenAtMs:  cap.FirstTokenAtMs,
		CompletedAtMs:   cap.CompletedAtMs,
	}
	utils.Sig().Emit(SignalLLMUsage, payload)
}
