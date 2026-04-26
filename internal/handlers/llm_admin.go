// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"sort"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
)

type llmAdminChannelOption struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Group    string `json:"group"`
	Protocol string `json:"protocol"`
	Models   string `json:"models"`
	Status   int    `json:"status"`
}

// getLLMAdminFormOptions GET /api/llm-admin/form-options 下拉：模型元数据、渠道、以及渠道 models 字段解析出的候选名。
func (h *Handlers) getLLMAdminFormOptions(c *gin.Context) {
	group := strings.TrimSpace(c.Query("group"))
	q := h.db.Model(&models.LLMChannel{}).Order("id DESC").Limit(500)
	if group != "" {
		q = q.Where("`group` = ?", group)
	}
	var chs []models.LLMChannel
	if err := q.Find(&chs).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	suggest := map[string]struct{}{}
	chOpts := make([]llmAdminChannelOption, 0, len(chs))
	for i := range chs {
		ch := chs[i]
		chOpts = append(chOpts, llmAdminChannelOption{
			ID:       ch.Id,
			Name:     ch.Name,
			Group:    ch.Group,
			Protocol: ch.Protocol,
			Models:   ch.Models,
			Status:   ch.Status,
		})
		for _, m := range models.SplitLLMModelNamesCSV(ch.Models) {
			suggest[m] = struct{}{}
		}
	}
	var metas []models.LLMModelMeta
	if err := h.db.Where("status = ?", 1).Order("sort_order ASC, id ASC").Find(&metas).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	for i := range metas {
		if strings.TrimSpace(metas[i].ModelName) != "" {
			suggest[metas[i].ModelName] = struct{}{}
		}
	}
	names := make([]string, 0, len(suggest))
	for m := range suggest {
		names = append(names, m)
	}
	sort.Strings(names)
	response.Success(c, "ok", gin.H{
		"model_metas":          metas,
		"channels":             chOpts,
		"model_name_suggestions": names,
	})
}
