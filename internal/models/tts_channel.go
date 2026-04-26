// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

// TTSChannel 语音合成上游渠道；厂商、音色、鉴权等差异放在 ConfigJSON（与 Provider 联合解析）。
type TTSChannel struct {
	BaseModel
	Provider   string `json:"provider" gorm:"size:64;not null;index;comment:厂商如 aliyun_cosyvoice、azure、edge"`
	Name       string `json:"name" gorm:"size:128;not null;comment:展示名称"`
	Enabled    bool   `json:"enabled" gorm:"not null;default:true;index;comment:是否启用"`
	Group      string `json:"group" gorm:"size:64;default:'';index;comment:路由分组"`
	SortOrder  int    `json:"sortOrder" gorm:"not null;default:0;index;comment:同组内优先级"`
	ConfigJSON string `json:"configJson,omitempty" gorm:"type:text;comment:厂商相关 JSON 配置"`
}

// TableName GORM 表名
func (TTSChannel) TableName() string {
	return "tts_channels"
}
