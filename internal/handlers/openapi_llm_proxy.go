// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func credentialUserIDString(userID int) string {
	if userID <= 0 {
		return ""
	}
	return strconv.Itoa(userID)
}

func listLLMChannelsOrdered(db *gorm.DB, group, protocol string) ([]models.LLMChannel, error) {
	g := strings.TrimSpace(group)
	if g == "" {
		g = "default"
	}
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = models.LLMChannelProtocolOpenAI
	}
	var list []models.LLMChannel
	q := db.Where("status = ? AND protocol = ? AND `group` = ?", 1, proto, g).
		Order("(CASE WHEN priority IS NULL THEN 0 ELSE priority END) DESC").Order("id ASC")
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	var out []models.LLMChannel
	for i := range list {
		if strings.TrimSpace(list[i].Key) != "" {
			out = append(out, list[i])
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no_llm_channel")
	}
	return out, nil
}

func llmChannelsToUpstream(chs []models.LLMChannel) []llm.UpstreamChannel {
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

func openapiUsageEmitMeta(c *gin.Context, cred *models.Credential) llm.OpenAPIUsageEmitMeta {
	return llm.OpenAPIUsageEmitMeta{
		UserIDStr: credentialUserIDString(cred.UserId),
		UserAgent: c.Request.UserAgent(),
		ClientIP:  c.ClientIP(),
	}
}

func channelBaseURLString(ch *models.LLMChannel) string {
	if ch == nil || ch.BaseURL == nil {
		return ""
	}
	return strings.TrimSpace(*ch.BaseURL)
}

func openAIStreamAttempts(ch models.LLMChannel, cap *llm.OpenAIStreamCapture, streamOK bool) []llm.UsageChannelAttempt {
	bu := channelBaseURLString(&ch)
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
	bu := channelBaseURLString(&ch)
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

func llmCredDecrementQuota(db *gorm.DB, cred *models.Credential) {
	if cred == nil || db == nil {
		return
	}
	if cred.UnlimitedQuota {
		_ = db.Model(&models.Credential{}).Where("id = ?", cred.Id).
			Update("used_quota", gorm.Expr("used_quota + ?", 1)).Error
		return
	}
	_ = db.Model(&models.Credential{}).Where("id = ? AND remain_quota > ?", cred.Id, 0).
		Update("remain_quota", gorm.Expr("remain_quota - ?", 1)).Error
	_ = db.Model(&models.Credential{}).Where("id = ?", cred.Id).
		Update("used_quota", gorm.Expr("used_quota + ?", 1)).Error
}

// openAPIOpenAIChatCompletions 透明转发；非流式按渠道优先级重试并记录 channel_attempts。
func (h *Handlers) openAPIOpenAIChatCompletions(c *gin.Context) {
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
	meta := openapiUsageEmitMeta(c, cred)

	if probe.Stream {
		channels, err := listLLMChannelsOrdered(h.db, cred.Group, models.LLMChannelProtocolOpenAI)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{
					"message": "No active OpenAI-protocol LLM channel for credential group",
					"type":    "api_error",
					"code":    "model_not_found",
				},
			})
			llm.EmitOpenAPIOpenAIUsageFailure(body, nil, meta, "No active OpenAI-protocol LLM channel for credential group")
			return
		}
		ch := channels[0]
		up := llmChannelsToUpstream([]models.LLMChannel{ch})[0]
		streamBody := llm.EnsureOpenAIChatStreamIncludeUsage(body)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Minute)
		defer cancel()
		cap, err := llm.ProxyOpenAIStreamWithCapture(ctx, streamBody, accept, up, c.Writer)
		streamOK := err == nil && cap != nil && cap.StatusCode >= 200 && cap.StatusCode < 300
		if err != nil {
			if !c.Writer.Written() {
				c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
			}
			res := &llm.OpenAPIProxyResult{}
			if cap != nil {
				res.FinalStatus = cap.StatusCode
				res.WallLatencyMs = cap.WallLatencyMs
				res.Attempts = openAIStreamAttempts(ch, cap, false)
			}
			llm.EmitOpenAPIOpenAIUsageFailure(streamBody, res, meta, err.Error())
			return
		}
		if streamOK {
			llmCredDecrementQuota(h.db, cred)
			llm.EmitOpenAPIOpenAIUsageStreamSuccess(streamBody, meta, cap, ch.Id, channelBaseURLString(&ch), openAIStreamAttempts(ch, cap, true))
		} else {
			extra := "upstream_http"
			if cap != nil && cap.StatusCode > 0 {
				extra = http.StatusText(cap.StatusCode)
			}
			llm.EmitOpenAPIOpenAIUsageFailure(streamBody, &llm.OpenAPIProxyResult{
				FinalStatus:   cap.StatusCode,
				WallLatencyMs: cap.WallLatencyMs,
				Attempts:      openAIStreamAttempts(ch, cap, false),
			}, meta, extra)
		}
		return
	}

	channels, err := listLLMChannelsOrdered(h.db, cred.Group, models.LLMChannelProtocolOpenAI)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"message": "No active OpenAI-protocol LLM channel for credential group",
				"type":    "api_error",
				"code":    "model_not_found",
			},
		})
		llm.EmitOpenAPIOpenAIUsageFailure(body, nil, meta, "No active OpenAI-protocol LLM channel for credential group")
		return
	}
	res := llm.ProxyOpenAINonStream(c.Request.Context(), body, accept, llmChannelsToUpstream(channels))
	llm.CopyOpenAPIProxyResponseHeaders(c.Writer.Header(), res.FinalHeader)
	c.Status(res.FinalStatus)
	_, _ = c.Writer.Write(res.FinalBody)

	if !res.AllFailed && res.FinalStatus >= 200 && res.FinalStatus < 300 {
		llmCredDecrementQuota(h.db, cred)
		llm.EmitOpenAPIOpenAIUsageSuccess(body, res, meta)
	} else {
		llm.EmitOpenAPIOpenAIUsageFailure(body, res, meta, "")
	}
}

// openAPIAnthropicMessages 透明转发；非流式按渠道优先级重试。
func (h *Handlers) openAPIAnthropicMessages(c *gin.Context) {
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
	meta := openapiUsageEmitMeta(c, cred)

	if probe.Stream {
		channels, err := listLLMChannelsOrdered(h.db, cred.Group, models.LLMChannelProtocolAnthropic)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"type":  "error",
				"error": gin.H{"type": "api_error", "message": "No active Anthropic-protocol LLM channel for credential group"},
			})
			llm.EmitOpenAPIAnthropicUsageFailure(body, nil, meta, "No active Anthropic-protocol LLM channel for credential group")
			return
		}
		ch := channels[0]
		up := llmChannelsToUpstream([]models.LLMChannel{ch})[0]
		ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Minute)
		defer cancel()
		cap, err := llm.ProxyAnthropicStreamWithCapture(ctx, body, av, ab, up, c.Writer)
		streamOK := err == nil && cap != nil && cap.StatusCode >= 200 && cap.StatusCode < 300
		if err != nil {
			if !c.Writer.Written() {
				c.JSON(http.StatusBadGateway, gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": err.Error()}})
			}
			res := &llm.OpenAPIProxyResult{}
			if cap != nil {
				res.FinalStatus = cap.StatusCode
				res.WallLatencyMs = cap.WallLatencyMs
				res.Attempts = anthropicStreamAttempts(ch, cap, false)
			}
			llm.EmitOpenAPIAnthropicUsageFailure(body, res, meta, err.Error())
			return
		}
		if streamOK {
			llmCredDecrementQuota(h.db, cred)
			llm.EmitOpenAPIAnthropicUsageStreamSuccess(body, meta, cap, ch.Id, channelBaseURLString(&ch), anthropicStreamAttempts(ch, cap, true))
		} else {
			extra := "upstream_http"
			if cap != nil && cap.StatusCode > 0 {
				extra = http.StatusText(cap.StatusCode)
			}
			llm.EmitOpenAPIAnthropicUsageFailure(body, &llm.OpenAPIProxyResult{
				FinalStatus:   cap.StatusCode,
				WallLatencyMs: cap.WallLatencyMs,
				Attempts:      anthropicStreamAttempts(ch, cap, false),
			}, meta, extra)
		}
		return
	}

	channels, err := listLLMChannelsOrdered(h.db, cred.Group, models.LLMChannelProtocolAnthropic)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":  "error",
			"error": gin.H{"type": "api_error", "message": "No active Anthropic-protocol LLM channel for credential group"},
		})
		llm.EmitOpenAPIAnthropicUsageFailure(body, nil, meta, "No active Anthropic-protocol LLM channel for credential group")
		return
	}
	res := llm.ProxyAnthropicNonStream(c.Request.Context(), body, av, ab, llmChannelsToUpstream(channels))
	llm.CopyOpenAPIProxyResponseHeaders(c.Writer.Header(), res.FinalHeader)
	c.Status(res.FinalStatus)
	_, _ = c.Writer.Write(res.FinalBody)

	if !res.AllFailed && res.FinalStatus >= 200 && res.FinalStatus < 300 {
		llmCredDecrementQuota(h.db, cred)
		llm.EmitOpenAPIAnthropicUsageSuccess(body, res, meta)
	} else {
		llm.EmitOpenAPIAnthropicUsageFailure(body, res, meta, "")
	}
}
