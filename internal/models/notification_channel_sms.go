package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SMSChannelFormView is returned to the frontend for editing (secrets not echoed).
type SMSChannelFormView struct {
	Provider   string         `json:"provider"`
	Config     map[string]any `json:"config"`
	SecretKeys []string       `json:"secretKeys,omitempty"` // which fields are secrets
}

type smsChannelConfigEnvelope struct {
	Provider string         `json:"provider"`
	Config   map[string]any `json:"config"`
}

func BuildSMSChannelConfigJSON(provider string, cfg any) (string, error) {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" {
		return "", errors.New("sms provider 不能为空")
	}
	// cfg may be object or nil; normalize to map.
	var m map[string]any
	switch v := cfg.(type) {
	case map[string]any:
		m = v
	default:
		// attempt marshal/unmarshal
		if cfg == nil {
			m = map[string]any{}
		} else {
			b, err := json.Marshal(cfg)
			if err != nil {
				return "", err
			}
			_ = json.Unmarshal(b, &m)
		}
	}
	env := smsChannelConfigEnvelope{Provider: p, Config: m}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	// Minimal validation: must contain at least one key.
	if len(env.Config) == 0 {
		return "", fmt.Errorf("sms provider=%s 缺少配置", p)
	}
	return string(raw), nil
}

func DecodeSMSChannelForm(configJSON string) (*SMSChannelFormView, error) {
	var env smsChannelConfigEnvelope
	if err := json.Unmarshal([]byte(configJSON), &env); err != nil {
		return nil, err
	}
	out := &SMSChannelFormView{
		Provider: strings.ToLower(strings.TrimSpace(env.Provider)),
		Config:   env.Config,
	}
	// Mark known secrets (frontend will show "已设置" instead of value).
	switch out.Provider {
	case "yunpian", "luosimao", "juhe":
		out.SecretKeys = []string{"apiKey", "appKey"}
	case "twilio":
		out.SecretKeys = []string{"token"}
	case "huyi":
		out.SecretKeys = []string{"apiKey"}
	case "submail":
		out.SecretKeys = []string{"appKey"}
	case "chuanglan":
		out.SecretKeys = []string{"password"}
	}
	// Strip secret values by best-effort: replace with empty string.
	for _, k := range out.SecretKeys {
		if _, ok := out.Config[k]; ok {
			out.Config[k] = ""
		}
	}
	return out, nil
}

// MergeSMSSecretsOnUpdate keeps secret fields when client sends empty string on update.
func MergeSMSSecretsOnUpdate(oldJSON, newJSON string) (string, error) {
	var oldE, newE smsChannelConfigEnvelope
	if err := json.Unmarshal([]byte(oldJSON), &oldE); err != nil {
		return newJSON, err
	}
	if err := json.Unmarshal([]byte(newJSON), &newE); err != nil {
		return newJSON, err
	}
	if strings.ToLower(strings.TrimSpace(oldE.Provider)) != strings.ToLower(strings.TrimSpace(newE.Provider)) {
		return newJSON, nil
	}
	if newE.Config == nil {
		newE.Config = map[string]any{}
	}
	// heuristic: any key present in old with non-empty string, but new empty string => keep old
	for k, ov := range oldE.Config {
		os, ok := ov.(string)
		if !ok || strings.TrimSpace(os) == "" {
			continue
		}
		if nv, ok := newE.Config[k]; ok {
			if ns, ok := nv.(string); ok && strings.TrimSpace(ns) == "" {
				newE.Config[k] = os
			}
		}
	}
	out, err := json.Marshal(newE)
	if err != nil {
		return newJSON, err
	}
	return string(out), nil
}

