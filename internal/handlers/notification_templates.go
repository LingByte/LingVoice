// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (h *Handlers) registerMailTemplatesRoutes(api *gin.RouterGroup) {
	mt := api.Group("/mail-templates")
	{
		mt.POST("/translate", h.mailTemplateTranslateHandler)
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
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	orgID := models.CurrentOrgID(c)
	total, err := models.CountMailTemplatesByOrg(h.db, orgID)
	if err != nil {
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	list, err := models.ListMailTemplatesByOrg(h.db, orgID, offset, pageSize)
	if err != nil {
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	response.SuccessOK(c, gin.H{
		"list":      list,
		"total":     total,
		"page":      page,
		"pageSize":  pageSize,
		"totalPage": totalPage,
	})
}

func (h *Handlers) mailTemplateDetailHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, response.Msg(c, "无效的 id"), nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	tpl, err := models.GetMailTemplateByOrgAndID(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "模版不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	response.SuccessOK(c, tpl)
}

func (h *Handlers) mailTemplateCreateHandler(c *gin.Context) {
	var req MailTemplateCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "参数错误"), gin.H{"error": err.Error()})
		return
	}
	u := models.CurrentUser(c)
	orgID := models.CurrentOrgID(c)
	tpl := models.MailTemplate{
		OrgID:       orgID,
		Code:        req.Code,
		Name:        req.Name,
		Description: req.Description,
		Locale:      req.Locale,
		Enabled:     true,
	}
	models.ApplyMailTemplateHTMLDerivedFields(&tpl, req.HTMLBody, req.Variables)
	if req.Enabled != nil {
		tpl.Enabled = *req.Enabled
	}
	tpl.SetCreateInfo(models.OperatorFromUser(u))
	if err := models.CreateMailTemplate(h.db, &tpl); err != nil {
		response.Fail(c, response.Msg(c, "创建失败"), gin.H{"error": err.Error()})
		return
	}
	response.Success(c, response.Msg(c, "创建成功"), tpl)
}

func (h *Handlers) mailTemplateUpdateHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, response.Msg(c, "无效的 id"), nil)
		return
	}
	var req MailTemplateUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "参数错误"), gin.H{"error": err.Error()})
		return
	}
	orgID := models.CurrentOrgID(c)
	tpl, err := models.GetMailTemplateByOrgAndID(h.db, orgID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "模版不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	u := models.CurrentUser(c)
	tpl.Name = req.Name
	tpl.Description = req.Description
	tpl.Locale = req.Locale
	models.ApplyMailTemplateHTMLDerivedFields(tpl, req.HTMLBody, req.Variables)
	if req.Enabled != nil {
		tpl.Enabled = *req.Enabled
	}
	tpl.SetUpdateInfo(models.OperatorFromUser(u))
	if err := models.SaveMailTemplate(h.db, tpl); err != nil {
		response.Fail(c, response.Msg(c, "更新失败"), gin.H{"error": err.Error()})
		return
	}
	response.Success(c, response.Msg(c, "更新成功"), tpl)
}

func (h *Handlers) mailTemplateDeleteHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, response.Msg(c, "无效的 id"), nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	n, err := models.DeleteMailTemplateByOrgAndID(h.db, orgID, id)
	if err != nil {
		response.Fail(c, response.Msg(c, "删除失败"), gin.H{"error": err.Error()})
		return
	}
	if n == 0 {
		response.FailWithCode(c, 404, response.Msg(c, "模版不存在"), nil)
		return
	}
	response.Success(c, response.Msg(c, "删除成功"), gin.H{"id": id})
}

// translateMailTemplate calls MyMemory (see pkg/utils/translator.go) to translate template fields.
func (h *Handlers) mailTemplateTranslateHandler(c *gin.Context) {
	var req TranslateMailTemplateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "参数错误"), gin.H{"error": err.Error()})
		return
	}
	from := strings.TrimSpace(req.FromLocale)
	to := strings.TrimSpace(req.ToLocale)
	if to == "" {
		response.FailWithCode(c, 400, response.Msg(c, "toLocale 不能为空（请选择模版目标语言）"), nil)
		return
	}
	if from == to {
		html := req.HTMLBody
		response.SuccessOK(c, TranslateMailTemplateResp{
			Name:        req.Name,
			HTMLBody:    html,
			TextBody:    base.HTMLToPlainText(html),
			Description: req.Description,
		})
		return
	}

	tr := base.NewMyMemoryTranslator()

	const maxShort = 450
	const maxHTMLChunk = 380
	const pause = 120 * time.Millisecond

	out := TranslateMailTemplateResp{
		Name:        req.Name,
		HTMLBody:    req.HTMLBody,
		TextBody:    base.HTMLToPlainText(req.HTMLBody),
		Description: req.Description,
	}

	var err error
	if strings.TrimSpace(req.Name) != "" {
		out.Name, err = base.TranslateLong(tr, req.Name, from, to, maxShort, pause)
		if err != nil {
			logger.Warn("translate name", zap.Error(err))
			response.Fail(c, response.Msg(c, "翻译失败"), gin.H{"error": err.Error()})
			return
		}
	}
	if strings.TrimSpace(req.Description) != "" {
		out.Description, err = base.TranslateLong(tr, req.Description, from, to, maxShort, pause)
		if err != nil {
			logger.Warn("translate description", zap.Error(err))
			response.Fail(c, response.Msg(c, "翻译失败"), gin.H{"error": err.Error()})
			return
		}
	}
	if strings.TrimSpace(req.HTMLBody) != "" {
		pre, inner, suf := base.SplitHTMLBodyForTranslation(req.HTMLBody)
		// 仅翻译 <body> 内片段，避免机器翻译破坏 <head>/<style>/<meta> 等。
		translatedInner, err := base.TranslateLong(tr, inner, from, to, maxHTMLChunk, pause)
		if err != nil {
			logger.Warn("translate html body", zap.Error(err))
			response.Fail(c, response.Msg(c, "翻译失败"), gin.H{"error": err.Error()})
			return
		}
		out.HTMLBody = base.JoinHTMLBodyAfterTranslation(pre, translatedInner, suf)
	}
	out.TextBody = base.HTMLToPlainText(out.HTMLBody)

	response.SuccessOK(c, out)
}
