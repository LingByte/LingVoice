// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strconv"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type chatSessionCreateBody struct {
	Title        string `json:"title"`
	Model        string `json:"model" binding:"required"`
	Provider     string `json:"provider"`
	SystemPrompt string `json:"system_prompt"`
}

type chatSessionPatchBody struct {
	Title string `json:"title" binding:"required,min=1,max=255"`
}

type chatMessageCreateBody struct {
	Role       string `json:"role" binding:"required"`
	Content    string `json:"content" binding:"required"`
	TokenCount int    `json:"token_count"`
	Model      string `json:"model"`
	Provider   string `json:"provider"`
	RequestID  string `json:"request_id"`
}

// chatSessionsListHandler GET /api/chat/sessions
func (h *Handlers) chatSessionsListHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	rows, err := models.ListChatSessionsForUser(h.db, strconv.FormatUint(uint64(u.ID), 10), 200)
	if err != nil {
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	out := make([]models.ChatSessionAPIRow, 0, len(rows))
	for i := range rows {
		out = append(out, models.ChatSessionToAPIRow(&rows[i]))
	}
	response.SuccessOK(c, gin.H{"list": out})
}

// chatSessionCreateHandler POST /api/chat/sessions
func (h *Handlers) chatSessionCreateHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	var body chatSessionCreateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "参数错误"), gin.H{"error": err.Error()})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		response.FailWithCode(c, 400, response.Msg(c, "缺少 model"), nil)
		return
	}
	prov := strings.TrimSpace(body.Provider)
	if prov == "" {
		prov = "openai"
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = "新对话"
	}
	row := models.ChatSession{
		ID:           base.SnowflakeUtil.GenID(),
		UserID:       strconv.FormatUint(uint64(u.ID), 10),
		Title:        title,
		Provider:     prov,
		Model:        model,
		SystemPrompt: strings.TrimSpace(body.SystemPrompt),
		Status:       "active",
	}
	if err := models.CreateChatSession(h.db, &row); err != nil {
		response.Fail(c, response.Msg(c, "创建失败"), gin.H{"error": err.Error()})
		return
	}
	response.Success(c, response.Msg(c, "创建成功"), gin.H{
		"session": models.ChatSessionToAPIRow(&row),
	})
}

// chatSessionDetailHandler GET /api/chat/sessions/:id
func (h *Handlers) chatSessionDetailHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的会话 id"), nil)
		return
	}
	row, err := models.GetChatSessionOwned(h.db, strconv.FormatUint(uint64(u.ID), 10), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "会话不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	response.SuccessOK(c, gin.H{
		"session": models.ChatSessionToAPIRow(row),
	})
}

// chatSessionPatchHandler PATCH /api/chat/sessions/:id
func (h *Handlers) chatSessionPatchHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的会话 id"), nil)
		return
	}
	var body chatSessionPatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "参数错误"), gin.H{"error": err.Error()})
		return
	}
	_, err := models.GetChatSessionOwned(h.db, strconv.FormatUint(uint64(u.ID), 10), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "会话不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	title := strings.TrimSpace(body.Title)
	if err := models.UpdateChatSessionTitle(h.db, strconv.FormatUint(uint64(u.ID), 10), sid, title); err != nil {
		response.Fail(c, response.Msg(c, "更新失败"), gin.H{"error": err.Error()})
		return
	}
	response.Success(c, response.Msg(c, "已更新"), gin.H{"id": sid, "title": title})
}

// chatSessionDeleteHandler DELETE /api/chat/sessions/:id
func (h *Handlers) chatSessionDeleteHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的会话 id"), nil)
		return
	}
	_, err := models.GetChatSessionOwned(h.db, strconv.FormatUint(uint64(u.ID), 10), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "会话不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	if err := models.SoftDeleteChatSession(h.db, strconv.FormatUint(uint64(u.ID), 10), sid); err != nil {
		response.Fail(c, response.Msg(c, "删除失败"), gin.H{"error": err.Error()})
		return
	}
	response.Success(c, response.Msg(c, "已删除"), gin.H{"id": sid})
}

// chatSessionMessagesListHandler GET /api/chat/sessions/:id/messages
func (h *Handlers) chatSessionMessagesListHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的会话 id"), nil)
		return
	}
	sess, err := models.GetChatSessionOwned(h.db, strconv.FormatUint(uint64(u.ID), 10), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "会话不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	rows, err := models.ListChatMessagesForSession(h.db, sid, 500)
	if err != nil {
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	out := make([]models.ChatMessageAPIRow, 0, len(rows))
	for i := range rows {
		out = append(out, models.ChatMessageToAPIRow(&rows[i]))
	}
	response.SuccessOK(c, gin.H{
		"list": out,
		"session": gin.H{
			"id":       sess.ID,
			"title":    sess.Title,
			"model":    sess.Model,
			"provider": sess.Provider,
		},
	})
}

// chatSessionMessageCreateHandler POST /api/chat/sessions/:id/messages
func (h *Handlers) chatSessionMessageCreateHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的会话 id"), nil)
		return
	}
	sess, err := models.GetChatSessionOwned(h.db, strconv.FormatUint(uint64(u.ID), 10), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "会话不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	var body chatMessageCreateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "参数错误"), gin.H{"error": err.Error()})
		return
	}
	role := strings.ToLower(strings.TrimSpace(body.Role))
	if role != "user" && role != "assistant" && role != "system" {
		response.FailWithCode(c, 400, response.Msg(c, "role 须为 user、assistant 或 system"), nil)
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = sess.Model
	}
	prov := strings.TrimSpace(body.Provider)
	if prov == "" {
		prov = sess.Provider
	}
	msg := models.ChatMessage{
		ID:         base.SnowflakeUtil.GenID(),
		SessionID:  sid,
		Role:       role,
		Content:    body.Content,
		TokenCount: body.TokenCount,
		Model:      model,
		Provider:   prov,
		RequestID:  strings.TrimSpace(body.RequestID),
	}
	if err := models.CreateChatMessage(h.db, &msg); err != nil {
		response.Fail(c, response.Msg(c, "保存失败"), gin.H{"error": err.Error()})
		return
	}
	_ = models.TouchChatSessionUpdatedAt(h.db, sid)
	response.SuccessOK(c, gin.H{
		"message": models.ChatMessageToAPIRow(&msg),
	})
}
