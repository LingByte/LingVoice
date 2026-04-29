// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type llmModelMetaWrite struct {
	ModelName       string `json:"model_name" binding:"required,max=255"`
	Description     string `json:"description"`
	Tags            string `json:"tags"`
	Status          *int   `json:"status"`
	IconURL         string `json:"icon_url"`
	Vendor          string `json:"vendor"`
	SortOrder       *int   `json:"sort_order"`
	ContextLength   *int   `json:"context_length"`
	MaxOutputTokens *int   `json:"max_output_tokens"`
	// 额度策略（与 new-api 模型倍率/按次/按 token/缓存折算思路对齐）
	QuotaBillingMode     *string  `json:"quota_billing_mode"`
	QuotaModelRatio      *float64 `json:"quota_model_ratio"`
	QuotaPromptRatio     *float64 `json:"quota_prompt_ratio"`
	QuotaCompletionRatio *float64 `json:"quota_completion_ratio"`
	QuotaCacheReadRatio  *float64 `json:"quota_cache_read_ratio"`
}

func mergeLLMModelMetaQuota(row *models.LLMModelMeta, body *llmModelMetaWrite, onCreate bool) {
	if onCreate {
		row.QuotaBillingMode = "token"
		row.QuotaModelRatio = 1
		row.QuotaPromptRatio = 1
		row.QuotaCompletionRatio = 1
		row.QuotaCacheReadRatio = 0.25
	}
	if body.QuotaBillingMode != nil {
		m := strings.ToLower(strings.TrimSpace(*body.QuotaBillingMode))
		if m == "times" || m == "count" || m == "token" || m == "tokens" {
			if m == "count" {
				m = "times"
			}
			if m == "tokens" {
				m = "token"
			}
			row.QuotaBillingMode = m
		}
	}
	if body.QuotaModelRatio != nil && *body.QuotaModelRatio > 0 {
		row.QuotaModelRatio = *body.QuotaModelRatio
	}
	if body.QuotaPromptRatio != nil && *body.QuotaPromptRatio > 0 {
		row.QuotaPromptRatio = *body.QuotaPromptRatio
	}
	if body.QuotaCompletionRatio != nil && *body.QuotaCompletionRatio > 0 {
		row.QuotaCompletionRatio = *body.QuotaCompletionRatio
	}
	if body.QuotaCacheReadRatio != nil && *body.QuotaCacheReadRatio >= 0 {
		row.QuotaCacheReadRatio = *body.QuotaCacheReadRatio
	}
	if strings.TrimSpace(row.QuotaBillingMode) == "" {
		row.QuotaBillingMode = "token"
	}
}

func (h *Handlers) llmModelMetasListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	q := h.db.Model(&models.LLMModelMeta{})
	if s := strings.TrimSpace(c.Query("q")); s != "" {
		like := "%" + strings.ToLower(s) + "%"
		q = q.Where("LOWER(model_name) LIKE ? OR LOWER(description) LIKE ? OR LOWER(tags) LIKE ?", like, like, like)
	}
	if s := strings.TrimSpace(c.Query("status")); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			q = q.Where("status = ?", v)
		}
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	listQ := h.db.Model(&models.LLMModelMeta{})
	if s := strings.TrimSpace(c.Query("q")); s != "" {
		like := "%" + strings.ToLower(s) + "%"
		listQ = listQ.Where("LOWER(model_name) LIKE ? OR LOWER(description) LIKE ? OR LOWER(tags) LIKE ?", like, like, like)
	}
	if s := strings.TrimSpace(c.Query("status")); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			listQ = listQ.Where("status = ?", v)
		}
	}
	var list []models.LLMModelMeta
	if err := listQ.Order("sort_order ASC, id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
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

func (h *Handlers) llmModelMetaDetailHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.LLMModelMeta
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "记录不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"meta": row})
}

func (h *Handlers) llmModelMetaCreateHandler(c *gin.Context) {
	var body llmModelMetaWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	now := time.Now().Unix()
	row := models.LLMModelMeta{
		ModelName:       strings.TrimSpace(body.ModelName),
		Description:     strings.TrimSpace(body.Description),
		Tags:            strings.TrimSpace(body.Tags),
		IconURL:         strings.TrimSpace(body.IconURL),
		Vendor:          strings.TrimSpace(body.Vendor),
		Status:          1,
		ContextLength:   body.ContextLength,
		MaxOutputTokens: body.MaxOutputTokens,
		CreatedTime:     now,
		UpdatedTime:     now,
	}
	if body.SortOrder != nil {
		row.SortOrder = *body.SortOrder
	}
	if body.Status != nil {
		row.Status = *body.Status
	}
	mergeLLMModelMetaQuota(&row, &body, true)
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", gin.H{"meta": row})
}

func (h *Handlers) llmModelMetaUpdateHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var body llmModelMetaWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var row models.LLMModelMeta
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "记录不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	row.ModelName = strings.TrimSpace(body.ModelName)
	row.Description = strings.TrimSpace(body.Description)
	row.Tags = strings.TrimSpace(body.Tags)
	row.IconURL = strings.TrimSpace(body.IconURL)
	row.Vendor = strings.TrimSpace(body.Vendor)
	if body.SortOrder != nil {
		row.SortOrder = *body.SortOrder
	}
	if body.ContextLength != nil {
		row.ContextLength = body.ContextLength
	}
	if body.MaxOutputTokens != nil {
		row.MaxOutputTokens = body.MaxOutputTokens
	}
	if body.Status != nil {
		row.Status = *body.Status
	}
	mergeLLMModelMetaQuota(&row, &body, false)
	row.UpdatedTime = time.Now().Unix()
	if err := h.db.Save(&row).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", gin.H{"meta": row})
}

func (h *Handlers) llmModelMetaDeleteHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	if err := h.db.Model(&models.LLMAbility{}).Where("model_meta_id = ?", id).Update("model_meta_id", nil).Error; err != nil {
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	res := h.db.Delete(&models.LLMModelMeta{}, id)
	if res.Error != nil {
		response.Fail(c, "删除失败", gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		response.FailWithCode(c, 404, "记录不存在", nil)
		return
	}
	response.Success(c, "已删除", nil)
}
