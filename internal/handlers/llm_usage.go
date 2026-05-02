// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strconv"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// userLLMUsageListHandler GET /api/me/llm-usage — 仅当前登录用户本人的用量分页（不要求管理员；忽略 user_id 查询参数）。
func (h *Handlers) userLLMUsageListHandler(c *gin.Context) {
	h.listLLMUsageInternal(c, true)
}

// llmUsageListHandler GET /api/llm-usage — 管理员可查全量并按 user_id 等筛选；非管理员等同仅本人。
func (h *Handlers) llmUsageListHandler(c *gin.Context) {
	h.listLLMUsageInternal(c, false)
}

// listLLMUsageInternal selfOnly 为 true 时恒按当前用户过滤（供「使用日志」等入口专用）。
func (h *Handlers) listLLMUsageInternal(c *gin.Context, selfOnly bool) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	selfID := strconv.FormatUint(uint64(u.ID), 10)
	forcedUserID := ""
	if selfOnly {
		forcedUserID = selfID
	} else if !u.IsAdmin() {
		forcedUserID = selfID
	}
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	q := h.db.Model(&models.LLMUsage{})
	if forcedUserID != "" {
		q = q.Where("user_id = ?", forcedUserID)
	} else if s := strings.TrimSpace(c.Query("user_id")); s != "" {
		q = q.Where("user_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("channel_id")); s != "" {
		if cid, err := strconv.Atoi(s); err == nil && cid > 0 {
			q = q.Where("channel_id = ?", cid)
		}
	}
	if s := strings.TrimSpace(c.Query("request_id")); s != "" {
		q = q.Where("request_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("provider")); s != "" {
		q = q.Where("provider = ?", s)
	}
	if s := strings.TrimSpace(c.Query("model")); s != "" {
		q = q.Where("model = ?", s)
	}
	if s := strings.TrimSpace(c.Query("request_type")); s != "" {
		q = q.Where("request_type = ?", s)
	}
	if v, ok := models.ParseQueryBool(c, "success"); ok {
		q = q.Where("success = ?", v)
	}
	if t, ok := models.ParseQueryTime(c, "from"); ok {
		q = q.Where("completed_at >= ?", t)
	}
	if t, ok := models.ParseQueryTime(c, "to"); ok {
		q = q.Where("completed_at <= ?", t)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}

	listQ := h.db.Model(&models.LLMUsage{})
	if forcedUserID != "" {
		listQ = listQ.Where("user_id = ?", forcedUserID)
	} else if s := strings.TrimSpace(c.Query("user_id")); s != "" {
		listQ = listQ.Where("user_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("channel_id")); s != "" {
		if cid, err := strconv.Atoi(s); err == nil && cid > 0 {
			listQ = listQ.Where("channel_id = ?", cid)
		}
	}
	if s := strings.TrimSpace(c.Query("request_id")); s != "" {
		listQ = listQ.Where("request_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("provider")); s != "" {
		listQ = listQ.Where("provider = ?", s)
	}
	if s := strings.TrimSpace(c.Query("model")); s != "" {
		listQ = listQ.Where("model = ?", s)
	}
	if s := strings.TrimSpace(c.Query("request_type")); s != "" {
		listQ = listQ.Where("request_type = ?", s)
	}
	if v, ok := models.ParseQueryBool(c, "success"); ok {
		listQ = listQ.Where("success = ?", v)
	}
	if t, ok := models.ParseQueryTime(c, "from"); ok {
		listQ = listQ.Where("completed_at >= ?", t)
	}
	if t, ok := models.ParseQueryTime(c, "to"); ok {
		listQ = listQ.Where("completed_at <= ?", t)
	}

	var list []models.LLMUsage
	if err := listQ.Order("created_at DESC").Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
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

// userLLMUsageDetailHandler GET /api/me/llm-usage/:id — 仅当记录属于当前用户时返回（不要求管理员）。
func (h *Handlers) userLLMUsageDetailHandler(c *gin.Context) {
	h.getLLMUsageInternal(c, true)
}

// llmUsageDetailHandler GET /api/llm-usage/:id — 管理员可查任意；非管理员仅可查本人记录。
func (h *Handlers) llmUsageDetailHandler(c *gin.Context) {
	h.getLLMUsageInternal(c, false)
}

func (h *Handlers) getLLMUsageInternal(c *gin.Context, selfOnly bool) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, response.Msg(c, "未登录"), nil)
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的 id"), nil)
		return
	}
	var row models.LLMUsage
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "记录不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	self := strconv.FormatUint(uint64(u.ID), 10)
	if selfOnly {
		if row.UserID != self {
			response.FailWithCode(c, 403, response.Msg(c, "无权访问该记录"), nil)
			return
		}
	} else if !u.IsAdmin() {
		if row.UserID != self {
			response.FailWithCode(c, 403, response.Msg(c, "无权访问该记录"), nil)
			return
		}
	}
	response.SuccessOK(c, gin.H{"usage": row})
}
