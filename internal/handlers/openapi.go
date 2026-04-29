// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice"
	"github.com/LingByte/LingVoice/internal/listeners"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/LingByte/LingVoice/pkg/notification/mail"
	"github.com/LingByte/LingVoice/pkg/utils/response"
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

func (h *Handlers) registerV1RelayRoutes(engine *gin.Engine) {
	v1mail := engine.Group("/v1")
	v1mail.Use(middleware.OpenAPIEmailCredential(h.db))
	{
		v1mail.GET("/mail-templates", h.mailTemplatesListHandler)
		v1mail.POST("/mail-templates", h.openAPIMailTemplateCreateHandler)
		v1mail.GET("/mail-logs", h.mailLogsListHandler)
		v1mail.GET("/mail-logs/:id", h.mailLogDetailHandler)
		v1mail.POST("/mail/send", h.openAPIMailSendHandler)
	}

	// OpenAI 协议：Authorization: Bearer（凭证 kind=llm）
	v1llm := engine.Group("/v1")
	v1llm.Use(middleware.OpenAPILLMProxyAuth(h.db, middleware.OpenAPILLMStyleOpenAI))
	{
		v1llm.GET("/models", h.openAPIModelsListHandler)
		v1llm.POST("/chat/completions", h.openAPIOpenAIChatCompletionsHandler)
		v1llm.POST("/agent/chat/stream", h.openAPIAgentChatStreamHandler)
	}

	v1asr := engine.Group("/v1/speech/asr")
	v1asr.Use(middleware.OpenAPISpeechProxyAuth(h.db, models.CredentialKindASR))
	{
		v1asr.POST("/transcribe", h.openAPIASRTranscribeHandler)
	}
	v1tts := engine.Group("/v1/speech/tts")
	v1tts.Use(middleware.OpenAPISpeechProxyAuth(h.db, models.CredentialKindTTS))
	{
		v1tts.POST("/synthesize", h.openAPITTSSynthesizeHandler)
	}

	// Anthropic Messages：与官方一致 POST /v1/messages；x-api-key 或 Bearer（凭证 kind=llm）
	v1anthropic := engine.Group("/v1")
	v1anthropic.Use(middleware.OpenAPILLMProxyAuth(h.db, middleware.OpenAPILLMStyleAnthropic))
	{
		v1anthropic.POST("/messages", h.openAPIAnthropicMessagesHandler)
	}
}

// openAPIMailTemplateCreateHandler 与控制台创建模版逻辑一致，创建人记为 openapi 凭证。
func (h *Handlers) openAPIMailTemplateCreateHandler(c *gin.Context) {
	cred, ok := middleware.OpenAPICredentialFromContext(c)
	if !ok || cred == nil {
		response.FailWithCode(c, 401, "未授权", nil)
		return
	}
	var req MailTemplateCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	orgID := uint(0)
	if cred.UserId > 0 {
		var u models.User
		if err := h.db.Where("id = ?", uint(cred.UserId)).First(&u).Error; err == nil {
			_ = models.EnsurePersonalOrg(h.db, &u)
			orgID = u.DefaultOrgID
		}
	}
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
	tpl.SetCreateInfo(fmt.Sprintf("openapi-credential:%d", cred.Id))
	if err := models.CreateMailTemplate(h.db, &tpl); err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", tpl)
}

func (h *Handlers) openAPIMailSendHandler(c *gin.Context) {
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
	orgID := uint(0)
	var logUID uint
	if cred.UserId > 0 {
		logUID = uint(cred.UserId)
		var u models.User
		if err := h.db.Where("id = ?", logUID).First(&u).Error; err == nil {
			_ = models.EnsurePersonalOrg(h.db, &u)
			orgID = u.DefaultOrgID
		}
	}
	tpl, err := models.GetMailTemplateByID(h.db, body.TemplateID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "模版不存在", nil)
			return
		}
		response.Fail(c, "查询模版失败", gin.H{"error": err.Error()})
		return
	}
	if orgID != 0 && tpl.OrgID != 0 && tpl.OrgID != orgID {
		response.FailWithCode(c, 403, "无权访问该模版", nil)
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
	htmlOut, err := LingVoice.RenderMailHTML(tpl.HTMLBody, params)
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
		subject, err = LingVoice.RenderMailText(subject, params)
		if err != nil {
			response.FailWithCode(c, 400, "主题模版渲染失败", gin.H{"error": err.Error()})
			return
		}
		subject = strings.TrimSpace(subject)
	}
	if !models.CredentialHasRemainingQuota(cred) {
		response.FailWithCode(c, 403, "该邮件 API 凭证剩余额度已用尽", gin.H{"reason": models.OpenAPIQuotaReasonCredentialExhausted})
		return
	}
	if cred.UserId > 0 {
		ok, uerr := models.UserHasSpendableQuota(h.db, uint(cred.UserId))
		if uerr != nil {
			if errors.Is(uerr, gorm.ErrRecordNotFound) {
				response.FailWithCode(c, 403, "API Key 所属用户不存在", gin.H{"reason": models.OpenAPIQuotaReasonUserNotFound})
				return
			}
			response.FailWithCode(c, 500, "校验用户额度失败", gin.H{"error": uerr.Error()})
			return
		}
		if !ok {
			response.FailWithCode(c, 403, "所属用户账户剩余额度已用尽", gin.H{"reason": models.OpenAPIQuotaReasonUserExhausted})
			return
		}
	}

	cfgs, err := listeners.EnabledMailConfigs(h.db)
	if err != nil {
		response.FailWithCode(c, 503, "未配置可用发信渠道", gin.H{"error": err.Error()})
		return
	}
	mailer, err := mail.NewMailer(cfgs, h.db, c.ClientIP(), mail.WithMailLogUserID(logUID), mail.WithMailLogOrgID(orgID))
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

	_ = models.BumpCredentialUsedAndDecrementRemain(h.db, cred.Id, cred.UnlimitedQuota, 1)

	response.Success(c, "已发送", gin.H{
		"to":          to,
		"template_id": tpl.ID,
		"code":        tpl.Code,
	})
}
