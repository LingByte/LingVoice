// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type internalNotificationCreateReq struct {
	UserID  uint   `json:"userId" binding:"required"`
	Title   string `json:"title" binding:"required,max=255"`
	Content string `json:"content" binding:"required"`
	Remark  string `json:"remark" binding:"max=128"`
}

type internalNotificationUpdateReq struct {
	Title   string `json:"title" binding:"required,max=255"`
	Content string `json:"content" binding:"required"`
	Read    *bool  `json:"read"`
	Remark  string `json:"remark" binding:"max=128"`
}

type markReadReq struct {
	Read *bool `json:"read"`
}

func (h *Handlers) listInternalNotifications(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	q := h.db.Model(&models.InternalNotification{})
	if u.IsAdmin() {
		if uid, ok := parseQueryUint(c, "userId"); ok {
			q = q.Where("user_id = ?", uid)
		}
	} else {
		q = q.Where("user_id = ?", u.ID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	var list []models.InternalNotification
	listQ := h.db.Model(&models.InternalNotification{})
	if u.IsAdmin() {
		if uid, ok := parseQueryUint(c, "userId"); ok {
			listQ = listQ.Where("user_id = ?", uid)
		}
	} else {
		listQ = listQ.Where("user_id = ?", u.ID)
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

func (h *Handlers) getInternalNotification(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.InternalNotification
	if err := h.db.First(&row, id).Error; err != nil {
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

func (h *Handlers) createInternalNotification(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	var req internalNotificationCreateReq
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
	row.SetCreateInfo(operatorFromUser(admin))
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", row)
}

func (h *Handlers) updateInternalNotification(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var req internalNotificationUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var row models.InternalNotification
	if err := h.db.First(&row, id).Error; err != nil {
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
	row.SetUpdateInfo(operatorFromUser(admin))
	if err := h.db.Save(&row).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

func (h *Handlers) markInternalNotificationRead(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.InternalNotification
	if err := h.db.First(&row, id).Error; err != nil {
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
		var body markReadReq
		if err := c.ShouldBindJSON(&body); err == nil && body.Read != nil {
			read = *body.Read
		}
	}
	row.Read = read
	row.SetUpdateInfo(operatorFromUser(u))
	if err := h.db.Model(&row).Updates(map[string]interface{}{
		"read":      row.Read,
		"update_by": row.UpdateBy,
	}).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", row)
}

func (h *Handlers) deleteInternalNotification(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.InternalNotification
	if err := h.db.First(&row, id).Error; err != nil {
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
	if err := h.db.Delete(&row).Error; err != nil {
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "删除成功", gin.H{"id": id})
}
