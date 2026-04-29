package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/LingByte/LingVoice/internal/listeners"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/notification/sms"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

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

func (h *Handlers) smsLogsListHandler(c *gin.Context) {
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize
	orgID := currentOrgID(c)

	q := h.db.Model(&sms.SMSLog{}).Where("org_id = ?", orgID)
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
	if s := strings.TrimSpace(c.Query("to_phone")); s != "" {
		q = q.Where("to_phone = ?", s)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	var list []sms.SMSLog
	if err := q.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
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

func (h *Handlers) smsLogDetailHandler(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	orgID := currentOrgID(c)
	var row sms.SMSLog
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
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	var req smsSendBody
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	orgID := currentOrgID(c)
	chans, err := listeners.EnabledSMSChannels(h.db, orgID)
	if err != nil {
		response.FailWithCode(c, 503, "未配置可用短信渠道", gin.H{"error": err.Error()})
		return
	}
	sender, err := sms.NewMultiSender(chans, h.db, c.ClientIP(), sms.WithSMSLogOrgID(orgID), sms.WithSMSLogUserID(u.ID))
	if err != nil {
		response.FailWithCode(c, 503, "短信服务不可用", gin.H{"error": err.Error()})
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
		response.Fail(c, "发送失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "已发送", gin.H{"to": req.To})
}
