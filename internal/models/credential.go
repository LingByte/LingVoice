package models

import (
	"errors"
	"strings"

	"github.com/LingByte/LingVoice/pkg/constants"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"gorm.io/gorm"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

// ErrCredentialUniqueKeyExhausted 多次尝试仍因唯一键冲突无法插入
var ErrCredentialUniqueKeyExhausted = errors.New("credential: unique key retries exhausted")

// 凭证服务类型：同一套「密钥 + 配额 + IP 限制」承载多种上游能力。
const (
	CredentialKindLLM   = "llm"   // 大模型 API Key；ModelLimits* 字段主要对此类生效
	CredentialKindASR   = "asr"   // 语音识别
	CredentialKindTTS   = "tts"   // 语音合成
	CredentialKindEmail = "email" // 邮件类第三方 API Key 等
)

// OpenAPI 额度类错误细分（写入 error.code 或业务 data.reason，便于前端区分凭证与用户两层配额）。
const (
	OpenAPIQuotaReasonCredentialExhausted = "credential_quota_exhausted"
	OpenAPIQuotaReasonUserExhausted       = "user_quota_exhausted"
	OpenAPIQuotaReasonUserNotFound        = "user_account_not_found"
)

// Credential 用户侧可调用的访问凭证
type Credential struct {
	Id                      int            `json:"id"`
	UserId                  int            `json:"user_id" gorm:"index:idx_credential_user_kind"`
	Kind                    string         `json:"kind" gorm:"size:16;index:idx_credential_user_kind;default:llm"`
	Key                     string         `json:"key" gorm:"type:char(48);uniqueIndex"`
	Status                  int            `json:"status" gorm:"default:1"` // 1 启用 0 禁用
	Name                    string         `json:"name" gorm:"index"`
	ExtraJSON               string         `json:"extra,omitempty" gorm:"column:extra_json;type:text"`
	CreatedTime             int64          `json:"created_time" gorm:"bigint"`
	AccessedTime            int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime             int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 永不过期
	RemainQuota             int            `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota          bool           `json:"unlimited_quota"`
	UsedQuota               int            `json:"used_quota" gorm:"default:0"`
	ModelLimitsEnabled      bool           `json:"model_limits_enabled"`
	ModelLimits             string         `json:"model_limits" gorm:"type:text"`
	AllowIps                *string        `json:"allow_ips" gorm:"default:''"`
	Group                   string         `json:"group" gorm:"default:''"`
	CrossGroupRetry         bool           `json:"cross_group_retry"`
	OpenAPIModelCatalogJSON string         `json:"openapi_model_catalog,omitempty" gorm:"column:openapi_model_catalog_json;type:text"`
	DeletedAt               gorm.DeletedAt `gorm:"index"`
}

// TableName 与历史常量一致。
func (Credential) TableName() string {
	return constants.USER_CREDENTIAL_TABLE_NAME
}

// CredentialHasRemainingQuota 凭证是否仍可按次/按量扣减（无限或 remain>0）。
func CredentialHasRemainingQuota(c *Credential) bool {
	if c == nil {
		return false
	}
	return c.UnlimitedQuota || c.RemainQuota > 0
}

// CredentialExpired 是否已过 expired_time（-1 或 0 表示不按时间过期）。
func CredentialExpired(c *Credential, nowUnix int64) bool {
	if c == nil {
		return true
	}
	if c.ExpiredTime == -1 || c.ExpiredTime <= 0 {
		return false
	}
	return c.ExpiredTime < nowUnix
}

// ValidateCredentialKind 返回空串表示合法，否则为中文说明。
func ValidateCredentialKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	switch k {
	case CredentialKindLLM, CredentialKindASR, CredentialKindTTS, CredentialKindEmail:
		return ""
	default:
		return "不支持的凭证类型 kind，可选：llm、asr、tts、email"
	}
}

// ListCredentialsForUser 列出某用户的凭证，可按 kind、非空 group 过滤（kind 须已由 ValidateCredentialKind 校验）。
func ListCredentialsForUser(db *gorm.DB, userID int, kindFilter, groupFilter string) ([]Credential, error) {
	q := db.Model(&Credential{}).Where("user_id = ?", userID)
	if k := strings.ToLower(strings.TrimSpace(kindFilter)); k != "" {
		q = q.Where("kind = ?", k)
	}
	if g := strings.TrimSpace(groupFilter); g != "" {
		q = q.Where("`group` = ?", g)
	}
	var rows []Credential
	err := q.Order("id DESC").Find(&rows).Error
	return rows, err
}

// ListDistinctCredentialGroups 当前用户已使用过的非空分组名。
func ListDistinctCredentialGroups(db *gorm.DB, userID int) ([]string, error) {
	var groups []string
	err := db.Model(&Credential{}).
		Distinct("`group`").
		Where("user_id = ? AND TRIM(COALESCE(`group`,'')) != ''", userID).
		Order("`group` ASC").
		Pluck("`group`", &groups).Error
	return groups, err
}

// GetCredentialOwnedByUser 按 id + user_id 读取一行。
func GetCredentialOwnedByUser(db *gorm.DB, id, userID int) (*Credential, error) {
	var row Credential
	err := db.Where("id = ? AND user_id = ?", id, userID).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateCredential 插入凭证（调用方需已填好 Key 等字段）。
func CreateCredential(db *gorm.DB, row *Credential) error {
	if row == nil {
		return errors.New("nil credential")
	}
	return db.Create(row).Error
}

// TryCreateCredentialWithUniqueKey 在唯一键冲突时重试生成 key，最多 maxAttempts 次。
func TryCreateCredentialWithUniqueKey(db *gorm.DB, bases Credential, maxAttempts int) (*Credential, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for i := 0; i < maxAttempts; i++ {
		row := bases
		row.Key = base.RandCredentialAPIKey()
		if err := db.Create(&row).Error; err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return nil, err
		}
		return &row, nil
	}
	return nil, ErrCredentialUniqueKeyExhausted
}

// SaveCredential 保存对已有行的修改（GORM Save）。
func SaveCredential(db *gorm.DB, row *Credential) error {
	if row == nil {
		return errors.New("nil credential")
	}
	return db.Save(row).Error
}

// DeleteCredentialOwnedByUser 软删除；返回影响行数。
func DeleteCredentialOwnedByUser(db *gorm.DB, id, userID int) (int64, error) {
	res := db.Where("id = ? AND user_id = ?", id, userID).Delete(&Credential{})
	return res.RowsAffected, res.Error
}

// BumpCredentialUsedAndDecrementRemain 成功调用后增加 used_quota；非无限时仅在 remain_quota>0 时减 remain。
func BumpCredentialUsedAndDecrementRemain(db *gorm.DB, credID int, unlimited bool, delta int) error {
	if delta < 1 {
		delta = 1
	}
	if unlimited {
		return db.Model(&Credential{}).Where("id = ?", credID).
			Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error
	}
	if err := db.Model(&Credential{}).Where("id = ? AND remain_quota > ?", credID, 0).
		Update("remain_quota", gorm.Expr("remain_quota - ?", delta)).Error; err != nil {
		return err
	}
	return db.Model(&Credential{}).Where("id = ?", credID).
		Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error
}

// TouchCredentialAccessedTime 更新 accessed_time。
func TouchCredentialAccessedTime(db *gorm.DB, credID int, nowUnix int64) error {
	return db.Model(&Credential{}).Where("id = ?", credID).Update("accessed_time", nowUnix).Error
}

func MaskTokenKey(key string) string {
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, "sk-") && len(key) > 12 {
		return "sk-" + strings.Repeat("*", min(24, len(key)-7)) + key[len(key)-4:]
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	if len(key) <= 8 {
		return key[:2] + "****" + key[len(key)-2:]
	}
	return key[:4] + "**********" + key[len(key)-4:]
}
