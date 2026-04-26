// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

// MailTemplate 是可持久化的邮件模版，供渲染主题/正文（占位符由业务层注入）。
// 通知渠道当前以邮件为主；预留 Locale 便于多语言模版行。
type MailTemplate struct {
	BaseModel

	// Code 业务唯一键，用于代码里加载模版，如 welcome、verify_code、password_reset
	Code string `json:"code" gorm:"uniqueIndex:idx_mail_tpl_code_locale;size:64;not null;comment:模版编码"`

	// Name 后台展示用名称
	Name string `json:"name" gorm:"size:128;not null;comment:模版名称"`

	// HTMLBody HTML 正文模版
	HTMLBody string `json:"htmlBody" gorm:"type:longtext;comment:HTML 正文"`

	// TextBody 纯文本正文模版（由服务端根据 HTMLBody 去标签生成，可与 HTML 共用占位符）
	TextBody string `json:"textBody,omitempty" gorm:"type:longtext;comment:纯文本正文"`

	// Description 用途说明，供运营/开发查看
	Description string `json:"description,omitempty" gorm:"size:512;comment:说明"`

	// Variables JSON 数组字符串，描述可用占位符，如 ["Username","VerifyURL"]
	Variables string `json:"variables,omitempty" gorm:"type:text;comment:占位符说明 JSON"`

	// Locale 语言区域；空字符串表示默认模版，与 Code 组成联合唯一
	Locale string `json:"locale,omitempty" gorm:"uniqueIndex:idx_mail_tpl_code_locale;size:32;default:'';comment:语言如 zh-CN"`

	// Enabled 是否启用；禁用后发送逻辑应拒绝或回退到内置默认
	Enabled bool `json:"enabled" gorm:"default:true;index;comment:是否启用"`
}

// TableName GORM 表名
func (MailTemplate) TableName() string {
	return "mail_templates"
}
