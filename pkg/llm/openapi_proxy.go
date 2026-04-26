// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxOpenAPIAttemptErrBytes = 6000

var upstreamOpenAPIHTTPClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	},
}

// UpstreamChannel OpenAPI 上游渠道（由 HTTP 层从 llm_channels 映射而来）。
type UpstreamChannel struct {
	ID                 int
	Key                string
	BaseURL            *string
	OpenAIOrganization *string
}

func (u UpstreamChannel) baseURLString() string {
	if u.BaseURL == nil {
		return ""
	}
	return strings.TrimSpace(*u.BaseURL)
}

// OpenAPIProxyResult 非流式多渠道路由执行结果。
type OpenAPIProxyResult struct {
	FinalStatus   int
	FinalBody     []byte
	FinalHeader   http.Header
	WinChannelID  int
	WinBaseURL    string
	Attempts      []UsageChannelAttempt
	WallLatencyMs int64
	QueueMs       int64
	WinHopMs      int64
	AllFailed     bool
}

var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailers":            {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

// CopyOpenAPIProxyResponseHeaders 复制上游响应头（跳过逐跳头）。
func CopyOpenAPIProxyResponseHeaders(dst http.Header, src http.Header) {
	if dst == nil || src == nil {
		return
	}
	for k, vv := range src {
		kk := http.CanonicalHeaderKey(k)
		if _, skip := hopByHopHeaders[kk]; skip {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func openAIChatCompletionsURL(base *string) string {
	const def = "https://api.openai.com/v1/chat/completions"
	if base == nil {
		return def
	}
	b := strings.TrimRight(strings.TrimSpace(*base), "/")
	if b == "" {
		return def
	}
	if strings.HasSuffix(strings.ToLower(b), "/v1") {
		return b + "/chat/completions"
	}
	return b + "/v1/chat/completions"
}

func anthropicMessagesURL(base *string) string {
	const def = "https://api.anthropic.com/v1/messages"
	if base == nil {
		return def
	}
	b := strings.TrimRight(strings.TrimSpace(*base), "/")
	if b == "" {
		return def
	}
	if strings.HasSuffix(strings.ToLower(b), "/v1") {
		return b + "/messages"
	}
	return b + "/v1/messages"
}

func truncateOpenAPIAttemptMsg(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	b := []byte(s)
	n := maxBytes
	for n > 0 && n < len(b) && b[n-1]&0xC0 == 0x80 {
		n--
	}
	return string(b[:n]) + "…"
}

func openAIBusinessOK(buf []byte) bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(buf, &raw) != nil {
		return false
	}
	if _, has := raw["error"]; has {
		return false
	}
	if _, has := raw["id"]; !has {
		return false
	}
	if _, has := raw["choices"]; !has {
		return false
	}
	return true
}

func openAIExtractError(buf []byte) (code, msg string) {
	var wrap struct {
		Error *struct {
			Message string      `json:"message"`
			Type    string      `json:"type"`
			Code    interface{} `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(buf, &wrap) != nil || wrap.Error == nil {
		return "invalid_response", truncateOpenAPIAttemptMsg(string(buf), maxOpenAPIAttemptErrBytes)
	}
	code = strings.TrimSpace(wrap.Error.Type)
	if code == "" {
		code = "openai_error"
	}
	msg = strings.TrimSpace(wrap.Error.Message)
	if msg == "" {
		msg = truncateOpenAPIAttemptMsg(string(buf), maxOpenAPIAttemptErrBytes)
	} else {
		msg = truncateOpenAPIAttemptMsg(msg, maxOpenAPIAttemptErrBytes)
	}
	return code, msg
}

func anthropicBusinessOK(buf []byte) bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(buf, &raw) != nil {
		return false
	}
	if typ, ok := raw["type"]; ok {
		var ts string
		_ = json.Unmarshal(typ, &ts)
		if ts == "error" {
			return false
		}
	}
	if _, ok := raw["id"]; !ok {
		return false
	}
	return true
}

func anthropicExtractError(buf []byte) (code, msg string) {
	var wrap struct {
		Type  string `json:"type"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(buf, &wrap) != nil {
		return "invalid_response", truncateOpenAPIAttemptMsg(string(buf), maxOpenAPIAttemptErrBytes)
	}
	if wrap.Type == "error" && wrap.Error != nil {
		code = strings.TrimSpace(wrap.Error.Type)
		if code == "" {
			code = "anthropic_error"
		}
		msg = truncateOpenAPIAttemptMsg(strings.TrimSpace(wrap.Error.Message), maxOpenAPIAttemptErrBytes)
		return code, msg
	}
	return "anthropic_error", truncateOpenAPIAttemptMsg(string(buf), maxOpenAPIAttemptErrBytes)
}

func doOpenAIUpstreamOnce(ctx context.Context, ch UpstreamChannel, body []byte, accept string) (status int, respBody []byte, respHdr http.Header, err error) {
	target := openAIChatCompletionsURL(ch.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(accept) != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(ch.Key))
	if ch.OpenAIOrganization != nil && strings.TrimSpace(*ch.OpenAIOrganization) != "" {
		req.Header.Set("OpenAI-Organization", strings.TrimSpace(*ch.OpenAIOrganization))
	}
	resp, err := upstreamOpenAPIHTTPClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, rerr := io.ReadAll(resp.Body)
	hdr := resp.Header.Clone()
	if rerr != nil {
		return resp.StatusCode, b, hdr, rerr
	}
	return resp.StatusCode, b, hdr, nil
}

func doAnthropicUpstreamOnce(ctx context.Context, ch UpstreamChannel, body []byte, anthropicVersion, anthropicBeta string) (status int, respBody []byte, respHdr http.Header, err error) {
	target := anthropicMessagesURL(ch.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(anthropicVersion) != "" {
		req.Header.Set("anthropic-version", anthropicVersion)
	} else {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	if strings.TrimSpace(anthropicBeta) != "" {
		req.Header.Set("anthropic-beta", anthropicBeta)
	}
	req.Header.Set("x-api-key", strings.TrimSpace(ch.Key))
	resp, err := upstreamOpenAPIHTTPClient.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, rerr := io.ReadAll(resp.Body)
	hdr := resp.Header.Clone()
	if rerr != nil {
		return resp.StatusCode, b, hdr, rerr
	}
	return resp.StatusCode, b, hdr, nil
}

// ProxyOpenAINonStream 按 channels 顺序重试，直到 OpenAI 兼容 chat completion 业务成功或全部失败。
func ProxyOpenAINonStream(ctx context.Context, body []byte, accept string, channels []UpstreamChannel) *OpenAPIProxyResult {
	if len(channels) == 0 {
		return &OpenAPIProxyResult{
			AllFailed:     true,
			FinalStatus:   http.StatusServiceUnavailable,
			FinalBody:     []byte(`{"error":{"message":"No active OpenAI-protocol LLM channel for credential group","type":"api_error","code":"model_not_found"}}`),
			FinalHeader:   http.Header{"Content-Type": []string{"application/json"}},
			WallLatencyMs: 0,
		}
	}
	overallStart := time.Now()
	var attempts []UsageChannelAttempt
	var queueAccum int64

	for i := range channels {
		ch := channels[i]
		t0 := time.Now()
		cctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
		status, buf, hdr, netErr := doOpenAIUpstreamOnce(cctx, ch, body, accept)
		cancel()
		lat := time.Since(t0).Milliseconds()
		ord := i + 1
		bu := ch.baseURLString()

		if netErr != nil {
			attempts = append(attempts, UsageChannelAttempt{
				Order: ord, ChannelID: ch.ID, BaseURL: bu, Success: false,
				LatencyMs: lat, TTFTMs: lat, ErrorCode: "upstream_network",
				ErrorMessage: truncateOpenAPIAttemptMsg(netErr.Error(), maxOpenAPIAttemptErrBytes),
			})
			queueAccum += lat
			continue
		}

		if status >= 200 && status < 300 && openAIBusinessOK(buf) {
			attempts = append(attempts, UsageChannelAttempt{
				Order: ord, ChannelID: ch.ID, BaseURL: bu, Success: true,
				StatusCode: status, LatencyMs: lat, TTFTMs: lat,
			})
			wall := time.Since(overallStart).Milliseconds()
			return &OpenAPIProxyResult{
				FinalStatus:   status,
				FinalBody:     buf,
				FinalHeader:   hdr,
				WinChannelID:  ch.ID,
				WinBaseURL:    bu,
				Attempts:      attempts,
				WallLatencyMs: wall,
				QueueMs:       queueAccum,
				WinHopMs:      lat,
				AllFailed:     false,
			}
		}

		ec, em := openAIExtractError(buf)
		if ec == "" {
			ec = "upstream_http"
		}
		if em == "" {
			em = truncateOpenAPIAttemptMsg(string(buf), maxOpenAPIAttemptErrBytes)
		}
		attempts = append(attempts, UsageChannelAttempt{
			Order: ord, ChannelID: ch.ID, BaseURL: bu, Success: false,
			StatusCode: status, LatencyMs: lat, TTFTMs: lat,
			ErrorCode: ec, ErrorMessage: em,
		})
		queueAccum += lat
	}

	wall := time.Since(overallStart).Milliseconds()
	lastMsg := "all upstream channels failed"
	if n := len(attempts); n > 0 && attempts[n-1].ErrorMessage != "" {
		lastMsg = attempts[n-1].ErrorMessage
	}
	failBody, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": lastMsg,
			"type":    "api_error",
			"code":    "all_channels_exhausted",
		},
	})
	return &OpenAPIProxyResult{
		AllFailed:     true,
		FinalStatus:   http.StatusBadGateway,
		FinalBody:     failBody,
		FinalHeader:   http.Header{"Content-Type": []string{"application/json"}},
		Attempts:      attempts,
		WallLatencyMs: wall,
		QueueMs:       queueAccum,
		WinHopMs:      0,
	}
}

// ProxyAnthropicNonStream 按 channels 顺序重试 Anthropic /v1/messages。
func ProxyAnthropicNonStream(ctx context.Context, body []byte, anthropicVersion, anthropicBeta string, channels []UpstreamChannel) *OpenAPIProxyResult {
	if len(channels) == 0 {
		b, _ := json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "api_error", "message": "No active Anthropic-protocol LLM channel for credential group"},
		})
		return &OpenAPIProxyResult{
			AllFailed:     true,
			FinalStatus:   http.StatusServiceUnavailable,
			FinalBody:     b,
			FinalHeader:   http.Header{"Content-Type": []string{"application/json"}},
			WallLatencyMs: 0,
		}
	}
	overallStart := time.Now()
	var attempts []UsageChannelAttempt
	var queueAccum int64

	for i := range channels {
		ch := channels[i]
		t0 := time.Now()
		cctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
		status, buf, hdr, netErr := doAnthropicUpstreamOnce(cctx, ch, body, anthropicVersion, anthropicBeta)
		cancel()
		lat := time.Since(t0).Milliseconds()
		ord := i + 1
		bu := ch.baseURLString()

		if netErr != nil {
			attempts = append(attempts, UsageChannelAttempt{
				Order: ord, ChannelID: ch.ID, BaseURL: bu, Success: false,
				LatencyMs: lat, TTFTMs: lat, ErrorCode: "upstream_network",
				ErrorMessage: truncateOpenAPIAttemptMsg(netErr.Error(), maxOpenAPIAttemptErrBytes),
			})
			queueAccum += lat
			continue
		}

		if status >= 200 && status < 300 && anthropicBusinessOK(buf) {
			attempts = append(attempts, UsageChannelAttempt{
				Order: ord, ChannelID: ch.ID, BaseURL: bu, Success: true,
				StatusCode: status, LatencyMs: lat, TTFTMs: lat,
			})
			wall := time.Since(overallStart).Milliseconds()
			return &OpenAPIProxyResult{
				FinalStatus:   status,
				FinalBody:     buf,
				FinalHeader:   hdr,
				WinChannelID:  ch.ID,
				WinBaseURL:    bu,
				Attempts:      attempts,
				WallLatencyMs: wall,
				QueueMs:       queueAccum,
				WinHopMs:      lat,
				AllFailed:     false,
			}
		}

		ec, em := anthropicExtractError(buf)
		if ec == "" {
			ec = "upstream_http"
		}
		attempts = append(attempts, UsageChannelAttempt{
			Order: ord, ChannelID: ch.ID, BaseURL: bu, Success: false,
			StatusCode: status, LatencyMs: lat, TTFTMs: lat,
			ErrorCode: ec, ErrorMessage: em,
		})
		queueAccum += lat
	}

	wall := time.Since(overallStart).Milliseconds()
	lastMsg := "all upstream channels failed"
	if n := len(attempts); n > 0 && attempts[n-1].ErrorMessage != "" {
		lastMsg = attempts[n-1].ErrorMessage
	}
	failBody, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "api_error", "message": lastMsg},
	})
	return &OpenAPIProxyResult{
		AllFailed:     true,
		FinalStatus:   http.StatusBadGateway,
		FinalBody:     failBody,
		FinalHeader:   http.Header{"Content-Type": []string{"application/json"}},
		Attempts:      attempts,
		WallLatencyMs: wall,
		QueueMs:       queueAccum,
		WinHopMs:      0,
	}
}
