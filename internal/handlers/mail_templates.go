// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/mailtemplate"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (h *Handlers) registerMailTemplatesRoutes(api *gin.RouterGroup) {
	mt := api.Group("/mail-templates")
	{
		mt.POST("/translate", h.mailTemplateTranslateHandler)
		mt.GET("/presets", h.mailTemplatePresetsHandler)
		mt.GET("", h.mailTemplatesListHandler)
		mt.POST("", h.mailTemplateCreateHandler)
		mt.GET("/:id", h.mailTemplateDetailHandler)
		mt.PUT("/:id", h.mailTemplateUpdateHandler)
		mt.DELETE("/:id", h.mailTemplateDeleteHandler)
	}
}

type MailTemplateCreateReq struct {
	Code        string `json:"code" binding:"required,max=64"`
	Name        string `json:"name" binding:"required,max=128"`
	HTMLBody    string `json:"htmlBody" binding:"required"`
	Description string `json:"description" binding:"max=512"`
	Variables   string `json:"variables"`
	Locale      string `json:"locale" binding:"max=32"`
	Enabled     *bool  `json:"enabled"`
}

type MailTemplateUpdateReq struct {
	Name        string `json:"name" binding:"required,max=128"`
	HTMLBody    string `json:"htmlBody" binding:"required"`
	Description string `json:"description" binding:"max=512"`
	Variables   string `json:"variables"`
	Locale      string `json:"locale" binding:"max=32"`
	Enabled     *bool  `json:"enabled"`
}

type TranslateMailTemplateReq struct {
	FromLocale  string `json:"fromLocale" binding:"required"`
	ToLocale    string `json:"toLocale" binding:"required"`
	Name        string `json:"name"`
	HTMLBody    string `json:"htmlBody"`
	Description string `json:"description"`
}

type TranslateMailTemplateResp struct {
	Name        string `json:"name"`
	HTMLBody    string `json:"htmlBody"`
	TextBody    string `json:"textBody"`
	Description string `json:"description"`
}

func (h *Handlers) mailTemplatesListHandler(c *gin.Context) {
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	var total int64
	orgID := currentOrgID(c)
	if err := h.db.Model(&models.MailTemplate{}).Where("org_id = ?", orgID).Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	var list []models.MailTemplate
	if err := h.db.Where("org_id = ?", orgID).Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
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
func (h *Handlers) mailTemplatePresetsHandler(c *gin.Context) {
	response.Success(c, "ok", mailtemplate.DefaultPresets())
}

func (h *Handlers) mailTemplateDetailHandler(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var tpl models.MailTemplate
	orgID := currentOrgID(c)
	if err := h.db.Where("org_id = ?", orgID).First(&tpl, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "模版不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", tpl)
}

func (h *Handlers) mailTemplateCreateHandler(c *gin.Context) {
	var req MailTemplateCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	u := models.CurrentUser(c)
	orgID := currentOrgID(c)
	plain := utils.HTMLToPlainText(req.HTMLBody)
	vars := strings.TrimSpace(req.Variables)
	if vars == "" {
		vars = utils.DeriveTemplateVariables(req.HTMLBody, plain)
	}
	tpl := models.MailTemplate{
		OrgID:       orgID,
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

func (h *Handlers) mailTemplateUpdateHandler(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req MailTemplateUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var tpl models.MailTemplate
	orgID := currentOrgID(c)
	if err := h.db.Where("org_id = ?", orgID).First(&tpl, id).Error; err != nil {
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
		vars = utils.DeriveTemplateVariables(req.HTMLBody, plain)
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

func (h *Handlers) mailTemplateDeleteHandler(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := currentOrgID(c)
	res := h.db.Where("org_id = ?", orgID).Delete(&models.MailTemplate{}, id)
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

// translateMailTemplate calls MyMemory (see pkg/utils/translator.go) to translate template fields.
func (h *Handlers) mailTemplateTranslateHandler(c *gin.Context) {
	var req TranslateMailTemplateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	from := strings.TrimSpace(req.FromLocale)
	to := strings.TrimSpace(req.ToLocale)
	if to == "" {
		response.FailWithCode(c, 400, "toLocale 不能为空（请选择模版目标语言）", nil)
		return
	}
	if from == to {
		html := req.HTMLBody
		response.Success(c, "ok", TranslateMailTemplateResp{
			Name:        req.Name,
			HTMLBody:    html,
			TextBody:    utils.HTMLToPlainText(html),
			Description: req.Description,
		})
		return
	}

	email := ""
	if config.GlobalConfig != nil {
		email = strings.TrimSpace(config.GlobalConfig.Services.Translation.MyMemoryEmail)
	}
	tr := utils.NewMyMemoryTranslator(email)

	const maxShort = 450
	const maxHTMLChunk = 380
	const pause = 120 * time.Millisecond

	out := TranslateMailTemplateResp{
		Name:        req.Name,
		HTMLBody:    req.HTMLBody,
		TextBody:    utils.HTMLToPlainText(req.HTMLBody),
		Description: req.Description,
	}

	var err error
	if strings.TrimSpace(req.Name) != "" {
		out.Name, err = utils.TranslateLong(tr, req.Name, from, to, maxShort, pause)
		if err != nil {
			logger.Warn("translate name", zap.Error(err))
			response.Fail(c, "翻译失败", gin.H{"error": err.Error()})
			return
		}
	}
	if strings.TrimSpace(req.Description) != "" {
		out.Description, err = utils.TranslateLong(tr, req.Description, from, to, maxShort, pause)
		if err != nil {
			logger.Warn("translate description", zap.Error(err))
			response.Fail(c, "翻译失败", gin.H{"error": err.Error()})
			return
		}
	}
	if strings.TrimSpace(req.HTMLBody) != "" {
		pre, inner, suf := utils.SplitHTMLBodyForTranslation(req.HTMLBody)
		// 仅翻译 <body> 内片段，避免机器翻译破坏 <head>/<style>/<meta> 等。
		translatedInner, err := utils.TranslateLong(tr, inner, from, to, maxHTMLChunk, pause)
		if err != nil {
			logger.Warn("translate html body", zap.Error(err))
			response.Fail(c, "翻译失败", gin.H{"error": err.Error()})
			return
		}
		out.HTMLBody = utils.JoinHTMLBodyAfterTranslation(pre, translatedInner, suf)
	}
	out.TextBody = utils.HTMLToPlainText(out.HTMLBody)

	response.Success(c, "ok", out)
}
