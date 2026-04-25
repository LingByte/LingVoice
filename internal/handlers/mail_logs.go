// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/notification"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type mailLogCreateReq struct {
	UserID      uint   `json:"user_id"`
	Provider    string `json:"provider" binding:"required,max=32"`
	ChannelName string `json:"channel_name" binding:"max=128"`
	ToEmail     string `json:"to_email" binding:"required,max=512"`
	Subject     string `json:"subject" binding:"required,max=512"`
	Status      string `json:"status" binding:"required,max=64"`
	HtmlBody    string `json:"html_body"`
	ErrorMsg    string `json:"error_msg"`
	MessageID   string `json:"message_id" binding:"max=255"`
	IPAddress   string `json:"ip_address" binding:"max=64"`
	RetryCount  *int   `json:"retry_count"`
}

type mailLogUpdateReq struct {
	Subject     string `json:"subject"`
	Status      string `json:"status" binding:"required,max=64"`
	ErrorMsg    string `json:"error_msg"`
	MessageID   string `json:"message_id" binding:"max=255"`
	ChannelName string `json:"channel_name" binding:"max=128"`
	HtmlBody    string `json:"html_body"`
	ToEmail     string `json:"to_email" binding:"max=512"`
	Provider    string `json:"provider" binding:"max=32"`
}

func (h *Handlers) listMailLogs(c *gin.Context) {
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	q := h.db.Model(&notification.MailLog{})
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
	var list []notification.MailLog
	listQ := h.db.Model(&notification.MailLog{})
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

func mailLogSentAtForStatus(status string) time.Time {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case notification.StatusSent, notification.StatusDelivered, notification.StatusQueued:
		return time.Now()
	default:
		return time.Time{}
	}
}

func (h *Handlers) createMailLog(c *gin.Context) {
	var req mailLogCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	row := notification.MailLog{
		ID:          notification.NextMailLogSnowflakeID(),
		UserID:      req.UserID,
		Provider:    strings.TrimSpace(req.Provider),
		ChannelName: strings.TrimSpace(req.ChannelName),
		ToEmail:     strings.TrimSpace(req.ToEmail),
		Subject:     strings.TrimSpace(req.Subject),
		Status:      strings.TrimSpace(req.Status),
		HtmlBody:    req.HtmlBody,
		ErrorMsg:    req.ErrorMsg,
		MessageID:   strings.TrimSpace(req.MessageID),
		IPAddress:   strings.TrimSpace(req.IPAddress),
		RetryCount:  0,
		SentAt:      mailLogSentAtForStatus(req.Status),
	}
	if req.RetryCount != nil {
		row.RetryCount = *req.RetryCount
	}
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", row)
}

func (h *Handlers) getMailLog(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row notification.MailLog
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "记录不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", row)
}

func (h *Handlers) updateMailLog(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req mailLogUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var row notification.MailLog
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "记录不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	row.Subject = req.Subject
	row.Status = req.Status
	row.ErrorMsg = req.ErrorMsg
	row.MessageID = strings.TrimSpace(req.MessageID)
	row.ChannelName = strings.TrimSpace(req.ChannelName)
	row.HtmlBody = req.HtmlBody
	if te := strings.TrimSpace(req.ToEmail); te != "" {
		row.ToEmail = te
	}
	if pv := strings.TrimSpace(req.Provider); pv != "" {
		row.Provider = pv
	}
	if row.SentAt.IsZero() {
		row.SentAt = mailLogSentAtForStatus(req.Status)
	}
	if err := h.db.Save(&row).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

func (h *Handlers) deleteMailLog(c *gin.Context) {
	id, ok := parseInt64Param(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	res := h.db.Delete(&notification.MailLog{}, id)
	if res.Error != nil {
		response.Fail(c, "删除失败", gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		response.FailWithCode(c, 404, "记录不存在", nil)
		return
	}
	response.Success(c, "删除成功", gin.H{"id": id})
}
