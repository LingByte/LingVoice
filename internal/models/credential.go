package models

import (
	"strings"

	"github.com/LingByte/LingVoice/pkg/constants"
	"gorm.io/gorm"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

// 凭证服务类型：同一套「密钥 + 配额 + IP 限制」承载多种上游能力。
const (
	CredentialKindLLM   = "llm"   // 大模型 API Key；ModelLimits* 字段主要对此类生效
	CredentialKindASR   = "asr"   // 语音识别
	CredentialKindTTS   = "tts"   // 语音合成
	CredentialKindEmail = "email" // 邮件类第三方 API Key 等
)

// Credential 用户侧可调用的访问凭证。
// 各 kind 专有字段优先放在 ExtraJSON（JSON 文本）。
type Credential struct {
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id" gorm:"index:idx_credential_user_kind"`
	Kind               string         `json:"kind" gorm:"size:16;index:idx_credential_user_kind;default:llm"`
	Key                string         `json:"key" gorm:"type:char(48);uniqueIndex"`
	Status             int            `json:"status" gorm:"default:1"` // 1 启用 0 禁用
	Name               string         `json:"name" gorm:"index"`
	ExtraJSON          string         `json:"extra,omitempty" gorm:"column:extra_json;type:text"`
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	AccessedTime       int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 永不过期
	RemainQuota        int            `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool           `json:"unlimited_quota"`
	UsedQuota          int            `json:"used_quota" gorm:"default:0"`
	ModelLimitsEnabled bool           `json:"model_limits_enabled"`
	ModelLimits        string         `json:"model_limits" gorm:"type:text"`
	AllowIps           *string        `json:"allow_ips" gorm:"default:''"`
	Group              string         `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool           `json:"cross_group_retry"`
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

// TableName 与历史常量一致。
func (Credential) TableName() string {
	return constants.USER_CREDENTIAL_TABLE_NAME
}

func MaskTokenKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	if len(key) <= 8 {
		return key[:2] + "****" + key[len(key)-2:]
	}
	return key[:4] + "**********" + key[len(key)-4:]
}
