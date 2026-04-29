// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

// MailTemplate 是可持久化的邮件模版，供渲染主题/正文（占位符由业务层注入）。
// 通知渠道当前以邮件为主；预留 Locale 便于多语言模版行。
type MailTemplate struct {
	BaseModel
	OrgID       uint   `json:"orgId" gorm:"uniqueIndex:idx_mail_tpl_org_code_locale;not null;default:0;comment:tenant organization id"`
	Code        string `json:"code" gorm:"uniqueIndex:idx_mail_tpl_org_code_locale;size:64;not null;comment:模版编码"`
	Name        string `json:"name" gorm:"size:128;not null;comment:模版名称"`
	HTMLBody    string `json:"htmlBody" gorm:"type:longtext;comment:HTML 正文"`
	TextBody    string `json:"textBody,omitempty" gorm:"type:longtext;comment:纯文本正文"`
	Description string `json:"description,omitempty" gorm:"size:512;comment:说明"`
	Variables   string `json:"variables,omitempty" gorm:"type:text;comment:占位符说明 JSON"`
	Locale      string `json:"locale,omitempty" gorm:"uniqueIndex:idx_mail_tpl_org_code_locale;size:32;default:'';comment:语言如 zh-CN"`
	Enabled     bool   `json:"enabled" gorm:"default:true;index;comment:是否启用"`
}

// TableName GORM 表名
func (MailTemplate) TableName() string {
	return "mail_templates"
}
