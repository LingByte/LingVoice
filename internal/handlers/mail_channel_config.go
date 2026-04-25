// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/pkg/notification"
)

const emailChannelCodePrefix = "EM"

// randomChannelCode returns a unique-looking code segment (caller adds prefix / uniqueness check).
func randomChannelCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b)[:12])
}

// BuildEmailChannelConfigJSON builds MailConfig-compatible JSON for notification_channels.config_json.
func BuildEmailChannelConfigJSON(driver string, name string, smtpHost string, smtpPort int64, smtpUser, smtpPassword, smtpFrom, fromDisplayName string, scUser, scKey, scFrom string) (string, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	cfg := notification.MailConfig{Name: strings.TrimSpace(name), FromName: strings.TrimSpace(fromDisplayName)}
	switch driver {
	case "smtp":
		if strings.TrimSpace(smtpHost) == "" || smtpPort <= 0 || strings.TrimSpace(smtpFrom) == "" {
			return "", errors.New("SMTP 需要 host、port、发件地址")
		}
		cfg.Provider = "smtp"
		cfg.Host = strings.TrimSpace(smtpHost)
		cfg.Port = smtpPort
		cfg.Username = strings.TrimSpace(smtpUser)
		cfg.Password = smtpPassword
		cfg.From = strings.TrimSpace(smtpFrom)
	case "sendcloud":
		if strings.TrimSpace(scUser) == "" || strings.TrimSpace(scKey) == "" || strings.TrimSpace(scFrom) == "" {
			return "", errors.New("SendCloud 需要 api_user、api_key、发件地址")
		}
		cfg.Provider = "sendcloud"
		cfg.APIUser = strings.TrimSpace(scUser)
		cfg.APIKey = strings.TrimSpace(scKey)
		cfg.From = strings.TrimSpace(scFrom)
	default:
		return "", fmt.Errorf("不支持的邮件驱动: %s", driver)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// EmailChannelFormView is returned to the frontend for editing (no SMTP password value).
type EmailChannelFormView struct {
	Driver string `json:"driver"`

	SMTPHost          string `json:"smtpHost"`
	SMTPPort          int64  `json:"smtpPort"`
	SMTPUsername      string `json:"smtpUsername"`
	SMTPFrom          string `json:"smtpFrom"`
	FromDisplayName   string `json:"fromDisplayName"`
	SMTPPasswordSet   bool   `json:"smtpPasswordSet"`
	SendcloudAPIUser  string `json:"sendcloudApiUser"`
	SendcloudAPIKeySet bool   `json:"sendcloudApiKeySet"`
	SendcloudFrom     string `json:"sendcloudFrom"`
}

// DecodeEmailChannelForm parses config_json into a flat form view (passwords not echoed).
func DecodeEmailChannelForm(configJSON string) (*EmailChannelFormView, error) {
	var cfg notification.MailConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, err
	}
	v := &EmailChannelFormView{}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "sendcloud":
		v.Driver = "sendcloud"
		v.SendcloudAPIUser = cfg.APIUser
		v.SendcloudFrom = cfg.From
		v.SendcloudAPIKeySet = cfg.APIKey != ""
		v.FromDisplayName = cfg.FromName
	case "smtp", "":
		v.Driver = "smtp"
		v.SMTPHost = cfg.Host
		v.SMTPPort = cfg.Port
		v.SMTPUsername = cfg.Username
		v.SMTPFrom = cfg.From
		v.SMTPPasswordSet = cfg.Password != ""
		v.FromDisplayName = cfg.FromName
	default:
		v.Driver = strings.ToLower(strings.TrimSpace(cfg.Provider))
	}
	return v, nil
}

// MergeEmailSecretsOnUpdate keeps SMTP password / SendCloud API key when client sends empty on update.
func MergeEmailSecretsOnUpdate(oldJSON, newJSON string) (string, error) {
	var oldC, newC notification.MailConfig
	if err := json.Unmarshal([]byte(oldJSON), &oldC); err != nil {
		return newJSON, err
	}
	if err := json.Unmarshal([]byte(newJSON), &newC); err != nil {
		return newJSON, err
	}
	if strings.ToLower(newC.Provider) == "smtp" && newC.Password == "" && oldC.Password != "" {
		newC.Password = oldC.Password
	}
	if strings.ToLower(newC.Provider) == "sendcloud" && newC.APIKey == "" && oldC.APIKey != "" {
		newC.APIKey = oldC.APIKey
	}
	if strings.TrimSpace(newC.FromName) == "" && strings.TrimSpace(oldC.FromName) != "" {
		newC.FromName = oldC.FromName
	}
	out, err := json.Marshal(newC)
	if err != nil {
		return newJSON, err
	}
	return string(out), nil
}
