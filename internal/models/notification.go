// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/pkg/notification/mail"
	"gorm.io/gorm"
)

// EmailChannelFormView is returned to the frontend for editing (no SMTP password value).
type EmailChannelFormView struct {
	Driver             string `json:"driver"`
	SMTPHost           string `json:"smtpHost"`
	SMTPPort           int64  `json:"smtpPort"`
	SMTPUsername       string `json:"smtpUsername"`
	SMTPFrom           string `json:"smtpFrom"`
	FromDisplayName    string `json:"fromDisplayName"`
	SMTPPasswordSet    bool   `json:"smtpPasswordSet"`
	SendcloudAPIUser   string `json:"sendcloudApiUser"`
	SendcloudAPIKeySet bool   `json:"sendcloudApiKeySet"`
	SendcloudFrom      string `json:"sendcloudFrom"`
}

type NotificationChannelListResult struct {
	List      []NotificationChannel
	Total     int64
	Page      int
	PageSize  int
	TotalPage int
}

// 通知渠道类型
const (
	NotificationChannelTypeEmail = "email"
	NotificationChannelTypeSMS   = "sms"
)

// NotificationChannel 统一描述一种可配置通知出口
type NotificationChannel struct {
	BaseModel
	OrgID      uint   `json:"orgId" gorm:"uniqueIndex:idx_notify_org_code;not null;default:0;comment:tenant organization id"`
	Type       string `json:"type" gorm:"size:32;not null;index:idx_notify_ch_type_sort,priority:1;comment:渠道类型"`
	Code       string `json:"code,omitempty" gorm:"size:64;uniqueIndex:idx_notify_org_code;comment:渠道编码"`
	Name       string `json:"name" gorm:"size:128;not null;comment:显示名称"`
	SortOrder  int    `json:"sortOrder" gorm:"not null;default:0;index:idx_notify_ch_type_sort,priority:2;comment:排序权重"`
	Enabled    bool   `json:"enabled" gorm:"not null;default:true;index;comment:是否启用"`
	ConfigJSON string `json:"configJson,omitempty" gorm:"type:text;comment:渠道配置 JSON"`
}

// TableName GORM 表名
func (NotificationChannel) TableName() string {
	return "notification_channels"
}

// ListNotificationChannels returns paginated notification channels, optionally filtered by type.
func ListNotificationChannels(db *gorm.DB, channelType string, page, pageSize int) (*NotificationChannelListResult, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	q := db.Model(&NotificationChannel{})
	if t := strings.TrimSpace(channelType); t != "" {
		q = q.Where("type = ?", t)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	var list []NotificationChannel
	listQ := db.Model(&NotificationChannel{})
	if t := strings.TrimSpace(channelType); t != "" {
		listQ = listQ.Where("type = ?", t)
	}
	if err := listQ.Order("type ASC, sort_order ASC, id ASC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, err
	}
	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	return &NotificationChannelListResult{
		List:      list,
		Total:     total,
		Page:      page,
		PageSize:  pageSize,
		TotalPage: totalPage,
	}, nil
}

// GetNotificationChannel returns a notification channel by primary key.
func GetNotificationChannel(db *gorm.DB, id uint) (*NotificationChannel, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	var row NotificationChannel
	if err := db.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// BuildEmailChannelConfigJSON builds MailConfig-compatible JSON for notification_channels.config_json.
func BuildEmailChannelConfigJSON(driver string, name string, smtpHost string, smtpPort int64, smtpUser, smtpPassword, smtpFrom, fromDisplayName string, scUser, scKey, scFrom string) (string, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	cfg := mail.MailConfig{Name: strings.TrimSpace(name), FromName: strings.TrimSpace(fromDisplayName)}
	switch driver {
	case mail.ProviderSMTP:
		if strings.TrimSpace(smtpHost) == "" || smtpPort <= 0 || strings.TrimSpace(smtpFrom) == "" {
			return "", errors.New("SMTP 需要 host、port、发件地址")
		}
		cfg.Provider = mail.ProviderSMTP
		cfg.Host = strings.TrimSpace(smtpHost)
		cfg.Port = smtpPort
		cfg.Username = strings.TrimSpace(smtpUser)
		cfg.Password = smtpPassword
		cfg.From = strings.TrimSpace(smtpFrom)
	case mail.ProviderSendCloud:
		if strings.TrimSpace(scUser) == "" || strings.TrimSpace(scKey) == "" || strings.TrimSpace(scFrom) == "" {
			return "", errors.New("SendCloud 需要 api_user、api_key、发件地址")
		}
		cfg.Provider = mail.ProviderSendCloud
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

// DecodeEmailChannelForm parses config_json into a flat form view (passwords not echoed).
func DecodeEmailChannelForm(configJSON string) (*EmailChannelFormView, error) {
	var cfg mail.MailConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, err
	}
	v := &EmailChannelFormView{}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case mail.ProviderSendCloud:
		v.Driver = mail.ProviderSendCloud
		v.SendcloudAPIUser = cfg.APIUser
		v.SendcloudFrom = cfg.From
		v.SendcloudAPIKeySet = cfg.APIKey != ""
		v.FromDisplayName = cfg.FromName
	case mail.ProviderSMTP, "":
		v.Driver = mail.ProviderSMTP
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
	var oldC, newC mail.MailConfig
	if err := json.Unmarshal([]byte(oldJSON), &oldC); err != nil {
		return newJSON, err
	}
	if err := json.Unmarshal([]byte(newJSON), &newC); err != nil {
		return newJSON, err
	}
	if strings.ToLower(newC.Provider) == mail.ProviderSMTP && newC.Password == "" && oldC.Password != "" {
		newC.Password = oldC.Password
	}
	if strings.ToLower(newC.Provider) == mail.ProviderSendCloud && newC.APIKey == "" && oldC.APIKey != "" {
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
