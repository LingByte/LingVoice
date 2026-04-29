// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Handlers) registerNotificationChannelRoutes(api *gin.RouterGroup) {
	nc := api.Group("/notification-channels")
	{
		nc.GET("", h.notificationChannelsListHandler)
		nc.POST("", h.notificationChannelCreateHandler)
		nc.GET("/:id", h.notificationChannelDetailHandler)
		nc.PUT("/:id", h.notificationChannelUpdateHandler)
		nc.DELETE("/:id", h.notificationChannelDeleteHandler)
	}
}

func (h *Handlers) registerInnerNotificationRoutes(api *gin.RouterGroup) {
	in := api.Group("/internal-notifications")
	in.Use(models.AuthRequired)
	{
		in.GET("", h.innerNotificationsListHandler)
		in.POST("", h.innerNotificationCreateHandler)
		in.GET("/:id", h.innerNotificationDetailHandler)
		in.PUT("/:id", h.innerNotificationUpdateHandler)
		in.PATCH("/:id/read", h.innerNotificationMarkReadHandler)
		in.DELETE("/:id", h.innerNotificationDeleteHandler)
	}
}

// NotificationChannelUpsertReq 创建/更新通知渠道（email/sms）。
// 为避免过度封装：后端仅负责校验、拼装 config_json；具体发送由各 provider 完成。
type NotificationChannelUpsertReq struct {
	ChannelType      string `json:"channelType" binding:"required,oneof=email sms"`
	Name             string `json:"name" binding:"required,max=128"`
	SortOrder        int    `json:"sortOrder"`
	Enabled          *bool  `json:"enabled"`
	Remark           string `json:"remark" binding:"max=128"`
	Driver           string `json:"driver"`
	SMTPHost         string `json:"smtpHost"`
	SMTPPort         int64  `json:"smtpPort"`
	SMTPUsername     string `json:"smtpUsername"`
	SMTPPassword     string `json:"smtpPassword"`
	SMTPFrom         string `json:"smtpFrom"`
	SendcloudAPIUser string `json:"sendcloudApiUser"`
	SendcloudAPIKey  string `json:"sendcloudApiKey"`
	SendcloudFrom    string `json:"sendcloudFrom"`
	FromDisplayName  string `json:"fromDisplayName"`
	SMSProvider      string `json:"smsProvider"` // yunpian|luosimao|twilio|huyi|juhe|chuanglan|submail...
	SMSConfig        any    `json:"smsConfig"`   // provider-specific config (stored as JSON)
}

type InnerNotificationCreateReq struct {
	UserID  uint   `json:"userId" binding:"required"`
	Title   string `json:"title" binding:"required,max=255"`
	Content string `json:"content" binding:"required"`
	Remark  string `json:"remark" binding:"max=128"`
}

type InnerNotificationUpdateReq struct {
	Title   string `json:"title" binding:"required,max=255"`
	Content string `json:"content" binding:"required"`
	Read    *bool  `json:"read"`
	Remark  string `json:"remark" binding:"max=128"`
}

type InnerNotificationMarkReadReq struct {
	Read *bool `json:"read"`
}

func (h *Handlers) notificationChannelsListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	t := strings.TrimSpace(c.Query("type"))
	orgID := models.CurrentOrgID(c)
	// minimal tenant filter: apply org_id if set
	out, err := models.ListNotificationChannels(h.db.Where("org_id = ?", orgID), t, page, pageSize)
	if err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{
		"list":      out.List,
		"total":     out.Total,
		"page":      out.Page,
		"pageSize":  out.PageSize,
		"totalPage": out.TotalPage,
	})
}

func (h *Handlers) notificationChannelDetailHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	row, err := models.GetNotificationChannel(h.db.Where("org_id = ?", orgID), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	out := gin.H{"channel": row}
	if row.Type == models.NotificationChannelTypeEmail && strings.TrimSpace(row.ConfigJSON) != "" {
		if vf, err := models.DecodeEmailChannelForm(row.ConfigJSON); err == nil {
			out["emailForm"] = vf
		}
	}
	if row.Type == models.NotificationChannelTypeSMS && strings.TrimSpace(row.ConfigJSON) != "" {
		if vf, err := models.DecodeSMSChannelForm(row.ConfigJSON); err == nil {
			out["smsForm"] = vf
		}
	}
	response.Success(c, "ok", out)
}

func (h *Handlers) notificationChannelCreateHandler(c *gin.Context) {
	var req NotificationChannelUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	cfgJSON := ""
	channelType := strings.ToLower(strings.TrimSpace(req.ChannelType))
	switch channelType {
	case models.NotificationChannelTypeEmail:
		d := strings.ToLower(strings.TrimSpace(req.Driver))
		var err error
		switch d {
		case "smtp":
			cfgJSON, err = models.BuildEmailChannelConfigJSON(
				"smtp", req.Name,
				req.SMTPHost, req.SMTPPort, req.SMTPUsername, req.SMTPPassword, req.SMTPFrom, req.FromDisplayName,
				"", "", "",
			)
		case "sendcloud":
			cfgJSON, err = models.BuildEmailChannelConfigJSON(
				"sendcloud", req.Name,
				"", 0, "", "", "", req.FromDisplayName,
				req.SendcloudAPIUser, req.SendcloudAPIKey, req.SendcloudFrom,
			)
		default:
			err = fmt.Errorf("未知邮件驱动: %s", d)
		}
		if err != nil {
			response.FailWithCode(c, 400, err.Error(), nil)
			return
		}
	case models.NotificationChannelTypeSMS:
		raw, err := models.BuildSMSChannelConfigJSON(req.SMSProvider, req.SMSConfig)
		if err != nil {
			response.FailWithCode(c, 400, err.Error(), nil)
			return
		}
		cfgJSON = raw
	default:
		response.FailWithCode(c, 400, "未知 channelType", nil)
		return
	}
	u := models.CurrentUser(c)
	orgID := models.CurrentOrgID(c)
	row := models.NotificationChannel{
		OrgID:      orgID,
		Type:       channelType,
		Code:       fmt.Sprintf("%s-%s", strings.ToUpper(channelType[:1]), base.SnowflakeUtil.GenID()),
		Name:       strings.TrimSpace(req.Name),
		SortOrder:  req.SortOrder,
		Enabled:    true,
		ConfigJSON: cfgJSON,
	}
	if req.Enabled != nil {
		row.Enabled = *req.Enabled
	}
	row.Remark = req.Remark
	row.SetCreateInfo(models.OperatorFromUser(u))
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", row)
}

func (h *Handlers) notificationChannelUpdateHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req NotificationChannelUpsertReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	orgID := models.CurrentOrgID(c)
	var row models.NotificationChannel
	if err := h.db.Where("org_id = ?", orgID).First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	channelType := strings.ToLower(strings.TrimSpace(req.ChannelType))
	if channelType != strings.ToLower(strings.TrimSpace(row.Type)) {
		response.FailWithCode(c, 400, "channelType 不匹配", nil)
		return
	}
	cfgJSON := ""
	switch channelType {
	case models.NotificationChannelTypeEmail:
		d := strings.ToLower(strings.TrimSpace(req.Driver))
		var err error
		switch d {
		case "smtp":
			cfgJSON, err = models.BuildEmailChannelConfigJSON(
				"smtp", req.Name,
				req.SMTPHost, req.SMTPPort, req.SMTPUsername, req.SMTPPassword, req.SMTPFrom, req.FromDisplayName,
				"", "", "",
			)
		case "sendcloud":
			cfgJSON, err = models.BuildEmailChannelConfigJSON(
				"sendcloud", req.Name,
				"", 0, "", "", "", req.FromDisplayName,
				req.SendcloudAPIUser, req.SendcloudAPIKey, req.SendcloudFrom,
			)
		default:
			err = fmt.Errorf("未知邮件驱动: %s", d)
		}
		if err != nil {
			response.FailWithCode(c, 400, err.Error(), nil)
			return
		}
		merged, err := models.MergeEmailSecretsOnUpdate(row.ConfigJSON, cfgJSON)
		if err != nil {
			row.ConfigJSON = cfgJSON
		} else {
			row.ConfigJSON = merged
		}
	case models.NotificationChannelTypeSMS:
		raw, err := models.BuildSMSChannelConfigJSON(req.SMSProvider, req.SMSConfig)
		if err != nil {
			response.FailWithCode(c, 400, err.Error(), nil)
			return
		}
		merged, err := models.MergeSMSSecretsOnUpdate(row.ConfigJSON, raw)
		if err != nil {
			row.ConfigJSON = raw
		} else {
			row.ConfigJSON = merged
		}
	default:
		response.FailWithCode(c, 400, "未知 channelType", nil)
		return
	}
	row.Name = strings.TrimSpace(req.Name)
	row.SortOrder = req.SortOrder
	if req.Enabled != nil {
		row.Enabled = *req.Enabled
	}
	row.Remark = req.Remark
	row.SetUpdateInfo(models.OperatorFromUser(models.CurrentUser(c)))
	if err := h.db.Save(&row).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

func (h *Handlers) notificationChannelDeleteHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := models.CurrentOrgID(c)
	res := h.db.Where("org_id = ?", orgID).Delete(&models.NotificationChannel{}, id)
	if res.Error != nil {
		response.Fail(c, "删除失败", gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		response.FailWithCode(c, 404, "渠道不存在", nil)
		return
	}
	response.Success(c, "删除成功", gin.H{"id": id})
}

func (h *Handlers) innerNotificationsListHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))

	var filterUserID *uint
	if u.IsAdmin() {
		if uid, ok := models.ParseQueryUint(c, "userId"); ok {
			filterUserID = &uid
		}
	}

	out, err := models.ListInternalNotifications(h.db, u, filterUserID, page, pageSize)
	if err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{
		"list":      out.List,
		"total":     out.Total,
		"page":      out.Page,
		"pageSize":  out.PageSize,
		"totalPage": out.TotalPage,
	})
}

func (h *Handlers) innerNotificationDetailHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	row, err := models.GetInternalNotificationByID(h.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "通知不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if !u.IsAdmin() && row.UserID != u.ID {
		response.FailWithCode(c, 403, "无权访问", nil)
		return
	}
	response.Success(c, "ok", row)
}

func (h *Handlers) innerNotificationCreateHandler(c *gin.Context) {
	if !models.RequireAdmin(c) {
		return
	}
	var req InnerNotificationCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	admin := models.CurrentUser(c)
	row := models.InternalNotification{
		UserID:  req.UserID,
		Title:   req.Title,
		Content: req.Content,
		Remark:  req.Remark,
		Read:    false,
	}
	row.SetCreateInfo(models.OperatorFromUser(admin))
	if err := models.CreateInternalNotification(h.db, &row); err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", row)
}

func (h *Handlers) innerNotificationUpdateHandler(c *gin.Context) {
	if !models.RequireAdmin(c) {
		return
	}
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req InnerNotificationUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	row, err := models.GetInternalNotificationByID(h.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "通知不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	admin := models.CurrentUser(c)
	row.Title = req.Title
	row.Content = req.Content
	row.Remark = req.Remark
	if req.Read != nil {
		row.Read = *req.Read
	}
	row.SetUpdateInfo(models.OperatorFromUser(admin))
	if err := models.SaveInternalNotification(h.db, row); err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

func (h *Handlers) innerNotificationMarkReadHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	row, err := models.GetInternalNotificationByID(h.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "通知不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if !u.IsAdmin() && row.UserID != u.ID {
		response.FailWithCode(c, 403, "无权操作", nil)
		return
	}
	read := true
	if c.Request.ContentLength > 0 {
		var body InnerNotificationMarkReadReq
		if err := c.ShouldBindJSON(&body); err == nil && body.Read != nil {
			read = *body.Read
		}
	}
	if err := models.PatchInternalNotificationRead(h.db, id, read, models.OperatorFromUser(u)); err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	fresh, err := models.GetInternalNotificationByID(h.db, id)
	if err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", fresh)
}

func (h *Handlers) innerNotificationDeleteHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	row, err := models.GetInternalNotificationByID(h.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "通知不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if !u.IsAdmin() && row.UserID != u.ID {
		response.FailWithCode(c, 403, "无权删除", nil)
		return
	}
	if err := models.DeleteInternalNotification(h.db, row); err != nil {
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "删除成功", gin.H{"id": id})
}
