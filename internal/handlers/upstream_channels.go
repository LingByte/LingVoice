// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---------- LLM ----------

type llmChannelWrite struct {
	Type               int                   `json:"type"`
	Key                string                `json:"key"`
	Name               string                `json:"name" binding:"required"`
	Protocol           string                `json:"protocol"`
	Status             int                   `json:"status"`
	OpenAIOrganization *string               `json:"openai_organization"`
	TestModel          *string               `json:"test_model"`
	BaseURL            *string               `json:"base_url"`
	Models             string                `json:"models"`
	Group              string                `json:"group"`
	ModelMapping       *string               `json:"model_mapping"`
	StatusCodeMapping  *string               `json:"status_code_mapping"`
	Priority           int64                 `json:"priority"`
	Weight             uint                  `json:"weight"`
	AutoBan            int                   `json:"auto_ban"`
	Tag                *string               `json:"tag"`
	ChannelInfo        models.LLMChannelInfo `json:"channel_info"`
}

func normalizeLLMProtocol(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return models.LLMChannelProtocolOpenAI
	}
	return p
}

func applyLLMWrite(w *llmChannelWrite, row *models.LLMChannel, keepKey bool) {
	row.Name = strings.TrimSpace(w.Name)
	if !keepKey || strings.TrimSpace(w.Key) != "" {
		row.Key = strings.TrimSpace(w.Key)
	}
	row.Type = w.Type
	row.Status = w.Status
	if row.Status == 0 {
		row.Status = 1
	}
	if strings.TrimSpace(w.Protocol) != "" {
		row.Protocol = normalizeLLMProtocol(w.Protocol)
	} else if !keepKey {
		row.Protocol = models.LLMChannelProtocolOpenAI
	}
	row.OpenAIOrganization = w.OpenAIOrganization
	row.TestModel = w.TestModel
	row.BaseURL = w.BaseURL
	row.Models = strings.TrimSpace(w.Models)
	row.Group = strings.TrimSpace(w.Group)
	if row.Group == "" {
		row.Group = "default"
	}
	row.ModelMapping = w.ModelMapping
	row.StatusCodeMapping = w.StatusCodeMapping
	pr := w.Priority
	row.Priority = &pr
	wgt := w.Weight
	if wgt == 0 {
		wgt = 1
	}
	row.Weight = &wgt
	ab := w.AutoBan
	if !keepKey && ab == 0 {
		ab = 1
	}
	row.AutoBan = &ab
	row.Tag = w.Tag
	row.ChannelInfo = w.ChannelInfo
}

func (h *Handlers) listLLMChannels(c *gin.Context) {
	q := h.db.Model(&models.LLMChannel{})
	if g := strings.TrimSpace(c.Query("group")); g != "" {
		q = q.Where("`group` = ?", g)
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
	var list []models.LLMChannel
	listQ := h.db.Model(&models.LLMChannel{})
	if g := strings.TrimSpace(c.Query("group")); g != "" {
		listQ = listQ.Where("`group` = ?", g)
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

// llmChannelCatalogItem 脱敏渠道摘要（不含 key），供凭证页等已登录用户使用。
type llmChannelCatalogItem struct {
	Id       int    `json:"id"`
	Name     string `json:"name"`
	Group    string `json:"group"`
	Protocol string `json:"protocol"`
	Models   string `json:"models"`
	Status   int    `json:"status"`
}

// listLLMChannelsCatalog GET /api/llm-channels/catalog 与 list 相同筛选与分页，但不返回 API Key 等敏感字段。
func (h *Handlers) listLLMChannelsCatalog(c *gin.Context) {
	q := h.db.Model(&models.LLMChannel{})
	if g := strings.TrimSpace(c.Query("group")); g != "" {
		q = q.Where("`group` = ?", g)
	}
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	if pageSize > 500 {
		pageSize = 500
	}
	offset := (page - 1) * pageSize

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	var list []models.LLMChannel
	listQ := h.db.Model(&models.LLMChannel{})
	if g := strings.TrimSpace(c.Query("group")); g != "" {
		listQ = listQ.Where("`group` = ?", g)
	}
	if err := listQ.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	out := make([]llmChannelCatalogItem, 0, len(list))
	for i := range list {
		ch := list[i]
		out = append(out, llmChannelCatalogItem{
			Id:       ch.Id,
			Name:     ch.Name,
			Group:    ch.Group,
			Protocol: ch.Protocol,
			Models:   ch.Models,
			Status:   ch.Status,
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

func (h *Handlers) getLLMChannel(c *gin.Context) {
	id, ok := parseIntParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.LLMChannel
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"channel": row})
}

func (h *Handlers) createLLMChannel(c *gin.Context) {
	var body llmChannelWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(body.Key) == "" {
		response.FailWithCode(c, 400, "key 不能为空", nil)
		return
	}
	now := time.Now().Unix()
	row := models.LLMChannel{
		CreatedTime:        now,
		TestTime:           0,
		ResponseTime:       0,
		Balance:            0,
		BalanceUpdatedTime: 0,
		UsedQuota:          0,
	}
	applyLLMWrite(&body, &row, false)
	if !models.IsLLMChannelProtocolKnown(row.Protocol) {
		response.FailWithCode(c, 400, "无效的 protocol，可选: openai, anthropic, coze, ollama, lmstudio", nil)
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return models.SyncLLMAbilitiesFromChannel(tx, &row)
	}); err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", row)
}

func (h *Handlers) updateLLMChannel(c *gin.Context) {
	id, ok := parseIntParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var body llmChannelWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var row models.LLMChannel
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	applyLLMWrite(&body, &row, true)
	if strings.TrimSpace(row.Key) == "" {
		response.FailWithCode(c, 400, "key 不能为空", nil)
		return
	}
	if strings.TrimSpace(body.Protocol) != "" && !models.IsLLMChannelProtocolKnown(row.Protocol) {
		response.FailWithCode(c, 400, "无效的 protocol", nil)
		return
	}
	if !models.IsLLMChannelProtocolKnown(row.Protocol) {
		row.Protocol = models.LLMChannelProtocolOpenAI
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return models.SyncLLMAbilitiesFromChannel(tx, &row)
	}); err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

func (h *Handlers) deleteLLMChannel(c *gin.Context) {
	id, ok := parseIntParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var probe models.LLMChannel
	if err := h.db.First(&probe, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("channel_id = ?", id).Delete(&models.LLMAbility{}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.LLMChannel{}, id).Error
	}); err != nil {
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "删除成功", gin.H{"id": id})
}

// ---------- ASR / TTS（结构相同，分表） ----------

type speechChannelWrite struct {
	Provider   string `json:"provider" binding:"required"`
	Name       string `json:"name" binding:"required"`
	Enabled    *bool  `json:"enabled"`
	Group      string `json:"group"`
	SortOrder  *int   `json:"sortOrder"`
	ConfigJSON string `json:"configJson"`
}

func normalizeASRProviderLocal(raw string) string {
	orig := strings.TrimSpace(raw)
	p := strings.ToLower(orig)
	switch p {
	case "tencent":
		return "qcloud"
	case "aliyun_funasr", "aliyun-funasr", "aliyun":
		return "funasr"
	case "volcengine_llm":
		return "volcllmasr"
	default:
		return orig
	}
}

func normalizeTTSProviderLocal(raw string) string {
	orig := strings.TrimSpace(raw)
	if strings.EqualFold(orig, "tencent") {
		return "qcloud"
	}
	return orig
}

func validateConfigJSON(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !json.Valid([]byte(s)) {
		return errors.New("configJson 须为合法 JSON")
	}
	return nil
}

func speechWriteToASR(w *speechChannelWrite, row *models.ASRChannel) {
	row.Provider = normalizeASRProviderLocal(strings.TrimSpace(w.Provider))
	row.Name = strings.TrimSpace(w.Name)
	row.Group = strings.TrimSpace(w.Group)
	if w.Enabled != nil {
		row.Enabled = *w.Enabled
	} else {
		row.Enabled = true
	}
	if w.SortOrder != nil {
		row.SortOrder = *w.SortOrder
	}
	row.ConfigJSON = strings.TrimSpace(w.ConfigJSON)
}

func speechWriteToTTS(w *speechChannelWrite, row *models.TTSChannel) {
	row.Provider = normalizeTTSProviderLocal(strings.TrimSpace(w.Provider))
	row.Name = strings.TrimSpace(w.Name)
	row.Group = strings.TrimSpace(w.Group)
	if w.Enabled != nil {
		row.Enabled = *w.Enabled
	} else {
		row.Enabled = true
	}
	if w.SortOrder != nil {
		row.SortOrder = *w.SortOrder
	}
	row.ConfigJSON = strings.TrimSpace(w.ConfigJSON)
}

func (h *Handlers) listASRChannels(c *gin.Context) {
	h.listSpeechLike(c, "asr")
}

func (h *Handlers) listTTSChannels(c *gin.Context) {
	h.listSpeechLike(c, "tts")
}

func (h *Handlers) listSpeechLike(c *gin.Context, kind string) {
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	switch kind {
	case "asr":
		q := h.db.Model(&models.ASRChannel{})
		if g := strings.TrimSpace(c.Query("group")); g != "" {
			q = q.Where("`group` = ?", g)
		}
		if p := strings.TrimSpace(c.Query("provider")); p != "" {
			q = q.Where("provider = ?", p)
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			response.Fail(c, "查询失败", gin.H{"error": err.Error()})
			return
		}
		var list []models.ASRChannel
		listQ := h.db.Model(&models.ASRChannel{})
		if g := strings.TrimSpace(c.Query("group")); g != "" {
			listQ = listQ.Where("`group` = ?", g)
		}
		if p := strings.TrimSpace(c.Query("provider")); p != "" {
			listQ = listQ.Where("provider = ?", p)
		}
		if err := listQ.Order("sort_order ASC, id ASC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
			response.Fail(c, "查询失败", gin.H{"error": err.Error()})
			return
		}
		totalPage := int(total) / pageSize
		if int(total)%pageSize != 0 {
			totalPage++
		}
		response.Success(c, "ok", gin.H{"list": list, "total": total, "page": page, "pageSize": pageSize, "totalPage": totalPage})
	case "tts":
		q := h.db.Model(&models.TTSChannel{})
		if g := strings.TrimSpace(c.Query("group")); g != "" {
			q = q.Where("`group` = ?", g)
		}
		if p := strings.TrimSpace(c.Query("provider")); p != "" {
			q = q.Where("provider = ?", p)
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			response.Fail(c, "查询失败", gin.H{"error": err.Error()})
			return
		}
		var list []models.TTSChannel
		listQ := h.db.Model(&models.TTSChannel{})
		if g := strings.TrimSpace(c.Query("group")); g != "" {
			listQ = listQ.Where("`group` = ?", g)
		}
		if p := strings.TrimSpace(c.Query("provider")); p != "" {
			listQ = listQ.Where("provider = ?", p)
		}
		if err := listQ.Order("sort_order ASC, id ASC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
			response.Fail(c, "查询失败", gin.H{"error": err.Error()})
			return
		}
		totalPage := int(total) / pageSize
		if int(total)%pageSize != 0 {
			totalPage++
		}
		response.Success(c, "ok", gin.H{"list": list, "total": total, "page": page, "pageSize": pageSize, "totalPage": totalPage})
	default:
		response.FailWithCode(c, 500, "内部错误", nil)
	}
}

func (h *Handlers) getASRChannel(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.ASRChannel
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"channel": row})
}

func (h *Handlers) getTTSChannel(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.TTSChannel
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"channel": row})
}

func (h *Handlers) createASRChannel(c *gin.Context) {
	var body speechChannelWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	if err := validateConfigJSON(body.ConfigJSON); err != nil {
		response.FailWithCode(c, 400, err.Error(), nil)
		return
	}
	row := models.ASRChannel{}
	speechWriteToASR(&body, &row)
	u := models.CurrentUser(c)
	row.SetCreateInfo(operatorFromUser(u))
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", row)
}

func (h *Handlers) createTTSChannel(c *gin.Context) {
	var body speechChannelWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	if err := validateConfigJSON(body.ConfigJSON); err != nil {
		response.FailWithCode(c, 400, err.Error(), nil)
		return
	}
	row := models.TTSChannel{}
	speechWriteToTTS(&body, &row)
	u := models.CurrentUser(c)
	row.SetCreateInfo(operatorFromUser(u))
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "创建成功", row)
}

func (h *Handlers) updateASRChannel(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var body speechChannelWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	if err := validateConfigJSON(body.ConfigJSON); err != nil {
		response.FailWithCode(c, 400, err.Error(), nil)
		return
	}
	var row models.ASRChannel
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	speechWriteToASR(&body, &row)
	row.SetUpdateInfo(operatorFromUser(models.CurrentUser(c)))
	if err := h.db.Save(&row).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

func (h *Handlers) updateTTSChannel(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var body speechChannelWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	if err := validateConfigJSON(body.ConfigJSON); err != nil {
		response.FailWithCode(c, 400, err.Error(), nil)
		return
	}
	var row models.TTSChannel
	if err := h.db.First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "渠道不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	speechWriteToTTS(&body, &row)
	row.SetUpdateInfo(operatorFromUser(models.CurrentUser(c)))
	if err := h.db.Save(&row).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", row)
}

func (h *Handlers) deleteASRChannel(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	res := h.db.Delete(&models.ASRChannel{}, id)
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

func (h *Handlers) deleteTTSChannel(c *gin.Context) {
	id, ok := parseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	res := h.db.Delete(&models.TTSChannel{}, id)
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
