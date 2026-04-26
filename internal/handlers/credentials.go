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
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	credentialStatusActive   = 1
	credentialStatusDisabled = 0
)

type credentialCreateBody struct {
	Kind            string `json:"kind" binding:"required"`
	Name            string `json:"name" binding:"required,min=1,max=128"`
	RemainQuota     int    `json:"remain_quota"`
	UnlimitedQuota  bool   `json:"unlimited_quota"`
	AllowIps        string `json:"allow_ips"`
	Group           string `json:"group" binding:"max=128"`
	CrossGroupRetry bool   `json:"cross_group_retry"`
	ExpiredTime     int64  `json:"expired_time"`
}

type credentialUpdateBody struct {
	Name                    string  `json:"name" binding:"required,min=1,max=128"`
	Status                  int     `json:"status"` // 0 或 1
	RemainQuota             int     `json:"remain_quota"`
	UnlimitedQuota          bool    `json:"unlimited_quota"`
	AllowIps                string  `json:"allow_ips"`
	Group                   string  `json:"group" binding:"max=128"`
	CrossGroupRetry         bool   `json:"cross_group_retry"`
	ExpiredTime             int64  `json:"expired_time"`
	OpenAPIModelCatalogJSON string `json:"openapi_model_catalog"`
}

func credentialKindError(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	switch k {
	case models.CredentialKindLLM, models.CredentialKindASR, models.CredentialKindTTS, models.CredentialKindEmail:
		return ""
	default:
		return "不支持的凭证类型 kind，可选：llm、asr、tts、email"
	}
}

func allowIPsPtr(s string) *string {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	return &t
}

func credentialToPublic(row *models.Credential) gin.H {
	if row == nil {
		return nil
	}
	var allow any
	if row.AllowIps != nil {
		allow = *row.AllowIps
	}
	return gin.H{
		"id":                   row.Id,
		"user_id":              row.UserId,
		"kind":                 row.Kind,
		"key_masked":           models.MaskTokenKey(row.Key),
		"status":               row.Status,
		"name":                 row.Name,
		"extra":                jsonRawIfObject(row.ExtraJSON),
		"openapi_model_catalog": jsonRawIfJSONArray(row.OpenAPIModelCatalogJSON),
		"created_time":         row.CreatedTime,
		"accessed_time":        row.AccessedTime,
		"expired_time":         row.ExpiredTime,
		"remain_quota":         row.RemainQuota,
		"unlimited_quota":      row.UnlimitedQuota,
		"used_quota":           row.UsedQuota,
		"model_limits_enabled": row.ModelLimitsEnabled,
		"model_limits":         row.ModelLimits,
		"allow_ips":            allow,
		"group":                row.Group,
		"cross_group_retry":    row.CrossGroupRetry,
	}
}

func jsonRawIfObject(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return map[string]any{}
	}
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	return s
}

func jsonRawIfJSONArray(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return []any{}
	}
	var v []any
	if json.Unmarshal([]byte(s), &v) == nil {
		return v
	}
	var objs []map[string]any
	if json.Unmarshal([]byte(s), &objs) == nil {
		return objs
	}
	return s
}

func (h *Handlers) listCredentials(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	kindFilter := strings.ToLower(strings.TrimSpace(c.Query("kind")))
	q := h.db.Model(&models.Credential{}).Where("user_id = ?", int(u.ID))
	if kindFilter != "" {
		if msg := credentialKindError(kindFilter); msg != "" {
			response.FailWithCode(c, 400, msg, nil)
			return
		}
		q = q.Where("kind = ?", kindFilter)
	}
	if g := strings.TrimSpace(c.Query("group")); g != "" {
		q = q.Where("`group` = ?", g)
	}
	var rows []models.Credential
	if err := q.Order("id DESC").Find(&rows).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for i := range rows {
		out = append(out, credentialToPublic(&rows[i]))
	}
	response.Success(c, "ok", gin.H{"list": out})
}

// listCredentialGroups 返回当前用户凭证中已使用过的分组名（去重、非空），供前端筛选。
func (h *Handlers) listCredentialGroups(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	var groups []string
	if err := h.db.Model(&models.Credential{}).
		Distinct("`group`").
		Where("user_id = ? AND TRIM(COALESCE(`group`,'')) != ''", int(u.ID)).
		Order("`group` ASC").
		Pluck("`group`", &groups).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"groups": groups})
}

func (h *Handlers) createCredential(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	var body credentialCreateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	if msg := credentialKindError(body.Kind); msg != "" {
		response.FailWithCode(c, 400, msg, nil)
		return
	}
	exp := body.ExpiredTime
	if exp == 0 {
		exp = -1
	}
	now := time.Now().Unix()
	base := models.Credential{
		UserId:             int(u.ID),
		Kind:               strings.ToLower(strings.TrimSpace(body.Kind)),
		Status:             credentialStatusActive,
		Name:               strings.TrimSpace(body.Name),
		ExtraJSON:          "",
		CreatedTime:        now,
		AccessedTime:       now,
		ExpiredTime:        exp,
		RemainQuota:        body.RemainQuota,
		UnlimitedQuota:     body.UnlimitedQuota,
		UsedQuota:          0,
		ModelLimitsEnabled: false,
		ModelLimits:        "",
		AllowIps:           allowIPsPtr(body.AllowIps),
		Group:              strings.TrimSpace(body.Group),
		CrossGroupRetry:    body.CrossGroupRetry,
	}
	for i := 0; i < 8; i++ {
		row := base
		row.Key = utils.RandCredentialAPIKey()
		if err := h.db.Create(&row).Error; err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			response.Fail(c, "创建失败", gin.H{"error": err.Error()})
			return
		}
		pub := credentialToPublic(&row)
		pub["key"] = row.Key
		pub["key_hint"] = "请立即保存：密钥仅本次返回，之后仅显示脱敏片段；若遗失请删除后重新创建。"
		response.Success(c, "创建成功", pub)
		return
	}
	response.FailWithCode(c, 500, "无法生成唯一密钥，请重试", nil)
}

func (h *Handlers) getCredential(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	id, ok := parseIntParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var row models.Credential
	if err := h.db.Where("id = ? AND user_id = ?", id, int(u.ID)).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "凭证不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", credentialToPublic(&row))
}

func (h *Handlers) updateCredential(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	id, ok := parseIntParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var body credentialUpdateBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	if body.Status != credentialStatusActive && body.Status != credentialStatusDisabled {
		response.FailWithCode(c, 400, "status 只能为 0（禁用）或 1（启用）", nil)
		return
	}
	var row models.Credential
	if err := h.db.Where("id = ? AND user_id = ?", id, int(u.ID)).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "凭证不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	exp := body.ExpiredTime
	if exp == 0 {
		exp = row.ExpiredTime
	}
	row.Name = strings.TrimSpace(body.Name)
	row.Status = body.Status
	row.RemainQuota = body.RemainQuota
	row.UnlimitedQuota = body.UnlimitedQuota
	row.AllowIps = allowIPsPtr(body.AllowIps)
	row.Group = strings.TrimSpace(body.Group)
	row.CrossGroupRetry = body.CrossGroupRetry
	row.ExpiredTime = exp
	if strings.EqualFold(strings.TrimSpace(row.Kind), models.CredentialKindLLM) {
		row.OpenAPIModelCatalogJSON = strings.TrimSpace(body.OpenAPIModelCatalogJSON)
	}

	if err := h.db.Save(&row).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "更新成功", credentialToPublic(&row))
}

// listLLMAvailableModelsForCredentialGroup GET /api/credentials/llm-available-models?group=
// 返回当前分组下可用于 OpenAPI 模型目录勾选的模型 id（与 /v1/models 无 catalog 时同源）。
func (h *Handlers) listLLMAvailableModelsForCredentialGroup(c *gin.Context) {
	if models.CurrentUser(c) == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	g := strings.TrimSpace(c.Query("group"))
	eff := g
	if eff == "" {
		eff = "default"
	}
	ids, err := CollectOpenAILLMModelIDsForGroup(h.db, g)
	if err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if ids == nil {
		ids = []string{}
	}
	response.Success(c, "ok", gin.H{"group": eff, "models": ids})
}

func (h *Handlers) deleteCredential(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	id, ok := parseIntParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	res := h.db.Where("id = ? AND user_id = ?", id, int(u.ID)).Delete(&models.Credential{})
	if res.Error != nil {
		response.Fail(c, "删除失败", gin.H{"error": res.Error.Error()})
		return
	}
	if res.RowsAffected == 0 {
		response.FailWithCode(c, 404, "凭证不存在", nil)
		return
	}
	response.Success(c, "已删除", gin.H{"id": id})
}
