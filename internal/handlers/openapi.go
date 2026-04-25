// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/internal/listeners"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/LingByte/LingVoice/pkg/notification"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// openapiSendMailBody 按邮件模版 ID 发送：正文来自模版 HTML，params 的键与模版中 {{.Key}} 对应。
// subject 可选；为空时使用模版名称；若含 {{ 则按 text/template 与 params 渲染。
type openapiSendMailBody struct {
	TemplateID uint           `json:"template_id" binding:"required"`
	To         string         `json:"to" binding:"required"`
	Subject    string         `json:"subject"`
	Params     map[string]any `json:"params"`
}

func (h *Handlers) registerOpenAPIRoutes(api *gin.RouterGroup) {
	og := api.Group("/openapi/v1")
	og.Use(middleware.OpenAPIEmailCredential(h.db))
	{
		og.GET("/mail-templates", h.listMailTemplates)
		og.POST("/mail-templates", h.openAPICreateMailTemplate)
		og.GET("/mail-logs", h.listMailLogs)
		og.GET("/mail-logs/:id", h.getMailLog)
		og.POST("/mail/send", h.openAPISendMail)
	}
}

// openAPICreateMailTemplate 与控制台创建模版逻辑一致，创建人记为 openapi 凭证。
func (h *Handlers) openAPICreateMailTemplate(c *gin.Context) {
	cred, ok := middleware.OpenAPICredentialFromContext(c)
	if !ok || cred == nil {
		response.FailWithCode(c, 401, "未授权", nil)
		return
	}
	var req mailTemplateCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
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
	tpl.SetCreateInfo(fmt.Sprintf("openapi-credential:%d", cred.Id))
	if err := h.db.Create(&tpl).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", tpl)
}

func (h *Handlers) openAPISendMail(c *gin.Context) {
	cred, ok := middleware.OpenAPICredentialFromContext(c)
	if !ok || cred == nil {
		response.FailWithCode(c, 401, "未授权", nil)
		return
	}
	var body openapiSendMailBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	if body.TemplateID == 0 {
		response.FailWithCode(c, 400, "缺少有效的 template_id", nil)
		return
	}
	var tpl models.MailTemplate
	if err := h.db.First(&tpl, body.TemplateID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "模版不存在", nil)
			return
		}
		response.Fail(c, "查询模版失败", gin.H{"error": err.Error()})
		return
	}
	if !tpl.Enabled {
		response.FailWithCode(c, 400, "模版已禁用", nil)
		return
	}
	params := body.Params
	if params == nil {
		params = map[string]any{}
	}
	htmlOut, err := utils.RenderMailHTML(tpl.HTMLBody, params)
	if err != nil {
		response.FailWithCode(c, 400, "正文模版渲染失败", gin.H{"error": err.Error()})
		return
	}
	subject := strings.TrimSpace(body.Subject)
	if subject == "" {
		subject = strings.TrimSpace(tpl.Name)
		if subject == "" {
			subject = tpl.Code
		}
	} else if strings.Contains(subject, "{{") {
		subject, err = utils.RenderMailText(subject, params)
		if err != nil {
			response.FailWithCode(c, 400, "主题模版渲染失败", gin.H{"error": err.Error()})
			return
		}
		subject = strings.TrimSpace(subject)
	}
	if !cred.UnlimitedQuota && cred.RemainQuota <= 0 {
		response.FailWithCode(c, 403, "额度不足", nil)
		return
	}

	cfgs, err := listeners.EnabledMailConfigs(h.db)
	if err != nil {
		response.FailWithCode(c, 503, "未配置可用发信渠道", gin.H{"error": err.Error()})
		return
	}
	var logUID uint
	if cred.UserId > 0 {
		logUID = uint(cred.UserId)
	}
	mailer, err := notification.NewMailerMultiWithIP(cfgs, h.db, c.ClientIP(), notification.WithMailLogUserID(logUID))
	if err != nil {
		response.FailWithCode(c, 503, "发信服务不可用", gin.H{"error": err.Error()})
		return
	}
	ctx := context.Background()
	to := strings.TrimSpace(body.To)
	if err := mailer.SendHTML(ctx, to, subject, htmlOut); err != nil {
		response.Fail(c, "发送失败", gin.H{"error": err.Error()})
		return
	}

	if !cred.UnlimitedQuota {
		if err := h.db.Model(&models.Credential{}).Where("id = ? AND remain_quota > ?", cred.Id, 0).
			Update("remain_quota", gorm.Expr("remain_quota - ?", 1)).Error; err != nil {
			_ = err
		}
		if err := h.db.Model(&models.Credential{}).Where("id = ?", cred.Id).
			Update("used_quota", gorm.Expr("used_quota + ?", 1)).Error; err != nil {
			_ = err
		}
	} else {
		_ = h.db.Model(&models.Credential{}).Where("id = ?", cred.Id).
			Update("used_quota", gorm.Expr("used_quota + ?", 1)).Error
	}

	response.Success(c, "已发送", gin.H{
		"to":           to,
		"template_id": tpl.ID,
		"code":        tpl.Code,
	})
}
