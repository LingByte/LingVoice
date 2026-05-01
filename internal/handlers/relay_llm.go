// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/gin-gonic/gin"
)

func openapiQuotaGroupRatio(cred *models.Credential) float64 {
	if cred == nil || config.GlobalConfig == nil {
		return 1
	}
	return config.GlobalConfig.OpenAPIQuotaGroupRatio(cred.Group)
}

func relayLLMChannelsToUpstream(chs []models.LLMChannel) []llm.UpstreamChannel {
	out := make([]llm.UpstreamChannel, 0, len(chs))
	for i := range chs {
		ch := chs[i]
		out = append(out, llm.UpstreamChannel{
			ID:                 ch.Id,
			Key:                ch.Key,
			BaseURL:            ch.BaseURL,
			OpenAIOrganization: ch.OpenAIOrganization,
		})
	}
	return out
}

func relayUsageMeta(c *gin.Context, cred *models.Credential) llm.RelayUsageMeta {
	return llm.RelayUsageMeta{
		UserIDStr: models.CredentialUserIDString(cred.UserId),
		UserAgent: c.Request.UserAgent(),
		ClientIP:  c.ClientIP(),
	}
}

func openAIStreamAttempts(ch models.LLMChannel, cap *llm.OpenAIStreamCapture, streamOK bool) []llm.UsageChannelAttempt {
	bu := models.LLMChannelBaseURLString(&ch)
	if cap == nil {
		return []llm.UsageChannelAttempt{{
			Order: 1, ChannelID: ch.Id, BaseURL: bu, Success: false,
			ErrorCode: "upstream", ErrorMessage: "empty capture",
		}}
	}
	ttft := int64(0)
	if cap.FirstTokenAtMs > 0 && cap.StartedAtMs > 0 {
		ttft = cap.FirstTokenAtMs - cap.StartedAtMs
	}
	ok := streamOK && cap.StatusCode >= 200 && cap.StatusCode < 300
	return []llm.UsageChannelAttempt{{
		Order: 1, ChannelID: ch.Id, BaseURL: bu, Success: ok,
		StatusCode: cap.StatusCode, LatencyMs: cap.WallLatencyMs, TTFTMs: ttft,
	}}
}

func anthropicStreamAttempts(ch models.LLMChannel, cap *llm.AnthropicStreamCapture, streamOK bool) []llm.UsageChannelAttempt {
	bu := models.LLMChannelBaseURLString(&ch)
	if cap == nil {
		return []llm.UsageChannelAttempt{{
			Order: 1, ChannelID: ch.Id, BaseURL: bu, Success: false,
			ErrorCode: "upstream", ErrorMessage: "empty capture",
		}}
	}
	ttft := int64(0)
	if cap.FirstTokenAtMs > 0 && cap.StartedAtMs > 0 {
		ttft = cap.FirstTokenAtMs - cap.StartedAtMs
	}
	ok := streamOK && cap.StatusCode >= 200 && cap.StatusCode < 300
	return []llm.UsageChannelAttempt{{
		Order: 1, ChannelID: ch.Id, BaseURL: bu, Success: ok,
		StatusCode: cap.StatusCode, LatencyMs: cap.WallLatencyMs, TTFTMs: ttft,
	}}
}

// openAPIOpenAIChatCompletionsHandler transparent relay; non-stream retries by channel order.
func (h *Handlers) openAPIOpenAIChatCompletionsHandler(c *gin.Context) {
	cred, ok := middleware.OpenAPILLMCredentialFromContext(c)
	if !ok || cred == nil {
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body", "type": "invalid_request_error"}})
		return
	}
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe)
	accept := strings.TrimSpace(c.GetHeader("Accept"))
	meta := relayUsageMeta(c, cred)

	if probe.Stream {
		channels, err := models.ListLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolOpenAI, models.ExtractJSONModelField(body))
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, models.OpenAINoLLMChannelPayload(cred))
			llm.EmitRelayOpenAIUsageFailure(body, nil, meta, "model_not_found", "No active OpenAI-protocol LLM channel for credential group")
			return
		}
		ch := channels[0]
		up := relayLLMChannelsToUpstream([]models.LLMChannel{ch})[0]
		streamBody := llm.EnsureOpenAIChatStreamIncludeUsage(body)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Minute)
		defer cancel()
		cap, err := llm.RelayOpenAIStreamWithCapture(ctx, streamBody, accept, up, c.Writer)
		streamOK := err == nil && cap != nil && cap.StatusCode >= 200 && cap.StatusCode < 300
		if err != nil {
			if !c.Writer.Written() {
				c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			}
			res := &llm.RelayResult{}
			if cap != nil {
				res.FinalStatus = cap.StatusCode
				res.WallLatencyMs = cap.WallLatencyMs
				res.Attempts = openAIStreamAttempts(ch, cap, false)
			}
			errCode := "upstream_failed"
			if res != nil && len(res.Attempts) > 0 && strings.TrimSpace(res.Attempts[len(res.Attempts)-1].ErrorCode) != "" {
				errCode = res.Attempts[len(res.Attempts)-1].ErrorCode
			}
			llm.EmitRelayOpenAIUsageFailure(streamBody, res, meta, errCode, err.Error())
			return
		}
		if streamOK {
			u := OpenAIUsageNumbers{
				Model:            strings.TrimSpace(cap.Model),
				PromptTokens:     cap.PromptTokens,
				CompletionTokens: cap.CompletionTokens,
				TotalTokens:      cap.TotalTokens,
				CachedPrompt:     cap.CachedPrompt,
			}
			if u.Model == "" {
				u.Model = models.ExtractJSONModelField(streamBody)
			}
			d := QuotaDeltaOpenAI(h.db, u.Model, u, openapiQuotaGroupRatio(cred))
			models.DecrementCredentialAndUserQuota(h.db, cred, d)
			llm.EmitRelayOpenAIStreamUsageSuccess(streamBody, meta, cap, ch.Id, models.LLMChannelBaseURLString(&ch), openAIStreamAttempts(ch, cap, true), d)
		} else {
			extra := "upstream_http"
			if cap != nil && cap.StatusCode > 0 {
				extra = http.StatusText(cap.StatusCode)
			}
			errCode := "upstream_failed"
			if cap != nil && cap.StatusCode > 0 {
				errCode = "upstream_http"
			}
			llm.EmitRelayOpenAIUsageFailure(streamBody, &llm.RelayResult{
				FinalStatus:   cap.StatusCode,
				WallLatencyMs: cap.WallLatencyMs,
				Attempts:      openAIStreamAttempts(ch, cap, false),
			}, meta, errCode, extra)
		}
		return
	}

	channels, err := models.ListLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolOpenAI, models.ExtractJSONModelField(body))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, models.OpenAINoLLMChannelPayload(cred))
		llm.EmitRelayOpenAIUsageFailure(body, nil, meta, "model_not_found", "No active OpenAI-protocol LLM channel for credential group")
		return
	}

	upstreams := relayLLMChannelsToUpstream(channels)
	overallStart := time.Now()
	var attempts []llm.UsageChannelAttempt
	var queueAccum int64
	var res *llm.RelayResult
	for i := range upstreams {
		up := upstreams[i]
		cctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Minute)
		once := llm.RelayOpenAIChatCompletionsOnce(cctx, body, accept, up, i+1)
		cancel()

		attempts = append(attempts, once.Attempt)
		if once.Attempt.Success {
			wall := time.Since(overallStart).Milliseconds()
			res = &llm.RelayResult{
				FinalStatus:   once.Status,
				FinalBody:     once.Body,
				FinalHeader:   once.Header,
				WinChannelID:  up.ID,
				WinBaseURL:    strings.TrimSpace(once.Attempt.BaseURL),
				Attempts:      attempts,
				WallLatencyMs: wall,
				QueueMs:       queueAccum,
				WinHopMs:      once.Attempt.TTFTMs,
				AllFailed:     false,
			}
			break
		}
		queueAccum += once.Attempt.LatencyMs
	}
	if res == nil {
		wall := time.Since(overallStart).Milliseconds()
		lastMsg := "all upstream channels failed"
		if n := len(attempts); n > 0 && strings.TrimSpace(attempts[n-1].ErrorMessage) != "" {
			lastMsg = attempts[n-1].ErrorMessage
		}
		failBody, _ := json.Marshal(map[string]any{
			"error": map[string]any{
				"message": lastMsg,
				"type":    "api_error",
				"code":    "all_channels_exhausted",
			},
		})
		res = &llm.RelayResult{
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
	llm.CopyRelayResponseHeaders(c.Writer.Header(), res.FinalHeader)
	c.Status(res.FinalStatus)
	_, _ = c.Writer.Write(res.FinalBody)

	if !res.AllFailed && res.FinalStatus >= 200 && res.FinalStatus < 300 {
		u := parseOpenAIUsageFromResponseJSON(res.FinalBody)
		if strings.TrimSpace(u.Model) == "" {
			u.Model = models.ExtractJSONModelField(body)
		}
		d := QuotaDeltaOpenAI(h.db, u.Model, u, openapiQuotaGroupRatio(cred))
		models.DecrementCredentialAndUserQuota(h.db, cred, d)
		llm.EmitRelayOpenAIUsageSuccess(body, res, meta, d)
	} else {
		errCode := "all_channels_exhausted"
		if res != nil && res.AllFailed == false && len(res.Attempts) > 0 && strings.TrimSpace(res.Attempts[len(res.Attempts)-1].ErrorCode) != "" {
			errCode = res.Attempts[len(res.Attempts)-1].ErrorCode
		}
		llm.EmitRelayOpenAIUsageFailure(body, res, meta, errCode, "")
	}
}

// openAPIAnthropicMessagesHandler transparent relay for Anthropic /v1/messages.
func (h *Handlers) openAPIAnthropicMessagesHandler(c *gin.Context) {
	cred, ok := middleware.OpenAPILLMCredentialFromContext(c)
	if !ok || cred == nil {
		return
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"type": "error", "error": gin.H{"type": "invalid_request_error", "message": "invalid body"}})
		return
	}
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe)
	av := strings.TrimSpace(c.GetHeader("anthropic-version"))
	ab := strings.TrimSpace(c.GetHeader("anthropic-beta"))
	meta := relayUsageMeta(c, cred)

	if probe.Stream {
		channels, err := models.ListLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolAnthropic, models.ExtractJSONModelField(body))
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"type":  "error",
				"error": gin.H{"type": "api_error", "message": "No active Anthropic-protocol LLM channel for credential group"},
			})
			llm.EmitRelayAnthropicUsageFailure(body, nil, meta, "model_not_found", "No active Anthropic-protocol LLM channel for credential group")
			return
		}
		ch := channels[0]
		up := relayLLMChannelsToUpstream([]models.LLMChannel{ch})[0]
		ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Minute)
		defer cancel()
		cap, err := llm.RelayAnthropicStreamWithCapture(ctx, body, av, ab, up, c.Writer)
		streamOK := err == nil && cap != nil && cap.StatusCode >= 200 && cap.StatusCode < 300
		if err != nil {
			if !c.Writer.Written() {
				c.JSON(http.StatusBadGateway, gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": err.Error()}})
			}
			res := &llm.RelayResult{}
			if cap != nil {
				res.FinalStatus = cap.StatusCode
				res.WallLatencyMs = cap.WallLatencyMs
				res.Attempts = anthropicStreamAttempts(ch, cap, false)
			}
			errCode := "upstream_failed"
			if res != nil && len(res.Attempts) > 0 && strings.TrimSpace(res.Attempts[len(res.Attempts)-1].ErrorCode) != "" {
				errCode = res.Attempts[len(res.Attempts)-1].ErrorCode
			}
			llm.EmitRelayAnthropicUsageFailure(body, res, meta, errCode, err.Error())
			return
		}
		if streamOK {
			tot := cap.TotalTokens
			if tot == 0 {
				tot = cap.PromptTokens + cap.CompletionTokens
			}
			u := OpenAIUsageNumbers{
				Model:            strings.TrimSpace(cap.Model),
				PromptTokens:     cap.PromptTokens,
				CompletionTokens: cap.CompletionTokens,
				TotalTokens:      tot,
			}
			if u.Model == "" {
				u.Model = models.ExtractJSONModelField(body)
			}
			d := QuotaDeltaOpenAI(h.db, u.Model, u, openapiQuotaGroupRatio(cred))
			models.DecrementCredentialAndUserQuota(h.db, cred, d)
			llm.EmitRelayAnthropicStreamUsageSuccess(body, meta, cap, ch.Id, models.LLMChannelBaseURLString(&ch), anthropicStreamAttempts(ch, cap, true), d)
		} else {
			extra := "upstream_http"
			if cap != nil && cap.StatusCode > 0 {
				extra = http.StatusText(cap.StatusCode)
			}
			errCode := "upstream_failed"
			if cap != nil && cap.StatusCode > 0 {
				errCode = "upstream_http"
			}
			llm.EmitRelayAnthropicUsageFailure(body, &llm.RelayResult{
				FinalStatus:   cap.StatusCode,
				WallLatencyMs: cap.WallLatencyMs,
				Attempts:      anthropicStreamAttempts(ch, cap, false),
			}, meta, errCode, extra)
		}
		return
	}

	channels, err := models.ListLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolAnthropic, models.ExtractJSONModelField(body))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":  "error",
			"error": gin.H{"type": "api_error", "message": "No active Anthropic-protocol LLM channel for credential group"},
		})
		llm.EmitRelayAnthropicUsageFailure(body, nil, meta, "model_not_found", "No active Anthropic-protocol LLM channel for credential group")
		return
	}

	upstreams := relayLLMChannelsToUpstream(channels)
	overallStart := time.Now()
	var attempts []llm.UsageChannelAttempt
	var queueAccum int64
	var res *llm.RelayResult
	for i := range upstreams {
		up := upstreams[i]
		cctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Minute)
		once := llm.RelayAnthropicMessagesOnce(cctx, body, av, ab, up, i+1)
		cancel()

		attempts = append(attempts, once.Attempt)
		if once.Attempt.Success {
			wall := time.Since(overallStart).Milliseconds()
			res = &llm.RelayResult{
				FinalStatus:   once.Status,
				FinalBody:     once.Body,
				FinalHeader:   once.Header,
				WinChannelID:  up.ID,
				WinBaseURL:    strings.TrimSpace(once.Attempt.BaseURL),
				Attempts:      attempts,
				WallLatencyMs: wall,
				QueueMs:       queueAccum,
				WinHopMs:      once.Attempt.TTFTMs,
				AllFailed:     false,
			}
			break
		}
		queueAccum += once.Attempt.LatencyMs
	}
	if res == nil {
		wall := time.Since(overallStart).Milliseconds()
		lastMsg := "all upstream channels failed"
		if n := len(attempts); n > 0 && strings.TrimSpace(attempts[n-1].ErrorMessage) != "" {
			lastMsg = attempts[n-1].ErrorMessage
		}
		failBody, _ := json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "api_error",
				"message": lastMsg,
			},
		})
		res = &llm.RelayResult{
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
	llm.CopyRelayResponseHeaders(c.Writer.Header(), res.FinalHeader)
	c.Status(res.FinalStatus)
	_, _ = c.Writer.Write(res.FinalBody)

	if !res.AllFailed && res.FinalStatus >= 200 && res.FinalStatus < 300 {
		u := ParseAnthropicUsageFromResponseJSON(res.FinalBody, models.ExtractJSONModelField(body))
		d := QuotaDeltaOpenAI(h.db, u.Model, u, openapiQuotaGroupRatio(cred))
		models.DecrementCredentialAndUserQuota(h.db, cred, d)
		llm.EmitRelayAnthropicUsageSuccess(body, res, meta, d)
	} else {
		errCode := "all_channels_exhausted"
		if res != nil && res.AllFailed == false && len(res.Attempts) > 0 && strings.TrimSpace(res.Attempts[len(res.Attempts)-1].ErrorCode) != "" {
			errCode = res.Attempts[len(res.Attempts)-1].ErrorCode
		}
		llm.EmitRelayAnthropicUsageFailure(body, res, meta, errCode, "")
	}
}

// openAPIModelsListHandler GET /v1/models — OpenAI-compatible list for this API key.
func (h *Handlers) openAPIModelsListHandler(c *gin.Context) {
	cred, ok := middleware.OpenAPILLMCredentialFromContext(c)
	if !ok || cred == nil {
		return
	}
	list, err := models.BuildOpenAPIModelListForCredential(h.db, cred)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "api_error",
				"code":    "internal_error",
			},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   list,
	})
}
