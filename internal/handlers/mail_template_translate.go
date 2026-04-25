// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/config"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type translateMailTemplateReq struct {
	FromLocale  string `json:"fromLocale" binding:"required"`
	ToLocale    string `json:"toLocale" binding:"required"`
	Name        string `json:"name"`
	HTMLBody    string `json:"htmlBody"`
	Description string `json:"description"`
}

type translateMailTemplateResp struct {
	Name        string `json:"name"`
	HTMLBody    string `json:"htmlBody"`
	TextBody    string `json:"textBody"`
	Description string `json:"description"`
}

// translateMailTemplate calls MyMemory (see pkg/utils/translator.go) to translate template fields.
// Plain text is always regenerated from translated HTML (strip tags).
func (h *Handlers) translateMailTemplate(c *gin.Context) {
	var req translateMailTemplateReq
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
		response.Success(c, "ok", translateMailTemplateResp{
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

	out := translateMailTemplateResp{
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
