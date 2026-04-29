// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/notification/mail"
	"gorm.io/gorm"
)

// 通知渠道类型
const (
	NotificationChannelTypeEmail = "email"
	NotificationChannelTypeSMS   = "sms"
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

// InternalNotificationListResult is paginated internal (in-app) notifications.
type InternalNotificationListResult struct {
	List      []InternalNotification
	Total     int64
	Page      int
	PageSize  int
	TotalPage int
}

// InternalNotification 站内通知
type InternalNotification struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	CreatedAt time.Time      `json:"createdAt" gorm:"autoCreateTime;comment:Creation time"`
	UpdatedAt time.Time      `json:"updatedAt,omitempty" gorm:"autoUpdateTime;comment:Update time"`
	DeletedAt gorm.DeletedAt `json:"deletedAt,omitempty" gorm:"index"`
	CreateBy  string         `json:"createBy,omitempty" gorm:"size:128;comment:Creator"`
	UpdateBy  string         `json:"updateBy,omitempty" gorm:"size:128;comment:Updater"`
	Remark    string         `json:"remark,omitempty" gorm:"size:128;comment:Remark"`
	UserID    uint           `json:"userId" gorm:"index;not null;comment:接收用户 ID"`
	Title     string         `json:"title" gorm:"size:255;not null;comment:标题"`
	Content   string         `json:"content" gorm:"type:text;not null;comment:正文"`
	Read      bool           `json:"read" gorm:"default:false;index;comment:是否已读"`
}

// TableName GORM 表名
func (InternalNotification) TableName() string {
	return "internal_notifications"
}

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

// SetCreateInfo 设置创建人（与 BaseModel 行为一致）
func (m *InternalNotification) SetCreateInfo(operator string) {
	m.CreateBy = operator
	m.UpdateBy = operator
}

// SetUpdateInfo 设置更新人
func (m *InternalNotification) SetUpdateInfo(operator string) {
	m.UpdateBy = operator
}

// IsSoftDeleted 是否已软删除（与 BaseModel 判定方式一致）
func (m *InternalNotification) IsSoftDeleted() bool {
	return !m.DeletedAt.Time.IsZero()
}

// ListInternalNotifications lists notifications visible to actor (admin may filter by user id).
func ListInternalNotifications(db *gorm.DB, actor *User, filterUserID *uint, page, pageSize int) (*InternalNotificationListResult, error) {
	if actor == nil {
		return nil, errors.New("models: nil actor")
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	scope := func(q *gorm.DB) *gorm.DB {
		q = q.Model(&InternalNotification{})
		if actor.IsAdmin() {
			if filterUserID != nil {
				q = q.Where("user_id = ?", *filterUserID)
			}
			return q
		}
		return q.Where("user_id = ?", actor.ID)
	}

	var total int64
	if err := scope(db).Count(&total).Error; err != nil {
		return nil, err
	}
	offset := (page - 1) * pageSize
	var list []InternalNotification
	if err := scope(db).Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, err
	}
	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	return &InternalNotificationListResult{
		List:      list,
		Total:     total,
		Page:      page,
		PageSize:  pageSize,
		TotalPage: totalPage,
	}, nil
}

// GetInternalNotificationByID loads a single row by primary key.
func GetInternalNotificationByID(db *gorm.DB, id uint) (*InternalNotification, error) {
	var row InternalNotification
	if err := db.First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateInternalNotification persists a new row.
func CreateInternalNotification(db *gorm.DB, row *InternalNotification) error {
	if row == nil {
		return errors.New("models: nil internal notification")
	}
	return db.Create(row).Error
}

// SaveInternalNotification persists updates to an existing row.
func SaveInternalNotification(db *gorm.DB, row *InternalNotification) error {
	return db.Save(row).Error
}

// DeleteInternalNotification hard-deletes the row.
func DeleteInternalNotification(db *gorm.DB, row *InternalNotification) error {
	return db.Delete(row).Error
}

// PatchInternalNotificationRead updates read flag and update_by.
func PatchInternalNotificationRead(db *gorm.DB, id uint, read bool, updateBy string) error {
	return db.Model(&InternalNotification{}).Where("id = ?", id).Updates(map[string]interface{}{
		"read":      read,
		"update_by": updateBy,
	}).Error
}
