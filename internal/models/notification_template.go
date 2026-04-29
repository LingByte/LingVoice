// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"errors"
	"strings"

	"github.com/LingByte/LingVoice/pkg/utils/base"
	"gorm.io/gorm"
)

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

// MailTemplateDerivedTextBody 由 HTML 生成纯文本正文（与发信侧一致）。
func MailTemplateDerivedTextBody(htmlBody string) string {
	return base.HTMLToPlainText(htmlBody)
}

// MailTemplateNormalizeVariables 若 variables 为空则从 HTML/纯文本推导占位符说明 JSON。
func MailTemplateNormalizeVariables(htmlBody, textBody, variables string) string {
	v := strings.TrimSpace(variables)
	if v != "" {
		return v
	}
	return base.DeriveTemplateVariables(htmlBody, textBody)
}

// ApplyMailTemplateHTMLDerivedFields 写入 HTML、派生 TextBody 与 Variables（variables 为空时推导）。
func ApplyMailTemplateHTMLDerivedFields(tpl *MailTemplate, htmlBody, variables string) {
	if tpl == nil {
		return
	}
	plain := MailTemplateDerivedTextBody(htmlBody)
	tpl.HTMLBody = htmlBody
	tpl.TextBody = plain
	tpl.Variables = MailTemplateNormalizeVariables(htmlBody, plain, variables)
}

// CountMailTemplatesByOrg 租户下模版总数。
func CountMailTemplatesByOrg(db *gorm.DB, orgID uint) (int64, error) {
	var n int64
	err := db.Model(&MailTemplate{}).Where("org_id = ?", orgID).Count(&n).Error
	return n, err
}

// ListMailTemplatesByOrg 分页列出租户模版。
func ListMailTemplatesByOrg(db *gorm.DB, orgID uint, offset, limit int) ([]MailTemplate, error) {
	var list []MailTemplate
	err := db.Where("org_id = ?", orgID).Order("id DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, err
}

// GetMailTemplateByOrgAndID 按主键与 org 读取；无行时返回 (nil, gorm.ErrRecordNotFound)。
func GetMailTemplateByOrgAndID(db *gorm.DB, orgID, id uint) (*MailTemplate, error) {
	var tpl MailTemplate
	err := db.Where("org_id = ?", orgID).First(&tpl, id).Error
	if err != nil {
		return nil, err
	}
	return &tpl, nil
}

// GetMailTemplateByID 仅按主键读取（OpenAPI 等在校验 org 前使用）。
func GetMailTemplateByID(db *gorm.DB, id uint) (*MailTemplate, error) {
	var tpl MailTemplate
	err := db.First(&tpl, id).Error
	if err != nil {
		return nil, err
	}
	return &tpl, nil
}

// CreateMailTemplate 插入一行。
func CreateMailTemplate(db *gorm.DB, tpl *MailTemplate) error {
	if tpl == nil {
		return errors.New("nil template")
	}
	return db.Create(tpl).Error
}

// SaveMailTemplate 全量保存已存在的行。
func SaveMailTemplate(db *gorm.DB, tpl *MailTemplate) error {
	if tpl == nil {
		return errors.New("nil template")
	}
	return db.Save(tpl).Error
}

// DeleteMailTemplateByOrgAndID 删除指定租户下的模版；返回影响行数。
func DeleteMailTemplateByOrgAndID(db *gorm.DB, orgID, id uint) (int64, error) {
	res := db.Where("org_id = ?", orgID).Delete(&MailTemplate{}, id)
	return res.RowsAffected, res.Error
}
