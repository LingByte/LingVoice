// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"sort"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type llmAbilityWrite struct {
	Group       string  `json:"group" binding:"required"`
	Model       string  `json:"model"`
	ModelMetaID *uint   `json:"model_meta_id"`
	ChannelId   int     `json:"channel_id" binding:"required"`
	Enabled     *bool   `json:"enabled"`
	Priority    *int64  `json:"priority"`
	Weight      *uint   `json:"weight"`
	Tag         *string `json:"tag"`
}

type llmAbilityListRow struct {
	models.LLMAbility
	ChannelName string `json:"channel_name,omitempty"`
}

func channelNameMap(db *gorm.DB, channelIDs []int) map[int]string {
	if len(channelIDs) == 0 {
		return nil
	}
	var chs []models.LLMChannel
	_ = db.Select("id", "name").Where("id IN ?", channelIDs).Find(&chs).Error
	out := make(map[int]string, len(chs))
	for i := range chs {
		out[chs[i].Id] = chs[i].Name
	}
	return out
}

func (h *Handlers) llmAbilitiesListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	q := h.db.Model(&models.LLMAbility{})
	if g := strings.TrimSpace(c.Query("group")); g != "" {
		q = q.Where("`group` = ?", g)
	}
	if m := strings.TrimSpace(c.Query("model")); m != "" {
		q = q.Where("model = ?", m)
	}
	if cid := models.ParseQueryInt(c, "channel_id", 0); cid > 0 {
		q = q.Where("channel_id = ?", cid)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	listQ := h.db.Model(&models.LLMAbility{})
	if g := strings.TrimSpace(c.Query("group")); g != "" {
		listQ = listQ.Where("`group` = ?", g)
	}
	if m := strings.TrimSpace(c.Query("model")); m != "" {
		listQ = listQ.Where("model = ?", m)
	}
	if cid := models.ParseQueryInt(c, "channel_id", 0); cid > 0 {
		listQ = listQ.Where("channel_id = ?", cid)
	}
	var list []models.LLMAbility
	if err := listQ.Order("priority DESC").Order("weight DESC").Order("channel_id ASC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	ids := make([]int, 0, len(list))
	for i := range list {
		ids = append(ids, list[i].ChannelId)
	}
	names := channelNameMap(h.db, ids)
	out := make([]llmAbilityListRow, 0, len(list))
	for i := range list {
		out = append(out, llmAbilityListRow{
			LLMAbility:  list[i],
			ChannelName: names[list[i].ChannelId],
		})
	}
	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	response.Success(c, "ok", gin.H{
		"list":      out,
		"total":     total,
		"page":      page,
		"pageSize":  pageSize,
		"totalPage": totalPage,
	})
}

func (h *Handlers) llmAbilityCreateHandler(c *gin.Context) {
	var body llmAbilityWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	model := strings.TrimSpace(body.Model)
	var metaID *uint
	if body.ModelMetaID != nil && *body.ModelMetaID > 0 {
		var meta models.LLMModelMeta
		if err := h.db.First(&meta, *body.ModelMetaID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				response.FailWithCode(c, 400, "模型元数据不存在", nil)
				return
			}
			response.Fail(c, "查询失败", gin.H{"error": err.Error()})
			return
		}
		model = strings.TrimSpace(meta.ModelName)
		id := meta.Id
		metaID = &id
	}
	if model == "" {
		response.FailWithCode(c, 400, "请填写 model 或 model_meta_id", nil)
		return
	}
	var ch models.LLMChannel
	if err := h.db.First(&ch, body.ChannelId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 400, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	row := models.LLMAbility{
		Group:       strings.TrimSpace(body.Group),
		Model:       model,
		ChannelId:   body.ChannelId,
		ModelMetaID: metaID,
		Enabled:     true,
		Priority:    0,
		Weight:      1,
		Tag:         body.Tag,
	}
	if body.Enabled != nil {
		row.Enabled = *body.Enabled
	}
	if body.Priority != nil {
		row.Priority = *body.Priority
	}
	if body.Weight != nil && *body.Weight > 0 {
		row.Weight = *body.Weight
	}
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", gin.H{"ability": row})
}

type llmAbilityPatchBody struct {
	Enabled     *bool   `json:"enabled"`
	Priority    *int64  `json:"priority"`
	Weight      *uint   `json:"weight"`
	Tag         *string `json:"tag"`
	ModelMetaID *uint   `json:"model_meta_id"`
}

func (h *Handlers) llmAbilityPatchHandler(c *gin.Context) {
	group := strings.TrimSpace(c.Query("group"))
	model := strings.TrimSpace(c.Query("model"))
	cid := models.ParseQueryInt(c, "channel_id", 0)
	if group == "" || model == "" || cid <= 0 {
		response.FailWithCode(c, 400, "请提供 query: group, model, channel_id", nil)
		return
	}
	var body llmAbilityPatchBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var row models.LLMAbility
	if err := h.db.Where("`group` = ? AND model = ? AND channel_id = ?", group, model, cid).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "记录不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	vals := map[string]any{}
	if body.Enabled != nil {
		vals["enabled"] = *body.Enabled
	}
	if body.Priority != nil {
		vals["priority"] = *body.Priority
	}
	if body.Weight != nil {
		vals["weight"] = *body.Weight
	}
	if body.Tag != nil {
		vals["tag"] = body.Tag
	}
	if body.ModelMetaID != nil {
		if *body.ModelMetaID == 0 {
			vals["model_meta_id"] = nil
		} else {
			var meta models.LLMModelMeta
			if err := h.db.First(&meta, *body.ModelMetaID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					response.FailWithCode(c, 400, "模型元数据不存在", nil)
					return
				}
				response.Fail(c, "查询失败", gin.H{"error": err.Error()})
				return
			}
			if strings.TrimSpace(meta.ModelName) != row.Model {
				response.FailWithCode(c, 400, "model_meta 的 model_name 与当前能力 model 不一致", nil)
				return
			}
			vals["model_meta_id"] = *body.ModelMetaID
		}
	}
	if len(vals) == 0 {
		response.FailWithCode(c, 400, "无可更新字段", nil)
		return
	}
	if err := h.db.Model(&row).Updates(vals).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	_ = h.db.Where("`group` = ? AND model = ? AND channel_id = ?", group, model, cid).First(&row).Error
	response.Success(c, "已更新", gin.H{"ability": row})
}

func (h *Handlers) llmAbilityDeleteHandler(c *gin.Context) {
	group := strings.TrimSpace(c.Query("group"))
	model := strings.TrimSpace(c.Query("model"))
	cid := models.ParseQueryInt(c, "channel_id", 0)
	if group == "" || model == "" || cid <= 0 {
		response.FailWithCode(c, 400, "请提供 query: group, model, channel_id", nil)
		return
	}
	res := h.db.Where("`group` = ? AND model = ? AND channel_id = ?", group, model, cid).Delete(&models.LLMAbility{})
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

// llmAbilitiesSyncChannelHandler POST /api/llm-abilities/sync-channel/:id 根据渠道 models/group 重建能力表。
func (h *Handlers) llmAbilitiesSyncChannelHandler(c *gin.Context) {
	id, ok := models.ParseIntParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var ch models.LLMChannel
	if err := h.db.First(&ch, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if err := models.SyncLLMAbilitiesFromChannel(h.db, &ch); err != nil {
		response.Fail(c, "同步失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "已同步", gin.H{"channel_id": id})
}

type llmAdminChannelOption struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Group    string `json:"group"`
	Protocol string `json:"protocol"`
	Models   string `json:"models"`
	Status   int    `json:"status"`
}

// llmAdminFormOptionsHandler GET /api/llm-admin/form-options 下拉：模型元数据、渠道、以及渠道 models 字段解析出的候选名。
func (h *Handlers) llmAdminFormOptionsHandler(c *gin.Context) {
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
		"model_metas":            metas,
		"channels":               chOpts,
		"model_name_suggestions": names,
	})
}
