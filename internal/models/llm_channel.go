package models

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

// MultiKeyMode 多 Key 调度方式（仅 LLM 渠道使用）。
type MultiKeyMode string

const (
	MultiKeyModeRandom  MultiKeyMode = "random"  // 随机
	MultiKeyModePolling MultiKeyMode = "polling" // 轮询
)

// LLMChannel 大模型上游渠道（OpenAI 兼容、自建网关等），与 ASR/TTS 分表。
type LLMChannel struct {
	Id                 int            `json:"id"`
	Type               int            `json:"type" gorm:"default:0"`
	Key                string         `json:"key" gorm:"not null"`
	OpenAIOrganization *string        `json:"openai_organization"`
	TestModel          *string        `json:"test_model"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index"`
	Weight             *uint          `json:"weight" gorm:"default:0"`
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	TestTime           int64          `json:"test_time" gorm:"bigint"`
	ResponseTime       int            `json:"response_time"` // in milliseconds
	BaseURL            *string        `json:"base_url" gorm:"column:base_url;default:''"`
	Other              string         `json:"other"`
	Balance            float64        `json:"balance"` // in USD
	BalanceUpdatedTime int64          `json:"balance_updated_time" gorm:"bigint"`
	Models             string         `json:"models"`
	Group              string         `json:"group" gorm:"type:varchar(64);default:'default'"`
	UsedQuota          int64          `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       *string        `json:"model_mapping" gorm:"type:text"`
	StatusCodeMapping  *string        `json:"status_code_mapping" gorm:"type:varchar(1024);default:''"`
	Priority           *int64         `json:"priority" gorm:"bigint;default:0"`
	AutoBan            *int           `json:"auto_ban" gorm:"default:1"`
	OtherInfo          string         `json:"other_info"`
	Tag                *string        `json:"tag" gorm:"index"`
	Setting            *string        `json:"setting" gorm:"type:text"` // 渠道额外设置
	ParamOverride      *string        `json:"param_override" gorm:"type:text"`
	HeaderOverride     *string        `json:"header_override" gorm:"type:text"`
	Remark             *string        `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	ChannelInfo        LLMChannelInfo `json:"channel_info" gorm:"column:channel_info;type:json"`
	OtherSettings      string         `json:"settings" gorm:"column:settings"`
	Keys               []string       `json:"-" gorm:"-"`
}

// LLMChannelInfo 多 Key 与轮询状态（仅 LLM）。
type LLMChannelInfo struct {
	IsMultiKey             bool           `json:"is_multi_key"`
	MultiKeySize           int            `json:"multi_key_size"`
	MultiKeyStatusList     map[int]int    `json:"multi_key_status_list"`
	MultiKeyDisabledReason map[int]string `json:"multi_key_disabled_reason,omitempty"`
	MultiKeyDisabledTime   map[int]int64  `json:"multi_key_disabled_time,omitempty"`
	MultiKeyPollingIndex   int            `json:"multi_key_polling_index"`
	MultiKeyMode           MultiKeyMode   `json:"multi_key_mode"`
}

// TableName GORM 表名（与 ASR/TTS 分表）。
func (LLMChannel) TableName() string {
	return "llm_channels"
}
