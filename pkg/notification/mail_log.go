// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package notification

import (
	"time"

	"github.com/LingByte/LingVoice/pkg/utils"
	"gorm.io/gorm"
)

func nextMailLogSnowflakeID() int64 {
	if utils.SnowflakeUtil != nil {
		if id := utils.SnowflakeUtil.NextID(); id > 0 {
			return id
		}
	}
	return time.Now().UnixNano()
}

// NextMailLogSnowflakeID allocates a primary key for mail_logs (system sends or admin API).
func NextMailLogSnowflakeID() int64 {
	return nextMailLogSnowflakeID()
}

// MailLog is a persisted record of an outbound email (optional when DB is wired).
type MailLog struct {
	ID int64 `gorm:"primaryKey;autoIncrement:false" json:"id,string"`
	UserID      uint   `gorm:"index" json:"user_id"`
	Provider    string `gorm:"size:32;index" json:"provider"` // smtp | sendcloud | multi
	ChannelName string `gorm:"size:128;index" json:"channel_name"` // MailConfig.Name when set
	ToEmail     string `gorm:"index" json:"to_email"`
	Subject     string `json:"subject"`
	Status      string `gorm:"index" json:"status"`
	HtmlBody    string `gorm:"type:longtext" json:"html_body"` // 邮件 HTML，管理端可预览
	ErrorMsg    string `gorm:"type:text" json:"error_msg"`
	MessageID   string `gorm:"type:varchar(255);index" json:"message_id"`
	IPAddress   string `gorm:"size:64" json:"ip_address"`
	RetryCount  int    `json:"retry_count"`
	SentAt      time.Time `json:"sent_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TableName returns the GORM table name.
func (MailLog) TableName() string {
	return "mail_logs"
}

// CreateMailLog records a successful send (or send accepted by provider).
func CreateMailLog(db *gorm.DB, userID uint, provider, channelName, toEmail, subject, htmlBody, messageID, status string, ip string) (*MailLog, error) {
	log := &MailLog{
		ID:          nextMailLogSnowflakeID(),
		UserID:      userID,
		Provider:    provider,
		ChannelName: channelName,
		ToEmail:     toEmail,
		Subject:     subject,
		HtmlBody:    htmlBody,
		Status:      status,
		MessageID:   messageID,
		IPAddress:   ip,
		SentAt:      time.Now(),
	}
	if err := db.Create(log).Error; err != nil {
		return nil, err
	}
	return log, nil
}

// CreateFailedMailLog records a send that failed after all retries.
func CreateFailedMailLog(db *gorm.DB, userID uint, provider, channelName, toEmail, subject, htmlBody, errMsg string, retries int, ip string) (*MailLog, error) {
	log := &MailLog{
		ID:          nextMailLogSnowflakeID(),
		UserID:      userID,
		Provider:    provider,
		ChannelName: channelName,
		ToEmail:     toEmail,
		Subject:     subject,
		HtmlBody:    htmlBody,
		Status:      StatusFailed,
		ErrorMsg:    errMsg,
		RetryCount:  retries,
		IPAddress:   ip,
		SentAt:      time.Time{},
	}
	if err := db.Create(log).Error; err != nil {
		return nil, err
	}
	return log, nil
}

// UpdateMailLogStatusByMessageID updates status for SendCloud (and any provider keyed by message id).
// Only rows with matching provider are updated when provider is non-empty.
func UpdateMailLogStatusByMessageID(db *gorm.DB, messageID, provider, status, errorMsg string) error {
	if messageID == "" {
		return nil
	}
	q := db.Model(&MailLog{}).Where("message_id = ?", messageID)
	if provider != "" {
		q = q.Where("provider = ?", provider)
	}
	return q.Updates(map[string]interface{}{
		"status":    status,
		"error_msg": errorMsg,
	}).Error
}

// GetMailLogByMessageID returns a log by provider message id.
func GetMailLogByMessageID(db *gorm.DB, messageID string) (*MailLog, error) {
	var log MailLog
	if err := db.Where("message_id = ?", messageID).First(&log).Error; err != nil {
		return nil, err
	}
	return &log, nil
}

// GetMailLogs returns paginated logs for a user.
func GetMailLogs(db *gorm.DB, userID uint, page, pageSize int) ([]MailLog, int64, error) {
	var logs []MailLog
	var total int64
	base := db.Model(&MailLog{}).Where("user_id = ?", userID)
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	if err := db.Where("user_id = ?", userID).Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&logs).Error; err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

// GetMailLogStats aggregates counts by status for a user.
func GetMailLogStats(db *gorm.DB, userID uint) (map[string]int64, error) {
	type row struct {
		Status string
		Cnt    int64
	}
	var rows []row
	if err := db.Model(&MailLog{}).Select("status, count(*) as cnt").Where("user_id = ?", userID).Group("status").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]int64{
		"total": 0,
	}
	for _, r := range rows {
		out[r.Status] = r.Cnt
		out["total"] += r.Cnt
	}
	return out, nil
}
