// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

// 通知渠道类型（可随业务扩展，发送侧按 Type 解析 ConfigJSON）。
const (
	NotificationChannelTypeEmail = "email"
	NotificationChannelTypeSMS   = "sms"
)

// NotificationChannel 统一描述一种可配置通知出口（当前多为邮件 JSON 配置，后续 SMS 等同表扩展）。
// ConfigJSON 建议与 pkg/notification.MailConfig / 未来 SmsConfig 结构对齐的 JSON 文本，便于反序列化。
type NotificationChannel struct {
	BaseModel

	// Type 渠道大类：email、sms（预留 push、webhook 等可继续加常量）
	Type string `json:"type" gorm:"size:32;not null;index:idx_notify_ch_type_sort,priority:1;comment:渠道类型"`

	// Code 服务端生成的唯一业务键（列表展示，勿手填）
	Code string `json:"code,omitempty" gorm:"size:64;index;comment:渠道编码"`

	// Name 展示名称
	Name string `json:"name" gorm:"size:128;not null;comment:显示名称"`

	// SortOrder 同 Type 下越小越优先（故障转移/轮询顺序）
	SortOrder int `json:"sortOrder" gorm:"not null;default:0;index:idx_notify_ch_type_sort,priority:2;comment:排序权重"`
	// Enabled 是否参与发送
	Enabled bool `json:"enabled" gorm:"not null;default:true;index;comment:是否启用"`
	// ConfigJSON 渠道参数 JSON（email 时可为 MailConfig 字段子集 + provider 等）
	ConfigJSON string `json:"configJson,omitempty" gorm:"type:text;comment:渠道配置 JSON"`
}

// TableName GORM 表名
func (NotificationChannel) TableName() string {
	return "notification_channels"
}

// IsNotificationChannelTypeKnown 用于校验 API 入参
func IsNotificationChannelTypeKnown(t string) bool {
	switch t {
	case NotificationChannelTypeEmail, NotificationChannelTypeSMS:
		return true
	default:
		return false
	}
}
