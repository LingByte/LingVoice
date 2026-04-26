// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

// LLMUsageUserDaily 用户 + UTC 日期维度的用量汇总（listener 增量 upsert；面板按区间 SUM，避免每次扫 llm_usage 全表）。
type LLMUsageUserDaily struct {
	ID           uint   `json:"id" gorm:"primaryKey"`
	UserID       string `json:"user_id" gorm:"size:64;uniqueIndex:ux_llm_usage_user_daily;not null;index"`
	StatDate     string `json:"stat_date" gorm:"size:10;uniqueIndex:ux_llm_usage_user_daily;not null"` // YYYY-MM-DD UTC
	RequestCount int64  `json:"request_count" gorm:"default:0"`
	SuccessCount int64  `json:"success_count" gorm:"default:0"`
	TokenSum     int64  `json:"token_sum" gorm:"default:0"`
	QuotaSum     int64  `json:"quota_sum" gorm:"default:0"`
}

func (LLMUsageUserDaily) TableName() string { return "llm_usage_user_daily" }

// LLMUsageUserModelDaily 用户 + 日期 + 模型维度的汇总（用于面板模型榜等）。
type LLMUsageUserModelDaily struct {
	ID           uint   `json:"id" gorm:"primaryKey"`
	UserID       string `json:"user_id" gorm:"size:64;uniqueIndex:ux_llm_usage_um_daily;not null;index"`
	StatDate     string `json:"stat_date" gorm:"size:10;uniqueIndex:ux_llm_usage_um_daily;not null"`
	Model        string `json:"model" gorm:"size:255;uniqueIndex:ux_llm_usage_um_daily;not null"`
	RequestCount int64  `json:"request_count" gorm:"default:0"`
	SuccessCount int64  `json:"success_count" gorm:"default:0"`
	TokenSum     int64  `json:"token_sum" gorm:"default:0"`
	QuotaSum     int64  `json:"quota_sum" gorm:"default:0"`
}

func (LLMUsageUserModelDaily) TableName() string { return "llm_usage_user_model_daily" }
