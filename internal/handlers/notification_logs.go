package handlers

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/LingByte/LingVoice/internal/listeners"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/notification/mail"
	"github.com/LingByte/LingVoice/pkg/notification/sms"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

func (h *Handlers) registerSMSLogRoutes(api *gin.RouterGroup) {
	sl := api.Group("/sms-logs")
	{
		sl.GET("", h.smsLogsListHandler)
		sl.GET("/:id", h.smsLogDetailHandler)
	}
	smsAdm := api.Group("/sms")
	smsAdm.Use(models.AuthRequired, models.AdminRequired)
	{
		smsAdm.POST("/send", h.smsSendHandler)
	}
}

func (h *Handlers) registerMailLogRoutes(api *gin.RouterGroup) {
	ml := api.Group("/mail-logs")
	{
		ml.GET("", h.mailLogsListHandler)
		ml.GET("/:id", h.mailLogDetailHandler)
	}
}

func (h *Handlers) mailLogsListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	orgID := models.CurrentOrgID(c)
	q := h.db.Model(&mail.MailLog{}).Where("org_id = ?", orgID)
	if uid, ok := models.ParseQueryUint(c, "user_id"); ok {
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
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	var list []mail.MailLog
	listQ := h.db.Model(&mail.MailLog{}).Where("org_id = ?", orgID)
	if uid, ok := models.ParseQueryUint(c, "user_id"); ok {
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

func (h *Handlers) mailLogDetailHandler(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的 id"), nil)
		return
	}
	var row mail.MailLog
	orgID := models.CurrentOrgID(c)
	if err := h.db.Where("org_id = ? AND id = ?", orgID, id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "记录不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	response.SuccessOK(c, row)
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

func (h *Handlers) smsLogsListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize
	orgID := models.CurrentOrgID(c)

	q := h.db.Model(&sms.SMSLog{}).Where("org_id = ?", orgID)
	if uid, ok := models.ParseQueryUint(c, "user_id"); ok {
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
	if s := strings.TrimSpace(c.Query("to_phone")); s != "" {
		q = q.Where("to_phone = ?", s)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	var list []sms.SMSLog
	if err := q.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
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

func (h *Handlers) smsLogDetailHandler(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的 id"), nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	var row sms.SMSLog
	if err := h.db.Where("org_id = ? AND id = ?", orgID, id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "记录不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	response.SuccessOK(c, row)
}

type smsSendBody struct {
	To       string            `json:"to" binding:"required"`
	Content  string            `json:"content"`
	Template string            `json:"template"`
	Data     map[string]string `json:"data"`
}

// smsSendHandler sends an SMS using enabled SMS channels of current org (admin debug).
func (h *Handlers) smsSendHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	var req smsSendBody
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, response.Msg(c, "参数错误"), gin.H{"error": err.Error()})
		return
	}
	orgID := models.CurrentOrgID(c)
	chans, err := listeners.EnabledSMSChannels(h.db, orgID)
	if err != nil {
		response.FailWithCode(c, 503, response.Msg(c, "未配置可用短信渠道"), gin.H{"error": err.Error()})
		return
	}
	sender, err := sms.NewMultiSender(chans, h.db, c.ClientIP(), sms.WithSMSLogOrgID(orgID), sms.WithSMSLogUserID(u.ID))
	if err != nil {
		response.FailWithCode(c, 503, response.Msg(c, "短信服务不可用"), gin.H{"error": err.Error()})
		return
	}
	msg := sms.Message{
		Content:  strings.TrimSpace(req.Content),
		Template: strings.TrimSpace(req.Template),
		Data:     req.Data,
	}
	sendReq := sms.SendRequest{
		To:      []sms.PhoneNumber{{Number: strings.TrimSpace(req.To)}},
		Message: msg,
	}
	if err := sender.Send(context.Background(), sendReq); err != nil {
		response.Fail(c, response.Msg(c, "发送失败"), gin.H{"error": err.Error()})
		return
	}
	response.Success(c, response.Msg(c, "已发送"), gin.H{"to": req.To})
}
