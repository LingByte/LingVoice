// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
)

type llmModelPlazaCatalogItem struct {
	models.LLMModelMeta
	RoutableChannelCount int64    `json:"routable_channel_count"`
	AbilityGroups        []string `json:"ability_groups,omitempty"`
}

type plazaVendorCount struct {
	Vendor string `json:"vendor"`
	Count  int64  `json:"count"`
}

type plazaGroupCount struct {
	Group string `json:"group"`
	Count int64  `json:"count"`
}

type plazaBillingCount struct {
	Billing string `json:"billing"`
	Count   int64  `json:"count"`
}

// llmModelPlazaListHandler GET /api/llm-model-plaza
// 模型广场：筛选（供应商 / 能力分组 / 计费类型 / 关键词）+ 侧栏统计；登录用户可访问。
func (h *Handlers) llmModelPlazaListHandler(c *gin.Context) {
	q := h.db.Model(&models.LLMModelMeta{}).Where("status = ?", 1)
	if v := strings.TrimSpace(c.Query("vendor")); v != "" {
		if v == "__empty__" {
			q = q.Where("(vendor IS NULL OR TRIM(vendor) = '')")
		} else {
			q = q.Where("LOWER(TRIM(vendor)) = ?", strings.ToLower(v))
		}
	}
	if b := strings.ToLower(strings.TrimSpace(c.Query("billing"))); b == "token" || b == "times" {
		q = q.Where("LOWER(TRIM(quota_billing_mode)) = ?", b)
	}
	if g := strings.TrimSpace(c.Query("group")); g != "" {
		q = q.Where("model_name IN (SELECT DISTINCT model FROM llm_abilities WHERE `group` = ? AND enabled = 1)", g)
	}
	if s := strings.TrimSpace(c.Query("q")); s != "" {
		like := "%" + strings.ToLower(s) + "%"
		q = q.Where("LOWER(model_name) LIKE ? OR LOWER(COALESCE(description,'')) LIKE ? OR LOWER(COALESCE(tags,'')) LIKE ?", like, like, like)
	}

	var metas []models.LLMModelMeta
	if err := q.Order("sort_order ASC, id DESC").Find(&metas).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}

	catalog := make([]llmModelPlazaCatalogItem, 0, len(metas))
	for i := range metas {
		m := metas[i]
		var cnt int64
		_ = h.db.Raw(
			"SELECT COUNT(DISTINCT channel_id) FROM llm_abilities WHERE model = ? AND enabled = 1",
			m.ModelName,
		).Scan(&cnt).Error
		var groups []string
		_ = h.db.Model(&models.LLMAbility{}).
			Where("model = ?", m.ModelName).
			Distinct("`group`").
			Order("`group` ASC").
			Pluck("`group`", &groups).Error
		catalog = append(catalog, llmModelPlazaCatalogItem{
			LLMModelMeta:         m,
			RoutableChannelCount: cnt,
			AbilityGroups:        groups,
		})
	}

	var orphan []string
	if err := h.db.Raw(`
SELECT DISTINCT a.model FROM llm_abilities a
WHERE NOT EXISTS (SELECT 1 FROM llm_model_metas m WHERE m.model_name = a.model AND m.status = 1)
ORDER BY a.model
`).Scan(&orphan).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if orphan == nil {
		orphan = []string{}
	}

	var vendorCounts []plazaVendorCount
	_ = h.db.Model(&models.LLMModelMeta{}).
		Select(`COALESCE(NULLIF(TRIM(vendor), ''), '__empty__') AS vendor, COUNT(*) AS count`).
		Where("status = ?", 1).
		Group("COALESCE(NULLIF(TRIM(vendor), ''), '__empty__')").
		Order("count DESC").
		Scan(&vendorCounts).Error

	var groupCounts []plazaGroupCount
	_ = h.db.Model(&models.LLMAbility{}).
		Select("`group`, COUNT(DISTINCT model) AS count").
		Where("enabled = ?", 1).
		Group("`group`").
		Order("count DESC").
		Scan(&groupCounts).Error

	var billingCounts []plazaBillingCount
	_ = h.db.Model(&models.LLMModelMeta{}).
		Select(`COALESCE(NULLIF(TRIM(quota_billing_mode), ''), 'token') AS billing, COUNT(*) AS count`).
		Where("status = ?", 1).
		Group("COALESCE(NULLIF(TRIM(quota_billing_mode), ''), 'token')").
		Scan(&billingCounts).Error

	var totalMeta int64
	_ = h.db.Model(&models.LLMModelMeta{}).Where("status = ?", 1).Count(&totalMeta).Error

	response.Success(c, "ok", gin.H{
		"catalog":             catalog,
		"models_without_meta": orphan,
		"total_filtered":      len(catalog),
		"total_meta_enabled":  totalMeta,
		"vendor_counts":       vendorCounts,
		"group_counts":        groupCounts,
		"billing_counts":      billingCounts,
		"usd_per_quota_unit":  dashboardUSDPerQuotaUnit,
	})
}
