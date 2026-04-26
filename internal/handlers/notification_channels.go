// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// emailChannelUpsertBody 邮件渠道表单（后端拼装 config_json）；编码由服务端生成。
type emailChannelUpsertBody struct {
	ChannelType string `json:"channelType" binding:"required,eq=email"`
	Driver      string `json:"driver" binding:"required,oneof=smtp sendcloud"`
	Name        string `json:"name" binding:"required,max=128"`

	SMTPHost     string `json:"smtpHost"`
	SMTPPort     int64  `json:"smtpPort"`
	SMTPUsername string `json:"smtpUsername"`
	SMTPPassword string `json:"smtpPassword"`
	SMTPFrom     string `json:"smtpFrom"`

	SendcloudAPIUser string `json:"sendcloudApiUser"`
	SendcloudAPIKey  string `json:"sendcloudApiKey"`
	SendcloudFrom    string `json:"sendcloudFrom"`
	FromDisplayName  string `json:"fromDisplayName"`

	SortOrder int    `json:"sortOrder"`
	Enabled   *bool  `json:"enabled"`
	Remark    string `json:"remark" binding:"max=128"`
}

func (req *emailChannelUpsertBody) buildConfigJSON() (string, error) {
	d := strings.ToLower(strings.TrimSpace(req.Driver))
	switch d {
	case "smtp":
		return BuildEmailChannelConfigJSON(
			"smtp", req.Name,
			req.SMTPHost, req.SMTPPort, req.SMTPUsername, req.SMTPPassword, req.SMTPFrom, req.FromDisplayName,
			"", "", "",
		)
	case "sendcloud":
		return BuildEmailChannelConfigJSON(
			"sendcloud", req.Name,
			"", 0, "", "", "", req.FromDisplayName,
			req.SendcloudAPIUser, req.SendcloudAPIKey, req.SendcloudFrom,
		)
	default:
		return "", fmt.Errorf("未知驱动: %s", d)
	}
}

func (h *Handlers) allocChannelCode() (string, error) {
	for i := 0; i < 16; i++ {
		code := fmt.Sprintf("%s-%s", emailChannelCodePrefix, randomChannelCode())
		var n int64
		if err := h.db.Model(&models.NotificationChannel{}).Where("code = ?", code).Count(&n).Error; err != nil {
			return "", err
		}
		if n == 0 {
			return code, nil
		}
	}
	return "", errors.New("无法生成唯一渠道编码")
}

func (h *Handlers) listNotificationChannels(c *gin.Context) {
	q := h.db.Model(&models.NotificationChannel{})
	if t := strings.TrimSpace(c.Query("type")); t != "" {
		q = q.Where("type = ?", t)
	}
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	var list []models.NotificationChannel
	listQ := h.db.Model(&models.NotificationChannel{})
	if t := strings.TrimSpace(c.Query("type")); t != "" {
		listQ = listQ.Where("type = ?", t)
	}
	if err := listQ.Order("type ASC, sort_order ASC, id ASC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
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

func (h *Handlers) getNotificationChannel(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.NotificationChannel
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	out := gin.H{"channel": row}
	if row.Type == models.NotificationChannelTypeEmail && strings.TrimSpace(row.ConfigJSON) != "" {
		if vf, err := DecodeEmailChannelForm(row.ConfigJSON); err == nil {
			out["emailForm"] = vf
		}
	}
	response.Success(c, "ok", out)
}

func (h *Handlers) createNotificationChannel(c *gin.Context) {
	var req emailChannelUpsertBody
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	if req.ChannelType != models.NotificationChannelTypeEmail {
		response.FailWithCode(c, 400, "当前仅支持 channelType=email", nil)
		return
	}
	cfgJSON, err := req.buildConfigJSON()
	if err != nil {
		response.FailWithCode(c, 400, err.Error(), nil)
		return
	}
	code, err := h.allocChannelCode()
	if err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	u := models.CurrentUser(c)
	row := models.NotificationChannel{
		Type:       models.NotificationChannelTypeEmail,
		Code:       code,
		Name:       strings.TrimSpace(req.Name),
		SortOrder:  req.SortOrder,
		Enabled:    true,
		ConfigJSON: cfgJSON,
	}
	if req.Enabled != nil {
		row.Enabled = *req.Enabled
	}
	row.Remark = req.Remark
	row.SetCreateInfo(operatorFromUser(u))
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", row)
}

func (h *Handlers) updateNotificationChannel(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req emailChannelUpsertBody
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var row models.NotificationChannel
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if row.Type != models.NotificationChannelTypeEmail {
		response.FailWithCode(c, 400, "该记录非邮件渠道，请使用后续专用接口编辑", nil)
		return
	}
	if req.ChannelType != models.NotificationChannelTypeEmail {
		response.FailWithCode(c, 400, "channelType 必须为 email", nil)
		return
	}
	cfgJSON, err := req.buildConfigJSON()
	if err != nil {
		response.FailWithCode(c, 400, err.Error(), nil)
		return
	}
	merged, err := MergeEmailSecretsOnUpdate(row.ConfigJSON, cfgJSON)
	if err != nil {
		merged = cfgJSON
	}
	row.Name = strings.TrimSpace(req.Name)
	row.SortOrder = req.SortOrder
	if req.Enabled != nil {
		row.Enabled = *req.Enabled
	}
	row.ConfigJSON = merged
	row.Remark = req.Remark
	row.SetUpdateInfo(operatorFromUser(models.CurrentUser(c)))
	if err := h.db.Save(&row).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

func (h *Handlers) deleteNotificationChannel(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	res := h.db.Delete(&models.NotificationChannel{}, id)
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
