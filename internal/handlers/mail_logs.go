// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"io"
	"strings"

	"github.com/LingByte/LingVoice/pkg/notification/mail"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Handlers) registerMailLogRoutes(api *gin.RouterGroup) {
	ml := api.Group("/mail-logs")
	{
		ml.GET("", h.mailLogsListHandler)
		ml.GET("/:id", h.mailLogDetailHandler)
	}
}

func (h *Handlers) mailLogsListHandler(c *gin.Context) {
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	orgID := currentOrgID(c)
	q := h.db.Model(&mail.MailLog{}).Where("org_id = ?", orgID)
	if uid, ok := parseQueryUint(c, "user_id"); ok {
		q = q.Where("user_id = ?", uid)
	}
	if s := strings.TrimSpace(c.Query("status")); s != "" {
		q = q.Where("status = ?", s)
	}
	if s := strings.TrimSpace(c.Query("provider")); s != "" {
		q = q.Where("provider = ?", s)
	}
	if s := strings.TrimSpace(c.Query("channel_name")); s != "" {
		q = q.Where("channel_name = ?", s)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	var list []mail.MailLog
	listQ := h.db.Model(&mail.MailLog{}).Where("org_id = ?", orgID)
	if uid, ok := parseQueryUint(c, "user_id"); ok {
		listQ = listQ.Where("user_id = ?", uid)
	}
	if s := strings.TrimSpace(c.Query("status")); s != "" {
		listQ = listQ.Where("status = ?", s)
	}
	if s := strings.TrimSpace(c.Query("provider")); s != "" {
		listQ = listQ.Where("provider = ?", s)
	}
	if s := strings.TrimSpace(c.Query("channel_name")); s != "" {
		listQ = listQ.Where("channel_name = ?", s)
	}
	if err := listQ.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
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

func (h *Handlers) mailLogDetailHandler(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row mail.MailLog
	orgID := currentOrgID(c)
	if err := h.db.Where("org_id = ? AND id = ?", orgID, id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "记录不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", row)
}

// sendCloudWebhookHandler receives SendCloud webhook events and applies them to mail_logs.
// Body may be JSON or x-www-form-urlencoded; we forward the raw payload to the parser.
func (h *Handlers) sendCloudWebhookHandler(c *gin.Context) {
	raw, _ := io.ReadAll(c.Request.Body)
	if len(raw) == 0 {
		c.JSON(200, gin.H{"ok": true})
		return
	}
	// SendCloud sometimes sends "messageId=xxx&..." and body may contain leading/trailing spaces/newlines.
	raw = []byte(strings.TrimSpace(string(raw)))
	if err := mail.ApplySendCloudWebhookToMailLog(h.db, raw); err != nil {
		// Webhook callers generally do not retry based on body; keep response simple.
		c.JSON(400, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
