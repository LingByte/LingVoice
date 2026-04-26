// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

// LLMModelMeta 模型目录元数据（展示/说明；路由仍以 llm_channels + llm_abilities 为准）。
type LLMModelMeta struct {
	Id              uint   `json:"id" gorm:"primaryKey"`
	ModelName       string `json:"model_name" gorm:"size:255;uniqueIndex;not null"`
	Description     string `json:"description,omitempty" gorm:"type:text"`
	Tags            string `json:"tags,omitempty" gorm:"size:255"`
	Status          int    `json:"status" gorm:"default:1"` // 1 启用展示 0 停用
	IconURL         string `json:"icon_url,omitempty" gorm:"size:512"` // 可选覆盖；空则前端按 vendor / 模型名推断
	Vendor          string `json:"vendor,omitempty" gorm:"size:64;index"` // 如 openai、anthropic、deepseek，便于图标与筛选
	SortOrder       int    `json:"sort_order" gorm:"default:0;index"`
	ContextLength   *int   `json:"context_length,omitempty"`
	MaxOutputTokens *int   `json:"max_output_tokens,omitempty"`
	// QuotaBillingMode：显式 times=按次；其它含空值=按 token 折算（与 new-api 默认按量一致）。
	QuotaBillingMode     string  `json:"quota_billing_mode,omitempty" gorm:"size:16;default:''"`
	QuotaModelRatio      float64 `json:"quota_model_ratio" gorm:"default:1"`           // 全局倍率
	QuotaPromptRatio     float64 `json:"quota_prompt_ratio" gorm:"default:1"`        // 非缓存输入 token 权重
	QuotaCompletionRatio float64 `json:"quota_completion_ratio" gorm:"default:1"`    // 输出 token 权重
	QuotaCacheReadRatio  float64 `json:"quota_cache_read_ratio" gorm:"default:0.25"` // 缓存命中 prompt 相对非缓存的折算（对齐常见「缓存计费」思路）
	CreatedTime          int64   `json:"created_time" gorm:"bigint"`
	UpdatedTime          int64   `json:"updated_time" gorm:"bigint"`
}

func (LLMModelMeta) TableName() string {
	return "llm_model_metas"
}
