// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SplitChannelModelList splits comma / newline separated model lists from channel config.
func SplitChannelModelList(s string) []string {
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

// ModelLimitSetFromCredential returns allowed model ids when credential.ModelLimitsEnabled; nil means no limit.
func ModelLimitSetFromCredential(cred *Credential) map[string]struct{} {
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
	for _, id := range SplitChannelModelList(raw) {
		m[id] = struct{}{}
	}
	return m
}

// ParseCredentialOpenAPIModelCatalog parses credential.OpenAPIModelCatalogJSON; supports [{"id":"x"}] or ["x","y"].
func ParseCredentialOpenAPIModelCatalog(jsonStr string) []string {
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

func collectOpenAPIModelIDsFromChannels(chs []LLMChannel) []string {
	seen := make(map[string]struct{})
	var order []string
	for i := range chs {
		for _, id := range SplitChannelModelList(chs[i].Models) {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			order = append(order, id)
		}
	}
	return order
}

// CollectOpenAPIAbilityModelsFromGroup derives model ids from llm_abilities × enabled channels; empty if none.
func CollectOpenAPIAbilityModelsFromGroup(db *gorm.DB, group, protocol string) ([]string, error) {
	if db == nil {
		return nil, nil
	}
	group = strings.TrimSpace(group)
	if group == "" {
		group = "default"
	}
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		protocol = LLMChannelProtocolOpenAI
	}
	var out []string
	err := db.Raw(
		"SELECT DISTINCT a.model FROM llm_abilities a "+
			"INNER JOIN llm_channels c ON c.id = a.channel_id AND c.status = 1 AND c.protocol = ? "+
			"WHERE a.enabled = 1 AND a.`group` = ? ORDER BY a.model",
		protocol, group,
	).Scan(&out).Error
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// CollectOpenAILLMModelIDsForGroup returns OpenAPI-visible model ids for a group (abilities first, else channel models).
func CollectOpenAILLMModelIDsForGroup(db *gorm.DB, group string) ([]string, error) {
	if db == nil {
		return nil, nil
	}
	g := strings.TrimSpace(group)
	if g == "" {
		g = "default"
	}
	var chs []LLMChannel
	q := db.Where("status = ? AND protocol = ? AND `group` = ?", 1, LLMChannelProtocolOpenAI, g).
		Order("(CASE WHEN priority IS NULL THEN 0 ELSE priority END) DESC").Order("id ASC")
	if err := q.Find(&chs).Error; err != nil {
		return nil, err
	}
	abilityIDs, err := CollectOpenAPIAbilityModelsFromGroup(db, g, LLMChannelProtocolOpenAI)
	if err != nil {
		return nil, err
	}
	if len(abilityIDs) > 0 {
		return abilityIDs, nil
	}
	return collectOpenAPIModelIDsFromChannels(chs), nil
}

// OpenAPIRelayModelItem is one row in GET /v1/models data[] (OpenAI-compatible shape).
type OpenAPIRelayModelItem struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// BuildOpenAPIModelListForCredential builds the model list for a credential (catalog or group channels, model_limits filter).
func BuildOpenAPIModelListForCredential(db *gorm.DB, cred *Credential) ([]OpenAPIRelayModelItem, error) {
	if cred == nil {
		return nil, nil
	}
	var ids []string
	if cat := ParseCredentialOpenAPIModelCatalog(cred.OpenAPIModelCatalogJSON); len(cat) > 0 {
		ids = cat
	} else {
		var err error
		ids, err = CollectOpenAILLMModelIDsForGroup(db, cred.Group)
		if err != nil {
			return nil, err
		}
	}
	lim := ModelLimitSetFromCredential(cred)
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
	out := make([]OpenAPIRelayModelItem, 0, len(ids))
	for _, id := range ids {
		out = append(out, OpenAPIRelayModelItem{
			ID:      id,
			Object:  "model",
			Created: created,
			OwnedBy: "lingvoice",
		})
	}
	return out, nil
}
