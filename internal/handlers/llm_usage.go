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
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func parseQueryTime(c *gin.Context, name string) (time.Time, bool) {
	s := strings.TrimSpace(c.Query(name))
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func parseQueryBool(c *gin.Context, name string) (bool, bool) {
	s := strings.ToLower(strings.TrimSpace(c.Query(name)))
	if s == "" {
		return false, false
	}
	switch s {
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return false, true
	default:
		return false, false
	}
}

// listLLMUsage 分页查询 LLM 用量（需登录且管理员）。
func (h *Handlers) listLLMUsage(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	q := h.db.Model(&models.LLMUsage{})
	if s := strings.TrimSpace(c.Query("user_id")); s != "" {
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
	if v, ok := parseQueryBool(c, "success"); ok {
		q = q.Where("success = ?", v)
	}
	if t, ok := parseQueryTime(c, "from"); ok {
		q = q.Where("completed_at >= ?", t)
	}
	if t, ok := parseQueryTime(c, "to"); ok {
		q = q.Where("completed_at <= ?", t)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}

	listQ := h.db.Model(&models.LLMUsage{})
	if s := strings.TrimSpace(c.Query("user_id")); s != "" {
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
	if v, ok := parseQueryBool(c, "success"); ok {
		listQ = listQ.Where("success = ?", v)
	}
	if t, ok := parseQueryTime(c, "from"); ok {
		listQ = listQ.Where("completed_at >= ?", t)
	}
	if t, ok := parseQueryTime(c, "to"); ok {
		listQ = listQ.Where("completed_at <= ?", t)
	}

	var list []models.LLMUsage
	if err := listQ.Order("created_at DESC").Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
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

// getLLMUsage 按主键 id 查询单条用量（需登录且管理员）。
func (h *Handlers) getLLMUsage(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.LLMUsage
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "记录不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"usage": row})
}
