// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"gorm.io/gorm"
)

// OpenAIUsageNumbers 从 OpenAI chat completion JSON 解析的用量（含缓存命中 prompt 子集）。
type OpenAIUsageNumbers struct {
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CachedPrompt     int
}

func parseOpenAIUsageFromResponseJSON(raw []byte) OpenAIUsageNumbers {
	var out OpenAIUsageNumbers
	if len(raw) == 0 {
		return out
	}
	var root struct {
		Model string `json:"model"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
			Details          *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return out
	}
	out.Model = strings.TrimSpace(root.Model)
	if root.Usage == nil {
		return out
	}
	out.PromptTokens = root.Usage.PromptTokens
	out.CompletionTokens = root.Usage.CompletionTokens
	out.TotalTokens = root.Usage.TotalTokens
	if root.Usage.Details != nil && root.Usage.Details.CachedTokens > 0 {
		out.CachedPrompt = root.Usage.Details.CachedTokens
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.PromptTokens + out.CompletionTokens
	}
	return out
}

func loadLLMModelMetaForQuota(db *gorm.DB, model string) *models.LLMModelMeta {
	model = strings.TrimSpace(model)
	if model == "" || db == nil {
		return nil
	}
	var m models.LLMModelMeta
	if err := db.Where("model_name = ? AND status = ?", model, 1).First(&m).Error; err != nil {
		return nil
	}
	return &m
}

// ParseAnthropicUsageFromResponseJSON 从 Anthropic message JSON 提取用量（以便共用 QuotaDeltaOpenAI）。
func ParseAnthropicUsageFromResponseJSON(raw []byte, fallbackModel string) OpenAIUsageNumbers {
	var root struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &root)
	m := strings.TrimSpace(root.Model)
	if m == "" {
		m = strings.TrimSpace(fallbackModel)
	}
	in, out := 0, 0
	if root.Usage != nil {
		in = root.Usage.InputTokens
		out = root.Usage.OutputTokens
	}
	return OpenAIUsageNumbers{
		Model:            m,
		PromptTokens:     in,
		CompletionTokens: out,
		TotalTokens:      in + out,
	}
}

// QuotaDeltaOpenAI 根据 llm_model_metas 计算本次应扣「额度单位」，对齐 QuantumNous/new-api 文本按量常见实现：
//   - 分组倍率 groupRatio 对应 new-api 分组倍率（凭证 Credential.Group，配置见 OPENAPI_QUOTA_GROUP_RATIOS）；
//   - token 模式：等价 token = Round(非缓存 prompt×pr + 缓存 prompt×pr×cache_read) + Round(completion×cr)，
//     再计算 Round(等价 token × model_ratio × group_ratio)（与 new-api 先合成 token 再乘 model×group 后 Round 一致）；
//   - times 模式：Round(model_ratio × group_ratio)，乘积 > 0 时至少扣 1；
//   - 上游报告的全无用量时返回 0；倍率乘积非零但舍入为 0 时至少扣 1（与 new-api ratio!=0 && quota<=0 时置 1 同类）。
func QuotaDeltaOpenAI(db *gorm.DB, model string, u OpenAIUsageNumbers, groupRatio float64) int {
	if groupRatio <= 0 {
		groupRatio = 1
	}
	meta := loadLLMModelMetaForQuota(db, model)
	mode := ""
	if meta != nil {
		mode = strings.ToLower(strings.TrimSpace(meta.QuotaBillingMode))
	}
	if mode == "count" {
		mode = "times"
	}
	if mode == "tokens" {
		mode = "token"
	}

	if meta != nil && mode == "times" {
		mr := 1.0
		if meta.QuotaModelRatio > 0 {
			mr = meta.QuotaModelRatio
		}
		combined := mr * groupRatio
		n := int(math.Round(combined))
		if n < 1 && combined > 0 {
			n = 1
		}
		return n
	}

	mr, pr, cr, cachR := 1.0, 1.0, 1.0, 0.25
	if meta != nil {
		if meta.QuotaModelRatio > 0 {
			mr = meta.QuotaModelRatio
		}
		if meta.QuotaPromptRatio > 0 {
			pr = meta.QuotaPromptRatio
		}
		if meta.QuotaCompletionRatio > 0 {
			cr = meta.QuotaCompletionRatio
		}
		if meta.QuotaCacheReadRatio >= 0 {
			cachR = meta.QuotaCacheReadRatio
		}
	}
	promptTok := u.PromptTokens
	if promptTok == 0 && u.TotalTokens > 0 {
		promptTok = u.TotalTokens - u.CompletionTokens
		if promptTok < 0 {
			promptTok = 0
		}
	}
	cached := u.CachedPrompt
	if cached > promptTok {
		cached = promptTok
	}
	if cached < 0 {
		cached = 0
	}
	uncached := promptTok - cached
	if uncached < 0 {
		uncached = 0
	}

	baseEquiv := math.Round(float64(uncached)*pr+float64(cached)*pr*cachR) +
		math.Round(float64(u.CompletionTokens)*cr)
	combined := mr * groupRatio
	raw := baseEquiv * combined
	n := int(math.Round(raw))

	noUsage := promptTok+u.CompletionTokens+u.CachedPrompt == 0 && u.TotalTokens == 0
	if noUsage {
		return 0
	}
	if n < 1 && combined != 0 {
		n = 1
	}
	if n < 1 {
		return 0
	}
	return n
}

// QuotaDeltaForAgentRun Agent 整次会话扣费：无上游 usage 汇总时，显式 token 元数据走最小估算，否则按 times 倍率一笔（均含 groupRatio）。
func QuotaDeltaForAgentRun(db *gorm.DB, model string, groupRatio float64) int {
	if groupRatio <= 0 {
		groupRatio = 1
	}
	meta := loadLLMModelMetaForQuota(db, model)
	if meta != nil && strings.ToLower(strings.TrimSpace(meta.QuotaBillingMode)) == "token" {
		u := OpenAIUsageNumbers{Model: model, PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}
		return QuotaDeltaOpenAI(db, model, u, groupRatio)
	}
	mr := 1.0
	if meta != nil && meta.QuotaModelRatio > 0 {
		mr = meta.QuotaModelRatio
	}
	combined := mr * groupRatio
	n := int(math.Round(combined))
	if n < 1 && combined > 0 {
		n = 1
	}
	return n
}
