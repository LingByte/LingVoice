// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/mailtemplate"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type mailTemplateCreateReq struct {
	Code        string `json:"code" binding:"required,max=64"`
	Name        string `json:"name" binding:"required,max=128"`
	HTMLBody    string `json:"htmlBody" binding:"required"`
	Description string `json:"description" binding:"max=512"`
	Variables   string `json:"variables"`
	Locale      string `json:"locale" binding:"max=32"`
	Enabled     *bool  `json:"enabled"`
}

type mailTemplateUpdateReq struct {
	Name        string `json:"name" binding:"required,max=128"`
	HTMLBody    string `json:"htmlBody" binding:"required"`
	Description string `json:"description" binding:"max=512"`
	Variables   string `json:"variables"`
	Locale      string `json:"locale" binding:"max=32"`
	Enabled     *bool  `json:"enabled"`
}

var mailTemplateVarRe = regexp.MustCompile(`\{\{\s*\.?([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// deriveTemplateVariables 从 HTML / 纯文本中解析 {{.Name}}、{{Name}} 占位符，生成 JSON 数组写入 variables。
func deriveTemplateVariables(html, plain string) string {
	text := html + "\n" + plain
	seen := map[string]struct{}{}
	var names []string
	for _, m := range mailTemplateVarRe.FindAllStringSubmatch(text, -1) {
		if len(m) < 2 {
			continue
		}
		k := m[1]
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		names = append(names, k)
	}
	b, _ := json.Marshal(names)
	return string(b)
}

func (h *Handlers) listMailTemplates(c *gin.Context) {
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	var total int64
	if err := h.db.Model(&models.MailTemplate{}).Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	var list []models.MailTemplate
	if err := h.db.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	response.Success(c, "ok", gin.H{
		"list":      list,
		"total":     total,
		"page":      page,
		"pageSize":  pageSize,
		"totalPage": totalPage,
	})
}

// listMailTemplatePresets returns built-in codes and default subject/HTML for admin UI (must register before GET /:id).
func (h *Handlers) listMailTemplatePresets(c *gin.Context) {
	response.Success(c, "ok", mailtemplate.DefaultPresets())
}

func (h *Handlers) getMailTemplate(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var tpl models.MailTemplate
	if err := h.db.First(&tpl, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "模版不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", tpl)
}

func (h *Handlers) createMailTemplate(c *gin.Context) {
	var req mailTemplateCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	u := models.CurrentUser(c)
	plain := utils.HTMLToPlainText(req.HTMLBody)
	vars := strings.TrimSpace(req.Variables)
	if vars == "" {
		vars = deriveTemplateVariables(req.HTMLBody, plain)
	}
	tpl := models.MailTemplate{
		Code:        req.Code,
		Name:        req.Name,
		HTMLBody:    req.HTMLBody,
		TextBody:    plain,
		Description: req.Description,
		Variables:   vars,
		Locale:      req.Locale,
		Enabled:     true,
	}
	if req.Enabled != nil {
		tpl.Enabled = *req.Enabled
	}
	tpl.SetCreateInfo(operatorFromUser(u))
	if err := h.db.Create(&tpl).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", tpl)
}

func (h *Handlers) updateMailTemplate(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req mailTemplateUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var tpl models.MailTemplate
	if err := h.db.First(&tpl, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "模版不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	u := models.CurrentUser(c)
	plain := utils.HTMLToPlainText(req.HTMLBody)
	vars := strings.TrimSpace(req.Variables)
	if vars == "" {
		vars = deriveTemplateVariables(req.HTMLBody, plain)
	}
	tpl.Name = req.Name
	tpl.HTMLBody = req.HTMLBody
	tpl.TextBody = plain
	tpl.Description = req.Description
	tpl.Variables = vars
	tpl.Locale = req.Locale
	if req.Enabled != nil {
		tpl.Enabled = *req.Enabled
	}
	tpl.SetUpdateInfo(operatorFromUser(u))
	if err := h.db.Save(&tpl).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", tpl)
}

func (h *Handlers) deleteMailTemplate(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	res := h.db.Delete(&models.MailTemplate{}, id)
	if res.Error != nil {
		response.Fail(c, "删除失败", gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		response.FailWithCode(c, 404, "模版不存在", nil)
		return
	}
	response.Success(c, "删除成功", gin.H{"id": id})
}
