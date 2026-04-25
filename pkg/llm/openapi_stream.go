// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// EnsureOpenAIChatStreamIncludeUsage 在 stream 为 true 时合并 stream_options.include_usage，便于上游返回 usage 块。
func EnsureOpenAIChatStreamIncludeUsage(body []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	var stream bool
	if raw, ok := m["stream"]; ok && json.Unmarshal(raw, &stream) == nil && stream {
		so := map[string]any{"include_usage": true}
		if raw, ok := m["stream_options"]; ok {
			var existing map[string]any
			if json.Unmarshal(raw, &existing) == nil {
				for k, v := range existing {
					so[k] = v
				}
			}
		}
		b, err := json.Marshal(so)
		if err != nil {
			return body
		}
		m["stream_options"] = json.RawMessage(b)
		out, err := json.Marshal(m)
		if err != nil {
			return body
		}
		return out
	}
	return body
}

// OpenAIStreamCapture 流式结束后用于用量信号（依赖上游在流中返回 usage，见 EnsureOpenAIChatStreamIncludeUsage）。
type OpenAIStreamCapture struct {
	StatusCode       int
	WallLatencyMs    int64
	StartedAtMs      int64
	FirstTokenAtMs   int64
	CompletedAtMs    int64
	RequestID        string
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

func (c *OpenAIStreamCapture) effectiveRequestID() string {
	if strings.TrimSpace(c.RequestID) != "" {
		return c.RequestID
	}
	return openapiUsageFailRequestID("ling-openapi-openai-stream-")
}

// ProxyOpenAIStreamWithCapture 流式转发并解析 SSE data 行中的 id/model/usage；将字节原样写入 w。
func ProxyOpenAIStreamWithCapture(ctx context.Context, body []byte, accept string, ch UpstreamChannel, w http.ResponseWriter) (*OpenAIStreamCapture, error) {
	cap := &OpenAIStreamCapture{}
	overall := time.Now()
	cap.StartedAtMs = overall.UnixMilli()

	target := openAIChatCompletionsURL(ch.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return cap, err
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
		return cap, err
	}
	defer resp.Body.Close()
	cap.StatusCode = resp.StatusCode

	CopyOpenAPIProxyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	fl, _ := w.(http.Flusher)
	br := bufio.NewReader(resp.Body)
	var firstTokenRecorded bool
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := w.Write(line); werr != nil {
				cap.CompletedAtMs = time.Now().UnixMilli()
				cap.WallLatencyMs = time.Since(overall).Milliseconds()
				return cap, werr
			}
			if fl != nil {
				fl.Flush()
			}
			parseOpenAISSELine(line, cap, &firstTokenRecorded)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			cap.CompletedAtMs = time.Now().UnixMilli()
			cap.WallLatencyMs = time.Since(overall).Milliseconds()
			return cap, err
		}
	}
	cap.CompletedAtMs = time.Now().UnixMilli()
	cap.WallLatencyMs = time.Since(overall).Milliseconds()
	if cap.FirstTokenAtMs == 0 {
		cap.FirstTokenAtMs = cap.CompletedAtMs
	}
	return cap, nil
}

func parseOpenAISSELine(line []byte, cap *OpenAIStreamCapture, firstTokenRecorded *bool) {
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(s, "data:"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	var chunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Usage   *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal([]byte(payload), &chunk) != nil {
		return
	}
	if strings.TrimSpace(chunk.ID) != "" {
		cap.RequestID = chunk.ID
	}
	if strings.TrimSpace(chunk.Model) != "" {
		cap.Model = chunk.Model
	}
	if chunk.Usage != nil {
		cap.PromptTokens = chunk.Usage.PromptTokens
		cap.CompletionTokens = chunk.Usage.CompletionTokens
		cap.TotalTokens = chunk.Usage.TotalTokens
		if cap.TotalTokens == 0 {
			cap.TotalTokens = cap.PromptTokens + cap.CompletionTokens
		}
	}
	if !*firstTokenRecorded && len(chunk.Choices) > 0 && strings.TrimSpace(chunk.Choices[0].Delta.Content) != "" {
		cap.FirstTokenAtMs = time.Now().UnixMilli()
		*firstTokenRecorded = true
	}
}

// AnthropicStreamCapture Anthropic SSE 流摘要（message_start / message_delta.usage）。
type AnthropicStreamCapture struct {
	StatusCode       int
	WallLatencyMs    int64
	StartedAtMs      int64
	FirstTokenAtMs   int64
	CompletedAtMs    int64
	RequestID        string
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

func (c *AnthropicStreamCapture) effectiveRequestID() string {
	if strings.TrimSpace(c.RequestID) != "" {
		return c.RequestID
	}
	return openapiUsageFailRequestID("ling-openapi-anthropic-stream-")
}

// ProxyAnthropicStreamWithCapture 流式转发并解析 Anthropic SSE。
func ProxyAnthropicStreamWithCapture(ctx context.Context, body []byte, anthropicVersion, anthropicBeta string, ch UpstreamChannel, w http.ResponseWriter) (*AnthropicStreamCapture, error) {
	cap := &AnthropicStreamCapture{}
	overall := time.Now()
	cap.StartedAtMs = overall.UnixMilli()

	target := anthropicMessagesURL(ch.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return cap, err
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
		return cap, err
	}
	defer resp.Body.Close()
	cap.StatusCode = resp.StatusCode

	CopyOpenAPIProxyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	fl, _ := w.(http.Flusher)
	br := bufio.NewReader(resp.Body)
	var firstContent bool
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := w.Write(line); werr != nil {
				cap.CompletedAtMs = time.Now().UnixMilli()
				cap.WallLatencyMs = time.Since(overall).Milliseconds()
				return cap, werr
			}
			if fl != nil {
				fl.Flush()
			}
			parseAnthropicSSELine(line, cap, &firstContent)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			cap.CompletedAtMs = time.Now().UnixMilli()
			cap.WallLatencyMs = time.Since(overall).Milliseconds()
			return cap, err
		}
	}
	cap.CompletedAtMs = time.Now().UnixMilli()
	cap.WallLatencyMs = time.Since(overall).Milliseconds()
	if cap.FirstTokenAtMs == 0 {
		cap.FirstTokenAtMs = cap.CompletedAtMs
	}
	return cap, nil
}

func parseAnthropicSSELine(line []byte, cap *AnthropicStreamCapture, firstContent *bool) {
	s := strings.TrimSpace(string(line))
	if !strings.HasPrefix(s, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(s, "data:"))
	if payload == "" {
		return
	}
	var wrap struct {
		Type string `json:"type"`
		// message_start
		Message *struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"message"`
		// message_delta
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		// content_block_delta
		Delta *struct {
			Text string `json:"text"`
		} `json:"delta"`
	}
	if json.Unmarshal([]byte(payload), &wrap) != nil {
		return
	}
	switch wrap.Type {
	case "message_start":
		if wrap.Message != nil {
			if strings.TrimSpace(wrap.Message.ID) != "" {
				cap.RequestID = wrap.Message.ID
			}
			if strings.TrimSpace(wrap.Message.Model) != "" {
				cap.Model = wrap.Message.Model
			}
		}
	case "message_delta":
		if wrap.Usage != nil {
			cap.PromptTokens = wrap.Usage.InputTokens
			cap.CompletionTokens = wrap.Usage.OutputTokens
			cap.TotalTokens = cap.PromptTokens + cap.CompletionTokens
		}
	case "content_block_delta":
		if !*firstContent && wrap.Delta != nil && strings.TrimSpace(wrap.Delta.Text) != "" {
			cap.FirstTokenAtMs = time.Now().UnixMilli()
			*firstContent = true
		}
	}
}
