// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice"
	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/internal/listeners"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/agent/exec"
	"github.com/LingByte/LingVoice/pkg/agent/plan"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/LingByte/LingVoice/pkg/notification/mail"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// OpenapiSendMailBody 按邮件模版 ID 发送：正文来自模版 HTML，params 的键与模版中 {{.Key}} 对应。
type OpenapiSendMailBody struct {
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
		response.FailWithCode(c, 401, response.Msg(c, "未授权"), nil)
		return
	}
	var req MailTemplateCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "参数错误"), gin.H{"error": err.Error()})
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
		response.Fail(c, response.Msg(c, "创建失败"), gin.H{"error": err.Error()})
		return
	}
	response.Success(c, response.Msg(c, "创建成功"), tpl)
}

func (h *Handlers) openAPIMailSendHandler(c *gin.Context) {
	cred, ok := middleware.OpenAPICredentialFromContext(c)
	if !ok || cred == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未授权"), nil)
		return
	}
	var body OpenapiSendMailBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "参数错误"), gin.H{"error": err.Error()})
		return
	}
	if body.TemplateID == 0 {
		response.FailWithCode(c, 400, response.Msg(c, "缺少有效的 template_id"), nil)
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
			response.FailWithCode(c, 404, response.Msg(c, "模版不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询模版失败"), gin.H{"error": err.Error()})
		return
	}
	if orgID != 0 && tpl.OrgID != 0 && tpl.OrgID != orgID {
		response.FailWithCode(c, 403, response.Msg(c, "无权访问该模版"), nil)
		return
	}
	if !tpl.Enabled {
		response.FailWithCode(c, 400, response.Msg(c, "模版已禁用"), nil)
		return
	}
	params := body.Params
	if params == nil {
		params = map[string]any{}
	}
	htmlOut, err := LingVoice.RenderMailHTML(tpl.HTMLBody, params)
	if err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "正文模版渲染失败"), gin.H{"error": err.Error()})
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
			response.FailWithCode(c, 400, response.Msg(c, "主题模版渲染失败"), gin.H{"error": err.Error()})
			return
		}
		subject = strings.TrimSpace(subject)
	}
	if !models.CredentialHasRemainingQuota(cred) {
		response.FailWithCode(c, 403, response.Msg(c, "该邮件 API 凭证剩余额度已用尽"), gin.H{"reason": models.OpenAPIQuotaReasonCredentialExhausted})
		return
	}
	if cred.UserId > 0 {
		ok, uerr := models.UserHasSpendableQuota(h.db, uint(cred.UserId))
		if uerr != nil {
			if errors.Is(uerr, gorm.ErrRecordNotFound) {
				response.FailWithCode(c, 403, response.Msg(c, "API Key 所属用户不存在"), gin.H{"reason": models.OpenAPIQuotaReasonUserNotFound})
				return
			}
			response.FailWithCode(c, 500, response.Msg(c, "校验用户额度失败"), gin.H{"error": uerr.Error()})
			return
		}
		if !ok {
			response.FailWithCode(c, 403, response.Msg(c, "所属用户账户剩余额度已用尽"), gin.H{"reason": models.OpenAPIQuotaReasonUserExhausted})
			return
		}
	}

	cfgs, err := listeners.EnabledMailConfigs(h.db)
	if err != nil {
		response.FailWithCode(c, 503, response.Msg(c, "未配置可用发信渠道"), gin.H{"error": err.Error()})
		return
	}
	mailer, err := mail.NewMailer(cfgs, h.db, c.ClientIP(), mail.WithMailLogUserID(logUID), mail.WithMailLogOrgID(orgID))
	if err != nil {
		response.FailWithCode(c, 503, response.Msg(c, "发信服务不可用"), gin.H{"error": err.Error()})
		return
	}
	ctx := context.Background()
	to := strings.TrimSpace(body.To)
	if err := mailer.SendHTML(ctx, to, subject, htmlOut); err != nil {
		response.Fail(c, response.Msg(c, "发送失败"), gin.H{"error": err.Error()})
		return
	}

	_ = models.BumpCredentialUsedAndDecrementRemain(h.db, cred.Id, cred.UnlimitedQuota, 1)

	response.Success(c, response.Msg(c, "已发送"), gin.H{
		"to":          to,
		"template_id": tpl.ID,
		"code":        tpl.Code,
	})
}

type openAPIAgentChatBody struct {
	Input     string `json:"input" binding:"required"`
	Model     string `json:"model"`
	MaxTasks  int    `json:"max_tasks"`
	SessionID string `json:"session_id"`
}

func agentOpenAPIQueryOpts(c *gin.Context, cred *models.Credential, sessionID string, ch *models.LLMChannel) *llm.QueryOptions {
	if ch == nil {
		return &llm.QueryOptions{
			SessionID:     strings.TrimSpace(sessionID),
			UserID:        models.CredentialUserIDString(cred.UserId),
			HTTPUserAgent: c.Request.UserAgent(),
			ClientIP:      c.ClientIP(),
		}
	}
	return &llm.QueryOptions{
		SessionID:      strings.TrimSpace(sessionID),
		UserID:         models.CredentialUserIDString(cred.UserId),
		HTTPUserAgent:  c.Request.UserAgent(),
		ClientIP:       c.ClientIP(),
		UsageChannelID: ch.Id,
	}
}

func newLLMHandlerFromLLMChannel(ctx context.Context, ch *models.LLMChannel) (llm.LLMHandler, error) {
	if ch == nil {
		return nil, fmt.Errorf("nil channel")
	}
	bu := ""
	if ch.BaseURL != nil {
		bu = strings.TrimSpace(*ch.BaseURL)
	}
	prov := strings.ToLower(strings.TrimSpace(ch.Protocol))
	if prov == "" {
		prov = models.LLMChannelProtocolOpenAI
	}
	return llm.NewProviderHandler(ctx, prov, &llm.LLMOptions{
		ApiKey:  strings.TrimSpace(ch.Key),
		BaseURL: bu,
	})
}

func writeNDJSONLine(c *gin.Context, fl http.Flusher, v any) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	if _, err := c.Writer.Write(append(b, '\n')); err != nil {
		return false
	}
	if fl != nil {
		fl.Flush()
	}
	return true
}

// openAPIAgentChatStreamHandler POST /v1/agent/chat/stream
// 按行 NDJSON：{"event":"...","data":{...}}；使用凭证分组下 OpenAI 协议 LLM 渠道执行 pkg/agent 规划与执行。
func (h *Handlers) openAPIAgentChatStreamHandler(c *gin.Context) {
	cred, ok := middleware.OpenAPILLMCredentialFromContext(c)
	if !ok || cred == nil {
		return
	}
	var body openAPIAgentChatBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error(), "type": "invalid_request_error"}})
		return
	}
	input := strings.TrimSpace(body.Input)
	if input == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "input is required", "type": "invalid_request_error"}})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = "gpt-4o-mini"
	}
	maxTasks := body.MaxTasks
	if maxTasks <= 0 {
		maxTasks = 6
	}
	if maxTasks > 32 {
		maxTasks = 32
	}

	channels, err := models.ListLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolOpenAI, model)
	if err != nil || len(channels) == 0 {
		c.JSON(http.StatusServiceUnavailable, models.OpenAINoLLMChannelPayload(cred))
		return
	}
	ch := channels[0]

	ctx, cancel := context.WithTimeout(c.Request.Context(), 6*time.Minute)
	defer cancel()

	handler, err := newLLMHandlerFromLLMChannel(ctx, &ch)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": err.Error(), "type": "api_error"}})
		return
	}
	defer handler.Interrupt()

	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	fl, _ := c.Writer.(http.Flusher)

	writeEvt := func(event string, data any) bool {
		return writeNDJSONLine(c, fl, gin.H{"event": event, "data": data})
	}

	if !writeEvt("start", gin.H{"model": model, "max_tasks": maxTasks}) {
		return
	}

	baseOpts := agentOpenAPIQueryOpts(c, cred, body.SessionID, &ch)
	decomposer := &plan.LLMDecomposer{
		LLM:      exec.NewPlanLLMBridge(handler, baseOpts, "openapi_agent_decompose"),
		Model:    model,
		MaxTasks: maxTasks,
	}
	pl, err := decomposer.Decompose(ctx, plan.Request{
		Goal:     input,
		MaxTasks: maxTasks,
		LLMModel: model,
	})
	if err != nil {
		_ = writeEvt("error", gin.H{"message": err.Error()})
		_ = writeEvt("done", gin.H{"ok": false})
		return
	}
	tasks := make([]gin.H, 0, len(pl.Tasks))
	for _, t := range pl.Tasks {
		tasks = append(tasks, gin.H{
			"id": t.ID, "title": t.Title, "instruction": t.Instruction, "expected": t.Expected,
		})
	}
	if !writeEvt("plan", gin.H{"goal": pl.Goal, "tasks": tasks, "task_count": len(pl.Tasks)}) {
		return
	}

	ex := &exec.Executor{
		Runner:    &exec.LLMTaskRunner{LLM: handler, Model: model, ExtraQuery: baseOpts},
		Evaluator: &exec.LLMTaskEvaluator{LLM: handler, Model: model, ExtraQuery: baseOpts},
		Opts: exec.Options{
			StopOnError: true,
			MaxTasks:    maxTasks,
			MaxAttempts: 2,
			BeforeTask: func(t plan.Task, index, total int) {
				_ = writeEvt("task", gin.H{
					"phase":   "running",
					"index":   index,
					"total":   total,
					"task_id": t.ID,
					"title":   t.Title,
					"status":  "running",
				})
			},
			AfterTask: func(t plan.Task, tr exec.TaskResult) {
				st := string(tr.Status)
				payload := gin.H{
					"phase":      "finished",
					"task_id":    t.ID,
					"title":      t.Title,
					"status":     st,
					"attempts":   tr.Attempts,
					"latency_ms": tr.Latency.Milliseconds(),
				}
				if tr.Status == exec.TaskSucceeded {
					payload["output"] = tr.Output
				}
				if tr.Error != "" {
					payload["error"] = tr.Error
				}
				_ = writeEvt("task", payload)
			},
		},
	}

	res, runErr := ex.Run(ctx, pl)
	finalOut := ""
	if res != nil {
		for i := len(res.TaskResults) - 1; i >= 0; i-- {
			if res.TaskResults[i].Status == exec.TaskSucceeded && strings.TrimSpace(res.TaskResults[i].Output) != "" {
				finalOut = strings.TrimSpace(res.TaskResults[i].Output)
				break
			}
		}
	}
	if finalOut == "" && runErr != nil {
		finalOut = runErr.Error()
	}
	if finalOut == "" {
		finalOut = "Agent 执行结束，但没有可用的文本输出。"
	}

	runOK := runErr == nil
	if runOK {
		gr := float64(1)
		if config.GlobalConfig != nil {
			gr = config.GlobalConfig.OpenAPIQuotaGroupRatio(cred.Group)
		}
		d := QuotaDeltaForAgentRun(h.db, model, gr)
		models.DecrementCredentialAndUserQuota(h.db, cred, d)
	}
	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}
	_ = writeEvt("final", gin.H{"output": finalOut, "ok": runOK, "error": errMsg})
	_ = writeEvt("done", gin.H{"ok": runOK})
}
