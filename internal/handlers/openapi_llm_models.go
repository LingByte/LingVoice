// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func splitChannelModelList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	repl := strings.NewReplacer("\n", ",", "\r", "", ";", ",", "，", ",")
	s = repl.Replace(s)
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func modelLimitSet(cred *models.Credential) map[string]struct{} {
	if cred == nil || !cred.ModelLimitsEnabled {
		return nil
	}
	raw := strings.TrimSpace(cred.ModelLimits)
	if raw == "" {
		return map[string]struct{}{}
	}
	var arr []string
	if json.Unmarshal([]byte(raw), &arr) == nil {
		m := make(map[string]struct{}, len(arr))
		for _, id := range arr {
			id = strings.TrimSpace(id)
			if id != "" {
				m[id] = struct{}{}
			}
		}
		return m
	}
	m := make(map[string]struct{})
	for _, id := range splitChannelModelList(raw) {
		m[id] = struct{}{}
	}
	return m
}

// parseCredentialOpenAPIModelCatalog 解析凭证 openapi_model_catalog_json；支持 [{"id":"x"}] 或 ["x","y"]。
func parseCredentialOpenAPIModelCatalog(jsonStr string) []string {
	jsonStr = strings.TrimSpace(jsonStr)
	if jsonStr == "" {
		return nil
	}
	var asStrings []string
	if json.Unmarshal([]byte(jsonStr), &asStrings) == nil && len(asStrings) > 0 {
		var out []string
		for _, s := range asStrings {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	var objs []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(jsonStr), &objs) == nil {
		var out []string
		for _, o := range objs {
			id := strings.TrimSpace(o.ID)
			if id != "" {
				out = append(out, id)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func collectOpenAPIModelIDsFromChannels(chs []models.LLMChannel) []string {
	seen := make(map[string]struct{})
	var order []string
	for i := range chs {
		for _, id := range splitChannelModelList(chs[i].Models) {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			order = append(order, id)
		}
	}
	return order
}

func buildOpenAPIModelListForCredential(db *gorm.DB, cred *models.Credential) ([]gin.H, error) {
	if cred == nil {
		return nil, nil
	}
	var ids []string
	if cat := parseCredentialOpenAPIModelCatalog(cred.OpenAPIModelCatalogJSON); len(cat) > 0 {
		ids = cat
	} else {
		var chs []models.LLMChannel
		g := strings.TrimSpace(cred.Group)
		if g == "" {
			g = "default"
		}
		q := db.Where("status = ? AND protocol = ? AND `group` = ?", 1, models.LLMChannelProtocolOpenAI, g).
			Order("(CASE WHEN priority IS NULL THEN 0 ELSE priority END) DESC").Order("id ASC")
		if err := q.Find(&chs).Error; err != nil {
			return nil, err
		}
		ids = collectOpenAPIModelIDsFromChannels(chs)
	}
	lim := modelLimitSet(cred)
	if lim != nil {
		var filtered []string
		for _, id := range ids {
			if _, ok := lim[id]; ok {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	created := int(time.Now().Unix())
	out := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		out = append(out, gin.H{
			"id":       id,
			"object":   "model",
			"created":  created,
			"owned_by": "lingvoice",
		})
	}
	return out, nil
}

// openAPIListModels GET /v1/models：返回本密钥可用的模型列表（凭证 catalog 或 group 渠道汇总，并受 model_limits 过滤）。
func (h *Handlers) openAPIListModels(c *gin.Context) {
	cred, ok := middleware.OpenAPILLMCredentialFromContext(c)
	if !ok || cred == nil {
		return
	}
	list, err := buildOpenAPIModelListForCredential(h.db, cred)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": err.Error(),
				"type":    "api_error",
				"code":    "internal_error",
			},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   list,
	})
}
