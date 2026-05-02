// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// agentRunsListHandler GET /api/agent/runs
// 管理员：分页查询 AgentRun（可按 user_id、session_id、status、时间筛选）。
func (h *Handlers) agentRunsListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	q := h.db.Model(&models.AgentRun{})
	if s := strings.TrimSpace(c.Query("user_id")); s != "" {
		q = q.Where("user_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("session_id")); s != "" {
		q = q.Where("session_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("status")); s != "" {
		q = q.Where("status = ?", s)
	}
	if s := strings.TrimSpace(c.Query("phase")); s != "" {
		q = q.Where("phase = ?", s)
	}
	if t, ok := models.ParseQueryTime(c, "from"); ok {
		q = q.Where("created_at >= ?", t)
	}
	if t, ok := models.ParseQueryTime(c, "to"); ok {
		q = q.Where("created_at <= ?", t)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}

	listQ := h.db.Model(&models.AgentRun{})
	if s := strings.TrimSpace(c.Query("user_id")); s != "" {
		listQ = listQ.Where("user_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("session_id")); s != "" {
		listQ = listQ.Where("session_id = ?", s)
	}
	if s := strings.TrimSpace(c.Query("status")); s != "" {
		listQ = listQ.Where("status = ?", s)
	}
	if s := strings.TrimSpace(c.Query("phase")); s != "" {
		listQ = listQ.Where("phase = ?", s)
	}
	if t, ok := models.ParseQueryTime(c, "from"); ok {
		listQ = listQ.Where("created_at >= ?", t)
	}
	if t, ok := models.ParseQueryTime(c, "to"); ok {
		listQ = listQ.Where("created_at <= ?", t)
	}

	var rows []models.AgentRun
	if err := listQ.Select(
		"id", "session_id", "user_id", "goal", "status", "phase",
		"total_steps", "total_tokens", "max_steps", "max_cost_tokens", "max_duration_ms",
		"started_at", "completed_at", "created_at", "updated_at",
	).Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}

	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	response.SuccessOK(c, gin.H{
		"list":      rows,
		"total":     total,
		"page":      page,
		"pageSize":  pageSize,
		"totalPage": totalPage,
	})
}

// agentRunDetailHandler GET /api/agent/runs/:id
func (h *Handlers) agentRunDetailHandler(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的 id"), nil)
		return
	}
	var row models.AgentRun
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "记录不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	response.SuccessOK(c, gin.H{"run": row})
}

// agentRunStepsListHandler GET /api/agent/runs/:id/steps
func (h *Handlers) agentRunStepsListHandler(c *gin.Context) {
	rid := strings.TrimSpace(c.Param("id"))
	if rid == "" {
		response.FailWithCode(c, 400, response.Msg(c, "无效的 run id"), nil)
		return
	}
	var run models.AgentRun
	if err := h.db.Where("id = ?", rid).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, response.Msg(c, "运行记录不存在"), nil)
			return
		}
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	var steps []models.AgentStep
	if err := h.db.Where("run_id = ?", rid).Order("created_at ASC").Order("step_id ASC").Find(&steps).Error; err != nil {
		response.Fail(c, response.Msg(c, "查询失败"), gin.H{"error": err.Error()})
		return
	}
	response.SuccessOK(c, gin.H{"run_id": run.ID, "list": steps})
}
