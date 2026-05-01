// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// EffectiveCredentialLLMGroup normalizes empty credential group to "default" for channel lookup.
func EffectiveCredentialLLMGroup(cred *Credential) string {
	if cred == nil {
		return "default"
	}
	g := strings.TrimSpace(cred.Group)
	if g == "" {
		return "default"
	}
	return g
}

// ListLLMChannelsOrdered returns enabled LLM channels for group+protocol with non-empty keys.
func ListLLMChannelsOrdered(db *gorm.DB, group, protocol string) ([]LLMChannel, error) {
	g := strings.TrimSpace(group)
	if g == "" {
		g = "default"
	}
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = LLMChannelProtocolOpenAI
	}
	var list []LLMChannel
	q := db.Where("status = ? AND protocol = ? AND `group` = ?", 1, proto, g).
		Order("(CASE WHEN priority IS NULL THEN 0 ELSE priority END) DESC").Order("id ASC")
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	var out []LLMChannel
	for i := range list {
		if strings.TrimSpace(list[i].Key) != "" {
			out = append(out, list[i])
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no_llm_channel")
	}
	return out, nil
}

// ListLLMChannelsForRelay picks channels by ability table when model is set and enabled; else full group list.
func ListLLMChannelsForRelay(db *gorm.DB, cred *Credential, protocol, model string) ([]LLMChannel, error) {
	model = strings.TrimSpace(model)
	g := EffectiveCredentialLLMGroup(cred)
	proto := strings.ToLower(strings.TrimSpace(protocol))
	if proto == "" {
		proto = LLMChannelProtocolOpenAI
	}
	if model == "" {
		return ListLLMChannelsOrdered(db, cred.Group, proto)
	}
	var cnt int64
	if err := db.Model(&LLMAbility{}).
		Where("`group` = ? AND model = ? AND enabled = ?", g, model, true).
		Count(&cnt).Error; err != nil {
		return nil, err
	}
	if cnt == 0 {
		return ListLLMChannelsOrdered(db, cred.Group, proto)
	}
	var chs []LLMChannel
	q := db.Model(&LLMChannel{}).
		Joins("INNER JOIN llm_abilities ON llm_abilities.channel_id = llm_channels.id AND llm_abilities.`group` = ? AND llm_abilities.model = ? AND llm_abilities.enabled = ?", g, model, true).
		Where("llm_channels.status = ? AND llm_channels.protocol = ?", 1, proto).
		Order("llm_abilities.priority DESC, llm_abilities.weight DESC, llm_channels.id ASC")
	if err := q.Find(&chs).Error; err != nil {
		return nil, err
	}
	var out []LLMChannel
	for i := range chs {
		if strings.TrimSpace(chs[i].Key) != "" {
			out = append(out, chs[i])
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no_llm_channel")
	}
	return out, nil
}

// LLMChannelBaseURLString returns trimmed base URL or empty.
func LLMChannelBaseURLString(ch *LLMChannel) string {
	if ch == nil || ch.BaseURL == nil {
		return ""
	}
	return strings.TrimSpace(*ch.BaseURL)
}

// OpenAINoLLMChannelPayload is a 503-style OpenAI error body when no OpenAI-protocol channel exists.
func OpenAINoLLMChannelPayload(cred *Credential) map[string]any {
	g := EffectiveCredentialLLMGroup(cred)
	return map[string]any{
		"error": map[string]any{
			"message": "No active OpenAI-protocol LLM channel for credential group",
			"type":    "api_error",
			"code":    "model_not_found",
			"param":   g,
		},
		"credential_group": g,
	}
}

// ExtractJSONModelField reads model from a JSON chat/messages body.
func ExtractJSONModelField(body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return ""
	}
	return strings.TrimSpace(v.Model)
}
