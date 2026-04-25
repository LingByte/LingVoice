// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func chatUserIDStr(u *models.User) string {
	if u == nil {
		return ""
	}
	return strconv.FormatUint(uint64(u.ID), 10)
}

func (h *Handlers) chatSessionOwned(db *gorm.DB, userID, sessionID string) (*models.ChatSession, error) {
	var row models.ChatSession
	err := db.Where("id = ? AND user_id = ? AND (deleted_at IS NULL)", sessionID, userID).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

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

func newChatSnowflakeID() string {
	if utils.SnowflakeUtil != nil {
		return utils.SnowflakeUtil.GenID()
	}
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

// listChatSessions GET /api/chat/sessions
func (h *Handlers) listChatSessions(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	uid := chatUserIDStr(u)
	var rows []models.ChatSession
	if err := h.db.Where("user_id = ? AND (deleted_at IS NULL)", uid).
		Order("updated_at DESC").Limit(200).Find(&rows).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		r := rows[i]
		out = append(out, gin.H{
			"id":            r.ID,
			"title":         r.Title,
			"model":         r.Model,
			"provider":      r.Provider,
			"system_prompt": r.SystemPrompt,
			"status":        r.Status,
			"created_at":    r.CreatedAt.UnixMilli(),
			"updated_at":    r.UpdatedAt.UnixMilli(),
		})
	}
	response.Success(c, "ok", gin.H{"list": out})
}

// createChatSession POST /api/chat/sessions
func (h *Handlers) createChatSession(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	var body chatSessionCreateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		response.FailWithCode(c, 400, "缺少 model", nil)
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
		ID:           newChatSnowflakeID(),
		UserID:       chatUserIDStr(u),
		Title:        title,
		Provider:     prov,
		Model:        model,
		SystemPrompt: strings.TrimSpace(body.SystemPrompt),
		Status:       "active",
	}
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", gin.H{
		"session": gin.H{
			"id":            row.ID,
			"title":         row.Title,
			"model":         row.Model,
			"provider":      row.Provider,
			"system_prompt": row.SystemPrompt,
			"status":        row.Status,
			"created_at":    row.CreatedAt.UnixMilli(),
			"updated_at":    row.UpdatedAt.UnixMilli(),
		},
	})
}

// getChatSession GET /api/chat/sessions/:id
func (h *Handlers) getChatSession(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, "无效的会话 id", nil)
		return
	}
	row, err := h.chatSessionOwned(h.db, chatUserIDStr(u), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "会话不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{
		"session": gin.H{
			"id":            row.ID,
			"title":         row.Title,
			"model":         row.Model,
			"provider":      row.Provider,
			"system_prompt": row.SystemPrompt,
			"status":        row.Status,
			"created_at":    row.CreatedAt.UnixMilli(),
			"updated_at":    row.UpdatedAt.UnixMilli(),
		},
	})
}

// patchChatSession PATCH /api/chat/sessions/:id
func (h *Handlers) patchChatSession(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, "无效的会话 id", nil)
		return
	}
	var body chatSessionPatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	_, err := h.chatSessionOwned(h.db, chatUserIDStr(u), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "会话不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if err := h.db.Model(&models.ChatSession{}).Where("id = ? AND user_id = ?", sid, chatUserIDStr(u)).
		Updates(map[string]interface{}{
			"title":      strings.TrimSpace(body.Title),
			"updated_at": time.Now(),
		}).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "已更新", gin.H{"id": sid, "title": strings.TrimSpace(body.Title)})
}

// deleteChatSession DELETE /api/chat/sessions/:id
func (h *Handlers) deleteChatSession(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, "无效的会话 id", nil)
		return
	}
	_, err := h.chatSessionOwned(h.db, chatUserIDStr(u), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "会话不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	now := time.Now()
	if err := h.db.Model(&models.ChatSession{}).Where("id = ? AND user_id = ?", sid, chatUserIDStr(u)).
		Updates(map[string]interface{}{
			"deleted_at": now,
			"status":     "deleted",
			"updated_at": now,
		}).Error; err != nil {
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "已删除", gin.H{"id": sid})
}

// listChatMessages GET /api/chat/sessions/:id/messages
func (h *Handlers) listChatMessages(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, "无效的会话 id", nil)
		return
	}
	sess, err := h.chatSessionOwned(h.db, chatUserIDStr(u), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "会话不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	var rows []models.ChatMessage
	if err := h.db.Where("session_id = ? AND (deleted_at IS NULL)", sid).
		Order("created_at ASC").Limit(500).Find(&rows).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		m := rows[i]
		out = append(out, gin.H{
			"id":         m.ID,
			"session_id": m.SessionID,
			"role":        m.Role,
			"content":     m.Content,
			"token_count": m.TokenCount,
			"model":       m.Model,
			"provider":    m.Provider,
			"request_id":  m.RequestID,
			"created_at":  m.CreatedAt.UnixMilli(),
		})
	}
	response.Success(c, "ok", gin.H{
		"list":    out,
		"session": gin.H{"id": sess.ID, "title": sess.Title, "model": sess.Model, "provider": sess.Provider},
	})
}

// appendChatMessage POST /api/chat/sessions/:id/messages
func (h *Handlers) appendChatMessage(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	sid := strings.TrimSpace(c.Param("id"))
	if sid == "" {
		response.FailWithCode(c, 400, "无效的会话 id", nil)
		return
	}
	sess, err := h.chatSessionOwned(h.db, chatUserIDStr(u), sid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "会话不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	var body chatMessageCreateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	role := strings.ToLower(strings.TrimSpace(body.Role))
	if role != "user" && role != "assistant" && role != "system" {
		response.FailWithCode(c, 400, "role 须为 user、assistant 或 system", nil)
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
		ID:         newChatSnowflakeID(),
		SessionID:  sid,
		Role:       role,
		Content:    body.Content,
		TokenCount: body.TokenCount,
		Model:      model,
		Provider:   prov,
		RequestID:  strings.TrimSpace(body.RequestID),
	}
	if err := h.db.Create(&msg).Error; err != nil {
		response.Fail(c, "保存失败", gin.H{"error": err.Error()})
		return
	}
	_ = h.db.Model(&models.ChatSession{}).Where("id = ?", sid).Update("updated_at", time.Now()).Error
	response.Success(c, "ok", gin.H{
		"message": gin.H{
			"id":          msg.ID,
			"session_id":  msg.SessionID,
			"role":        msg.Role,
			"content":     msg.Content,
			"token_count": msg.TokenCount,
			"model":       msg.Model,
			"provider":    msg.Provider,
			"request_id":  msg.RequestID,
			"created_at":  msg.CreatedAt.UnixMilli(),
		},
	})
}
