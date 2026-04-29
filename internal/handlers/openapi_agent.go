// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/agent/exec"
	"github.com/LingByte/LingVoice/pkg/agent/plan"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/gin-gonic/gin"
)

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
			UserID:        credentialUserIDString(cred.UserId),
			HTTPUserAgent: c.Request.UserAgent(),
			ClientIP:      c.ClientIP(),
		}
	}
	return &llm.QueryOptions{
		SessionID:      strings.TrimSpace(sessionID),
		UserID:         credentialUserIDString(cred.UserId),
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

// openAPIAgentChatStream POST /v1/agent/chat/stream
// 按行 NDJSON：{"event":"...","data":{...}}；使用凭证分组下 OpenAI 协议 LLM 渠道执行 pkg/agent 规划与执行。
func (h *Handlers) openAPIAgentChatStream(c *gin.Context) {
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

	channels, err := listLLMChannelsForRelay(h.db, cred, models.LLMChannelProtocolOpenAI, model)
	if err != nil || len(channels) == 0 {
		c.JSON(http.StatusServiceUnavailable, openAINoLLMChannelResponse(cred))
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
		llmCredAndUserDecrementQuota(h.db, cred, d)
	}
	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}
	_ = writeEvt("final", gin.H{"output": finalOut, "ok": runOK, "error": errMsg})
	_ = writeEvt("done", gin.H{"ok": runOK})
}
