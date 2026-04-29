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

	"github.com/LingByte/LingVoice/internal/config"
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

func openapiQuotaGroupRatio(cred *models.Credential) float64 {
	if cred == nil || config.GlobalConfig == nil {
		return 1
	}
	return config.GlobalConfig.OpenAPIQuotaGroupRatio(cred.Group)
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

func extractRelayModelFromJSONBody(body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return ""
	}
	return strings.TrimSpace(v.Model)
}

// listLLMChannelsForRelay 若凭证分组 + model 在 llm_abilities 中有启用行，则按能力优先级选渠道；否则回退为分组下全量渠道列表。
// model 为空时直接回退。若存在能力定义但无可用渠道（协议/key 等），返回 no_llm_channel。
func listLLMChannelsForRelay(db *gorm.DB, cred *models.Credential, protocol, model string) ([]models.LLMChannel, error) {
	model = strings.TrimSpace(model)
	g := effectiveCredentialLLMGroup(cred)
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = models.LLMChannelProtocolOpenAI
	}
	if model == "" {
		return listLLMChannelsOrdered(db, cred.Group, proto)
	}
	var cnt int64
	if err := db.Model(&models.LLMAbility{}).
		Where("`group` = ? AND model = ? AND enabled = ?", g, model, true).
		Count(&cnt).Error; err != nil {
		return nil, err
	}
	if cnt == 0 {
		return listLLMChannelsOrdered(db, cred.Group, proto)
	}
	var chs []models.LLMChannel
	q := db.Model(&models.LLMChannel{}).
		Joins("INNER JOIN llm_abilities ON llm_abilities.channel_id = llm_channels.id AND llm_abilities.`group` = ? AND llm_abilities.model = ? AND llm_abilities.enabled = ?", g, model, true).
		Where("llm_channels.status = ? AND llm_channels.protocol = ?", 1, proto).
		Order("llm_abilities.priority DESC, llm_abilities.weight DESC, llm_channels.id ASC")
	if err := q.Find(&chs).Error; err != nil {
		return nil, err
	}
	var out []models.LLMChannel
	for i := range chs {
		if strings.TrimSpace(chs[i].Key) != "" {
			out = append(out, chs[i])
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no_llm_channel")
	}
	return out, nil
}

// effectiveCredentialLLMGroup 与 listLLMChannelsOrdered 一致：空分组按 default 选渠道。
func effectiveCredentialLLMGroup(cred *models.Credential) string {
	if cred == nil {
		return "default"
	}
	g := strings.TrimSpace(cred.Group)
	if g == "" {
		return "default"
	}
	return g
}

// openAINoLLMChannelResponse 503：凭证能鉴权，但当前分组下没有可用的 OpenAI 协议 LLM 渠道。
func openAINoLLMChannelResponse(cred *models.Credential) gin.H {
	g := effectiveCredentialLLMGroup(cred)
	return gin.H{
		"error": gin.H{
			"message": "No active OpenAI-protocol LLM channel for credential group",
			"type":    "api_error",
			"code":    "model_not_found",
			"param":   g,
		},
		"credential_group": g,
	}
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

func llmCredDecrementQuota(db *gorm.DB, cred *models.Credential, delta int) {
	if cred == nil || db == nil {
		return
	}
	if delta < 1 {
		return
	}
	if cred.UnlimitedQuota {
		_ = db.Model(&models.Credential{}).Where("id = ?", cred.Id).
			Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error
		return
	}
	_ = db.Model(&models.Credential{}).Where("id = ? AND remain_quota > ?", cred.Id, 0).
		Update("remain_quota", gorm.Expr("remain_quota - ?", delta)).Error
	_ = db.Model(&models.Credential{}).Where("id = ?", cred.Id).
		Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error
}

// llmUserQuotaApply OpenAPI 成功扣减凭证额度后，同步用户表 remain_quota / used_quota（与凭证相同 delta、相同无限策略）。
func llmUserQuotaApply(db *gorm.DB, userID int, delta int) {
	if db == nil || userID <= 0 {
		return
	}
	if delta < 1 {
		return
	}
	var row models.User
	if err := db.Select("id", "unlimited_quota").Where("id = ?", userID).First(&row).Error; err != nil {
		return
	}
	if row.UnlimitedQuota {
		_ = db.Model(&models.User{}).Where("id = ?", userID).
			Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error
		return
	}
	_ = db.Model(&models.User{}).Where("id = ? AND remain_quota > ?", userID, 0).
		Update("remain_quota", gorm.Expr("remain_quota - ?", delta)).Error
	_ = db.Model(&models.User{}).Where("id = ?", userID).
		Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error
}

func llmCredAndUserDecrementQuota(db *gorm.DB, cred *models.Credential, delta int) {
	llmCredDecrementQuota(db, cred, delta)
	llmUserQuotaApply(db, cred.UserId, delta)
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
		channels, err := listLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolOpenAI, extractRelayModelFromJSONBody(body))
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, openAINoLLMChannelResponse(cred))
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
			u := OpenAIUsageNumbers{
				Model:            strings.TrimSpace(cap.Model),
				PromptTokens:     cap.PromptTokens,
				CompletionTokens: cap.CompletionTokens,
				TotalTokens:      cap.TotalTokens,
				CachedPrompt:     cap.CachedPrompt,
			}
			if u.Model == "" {
				u.Model = extractRelayModelFromJSONBody(streamBody)
			}
			d := QuotaDeltaOpenAI(h.db, u.Model, u, openapiQuotaGroupRatio(cred))
			llmCredAndUserDecrementQuota(h.db, cred, d)
			llm.EmitOpenAPIOpenAIUsageStreamSuccess(streamBody, meta, cap, ch.Id, channelBaseURLString(&ch), openAIStreamAttempts(ch, cap, true), d)
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

	channels, err := listLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolOpenAI, extractRelayModelFromJSONBody(body))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, openAINoLLMChannelResponse(cred))
		llm.EmitOpenAPIOpenAIUsageFailure(body, nil, meta, "No active OpenAI-protocol LLM channel for credential group")
		return
	}
	res := llm.ProxyOpenAINonStream(c.Request.Context(), body, accept, llmChannelsToUpstream(channels))
	llm.CopyOpenAPIProxyResponseHeaders(c.Writer.Header(), res.FinalHeader)
	c.Status(res.FinalStatus)
	_, _ = c.Writer.Write(res.FinalBody)

	if !res.AllFailed && res.FinalStatus >= 200 && res.FinalStatus < 300 {
		u := parseOpenAIUsageFromResponseJSON(res.FinalBody)
		if strings.TrimSpace(u.Model) == "" {
			u.Model = extractRelayModelFromJSONBody(body)
		}
		d := QuotaDeltaOpenAI(h.db, u.Model, u, openapiQuotaGroupRatio(cred))
		llmCredAndUserDecrementQuota(h.db, cred, d)
		llm.EmitOpenAPIOpenAIUsageSuccess(body, res, meta, d)
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
		channels, err := listLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolAnthropic, extractRelayModelFromJSONBody(body))
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
				u.Model = extractRelayModelFromJSONBody(body)
			}
			d := QuotaDeltaOpenAI(h.db, u.Model, u, openapiQuotaGroupRatio(cred))
			llmCredAndUserDecrementQuota(h.db, cred, d)
			llm.EmitOpenAPIAnthropicUsageStreamSuccess(body, meta, cap, ch.Id, channelBaseURLString(&ch), anthropicStreamAttempts(ch, cap, true), d)
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

	channels, err := listLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolAnthropic, extractRelayModelFromJSONBody(body))
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
		u := ParseAnthropicUsageFromResponseJSON(res.FinalBody, extractRelayModelFromJSONBody(body))
		d := QuotaDeltaOpenAI(h.db, u.Model, u, openapiQuotaGroupRatio(cred))
		llmCredAndUserDecrementQuota(h.db, cred, d)
		llm.EmitOpenAPIAnthropicUsageSuccess(body, res, meta, d)
	} else {
		llm.EmitOpenAPIAnthropicUsageFailure(body, res, meta, "")
	}
}
