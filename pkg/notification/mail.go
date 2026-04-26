// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package notification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/LingByte/LingVoice/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// providerSlot is one send channel (SMTP or SendCloud) with an optional label.
type providerSlot struct {
	label    string
	provider MailProvider
}

// Mailer sends HTML mail over one or more channels: each send round-robins the starting channel,
// then fails over to the rest until one succeeds or all are exhausted. Per-channel retries use RetryPolicy.
type Mailer struct {
	channels  []providerSlot
	retry     RetryPolicy
	rrCounter uint32

	db        *gorm.DB
	userID    uint
	ipAddress string
}

// channelLabel returns MailConfig.Name or a short derived label for logs.
func channelLabel(cfg MailConfig) string {
	if strings.TrimSpace(cfg.Name) != "" {
		return strings.TrimSpace(cfg.Name)
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "sendcloud":
		if cfg.APIUser != "" {
			return "sendcloud:" + cfg.APIUser
		}
		return "sendcloud"
	default:
		if cfg.Host != "" {
			return fmt.Sprintf("smtp:%s:%d", cfg.Host, cfg.Port)
		}
		return "smtp"
	}
}

func buildSlots(cfgs []MailConfig) ([]providerSlot, error) {
	if len(cfgs) == 0 {
		return nil, errors.New("notification: at least one mail channel is required")
	}
	slots := make([]providerSlot, 0, len(cfgs))
	for i, cfg := range cfgs {
		p, err := NewProviderFromConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("notification: channel %d (%s): %w", i, channelLabel(cfg), err)
		}
		slots = append(slots, providerSlot{label: channelLabel(cfg), provider: p})
	}
	return slots, nil
}

// NewMailer builds a Mailer from a single channel config.
func NewMailer(cfg MailConfig, opts ...MailerOption) (*Mailer, error) {
	return NewMailerMulti([]MailConfig{cfg}, opts...)
}

// NewMailerMulti builds a Mailer from multiple channel configs; sends rotate and fail over on errors.
func NewMailerMulti(channels []MailConfig, opts ...MailerOption) (*Mailer, error) {
	slots, err := buildSlots(channels)
	if err != nil {
		return nil, err
	}
	o := mailerOptions{retry: defaultRetryPolicy()}
	for _, fn := range opts {
		fn(&o)
	}
	m := &Mailer{
		channels: slots,
		retry:    o.retry.normalized(),
	}
	if o.mailLogUserID != nil {
		m.userID = *o.mailLogUserID
	}
	return m, nil
}

// NewMailerWithDB attaches GORM for mail_logs and sets user id for created rows.
func NewMailerWithDB(cfg MailConfig, db *gorm.DB, userID uint, opts ...MailerOption) (*Mailer, error) {
	return NewMailerMultiWithDB([]MailConfig{cfg}, db, userID, opts...)
}

// NewMailerMultiWithDB is like NewMailerMulti with DB and user id for logging.
func NewMailerMultiWithDB(channels []MailConfig, db *gorm.DB, userID uint, opts ...MailerOption) (*Mailer, error) {
	m, err := NewMailerMulti(channels, opts...)
	if err != nil {
		return nil, err
	}
	m.db = db
	m.userID = userID
	return m, nil
}

// NewMailerWithIP attaches GORM and client IP for mail_logs.ip_address.
// 默认 mail_logs.user_id 为 0；需要关联用户时请传 WithMailLogUserID。
func NewMailerWithIP(cfg MailConfig, db *gorm.DB, ip string, opts ...MailerOption) (*Mailer, error) {
	return NewMailerMultiWithIP([]MailConfig{cfg}, db, ip, opts...)
}

// NewMailerMultiWithIP is like NewMailerMulti with DB and IP for logging.
func NewMailerMultiWithIP(channels []MailConfig, db *gorm.DB, ip string, opts ...MailerOption) (*Mailer, error) {
	m, err := NewMailerMulti(channels, opts...)
	if err != nil {
		return nil, err
	}
	m.db = db
	m.ipAddress = ip
	return m, nil
}

// orderedChannels returns channels in round-robin order for this send attempt.
func (m *Mailer) orderedChannels() []providerSlot {
	n := len(m.channels)
	if n == 0 {
		return nil
	}
	if n == 1 {
		return m.channels
	}
	start := int(atomic.AddUint32(&m.rrCounter, 1)-1) % n
	out := make([]providerSlot, n)
	for i := 0; i < n; i++ {
		out[i] = m.channels[(start+i)%n]
	}
	return out
}

// SendHTML sends HTML mail with per-channel retries and cross-channel failover.
func (m *Mailer) SendHTML(ctx context.Context, to, subject, htmlBody string) error {
	if strings.TrimSpace(to) == "" {
		return errors.New("notification: empty recipient")
	}
	if len(m.channels) == 0 {
		return errors.New("notification: no channels configured")
	}

	policy := m.retry
	order := m.orderedChannels()

	var lastErr error
	var failParts []string
	totalAttempts := 0

	for chIdx, slot := range order {
		backoff := policy.InitialBackoff
		for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			totalAttempts++

			messageID, err := slot.provider.SendHTML(to, subject, htmlBody)
			if err == nil {
				m.logSendOutcome(to, subject, messageID, slot, nil)
				if m.db != nil {
					status := InitialMailStatus(slot.provider.Kind())
					_, dbErr := CreateMailLog(m.db, m.userID, string(slot.provider.Kind()), slot.label, to, subject, htmlBody, messageID, status, m.ipAddress)
					if dbErr != nil {
						logger.Error("notification: mail log create failed", zap.Error(dbErr),
							zap.String("to", to), zap.String("messageId", messageID), zap.String("channel", slot.label))
					}
				}
				return nil
			}

			lastErr = err
			failParts = append(failParts, fmt.Sprintf("[%s] %v", slot.label, err))
			logger.Warn("notification: send attempt failed",
				zap.Int("channelIndex", chIdx),
				zap.String("channel", slot.label),
				zap.Int("attempt", attempt),
				zap.Int("maxAttempts", policy.MaxAttempts),
				zap.String("to", to),
				zap.String("provider", string(slot.provider.Kind())),
				zap.Error(err))

			if attempt >= policy.MaxAttempts {
				break
			}
			if err := sleepCtx(ctx, backoff); err != nil {
				return err
			}
			if next := backoff * 2; next > policy.MaxBackoff {
				backoff = policy.MaxBackoff
			} else {
				backoff = next
			}
		}
	}

	errMsg := strings.Join(failParts, "; ")
	if len(errMsg) > 4000 {
		errMsg = errMsg[:4000] + "…"
	}
	m.logSendOutcome(to, subject, "", providerSlot{label: "multi", provider: nil}, lastErr)
	if m.db != nil {
		_, dbErr := CreateFailedMailLog(m.db, m.userID, "multi", "", to, subject, htmlBody, errMsg, totalAttempts, m.ipAddress)
		if dbErr != nil {
			logger.Error("notification: failed mail log create failed", zap.Error(dbErr), zap.String("to", to))
		}
	}
	if lastErr == nil {
		lastErr = errors.New("notification: all channels failed")
	}
	return lastErr
}

func (m *Mailer) logSendOutcome(to, subject, messageID string, slot providerSlot, err error) {
	fields := []zap.Field{
		zap.String("to", to),
		zap.String("subject", subject),
		zap.String("messageId", messageID),
		zap.String("channel", slot.label),
		zap.Uint("userId", m.userID),
	}
	if slot.provider != nil {
		fields = append(fields, zap.String("provider", string(slot.provider.Kind())))
	}
	if err != nil {
		logger.Error("notification: send failed", append(fields, zap.Error(err))...)
		return
	}
	logger.Info("notification: send ok", fields...)
}

// PrimaryProvider returns the first configured provider, or nil if none.
func (m *Mailer) PrimaryProvider() MailProvider {
	if len(m.channels) == 0 {
		return nil
	}
	return m.channels[0].provider
}

// ChannelCount returns the number of configured send channels.
func (m *Mailer) ChannelCount() int {
	return len(m.channels)
}

// NewProviderFromConfig returns the provider implementation for a single MailConfig.
func NewProviderFromConfig(cfg MailConfig) (MailProvider, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "sendcloud":
		if cfg.APIUser == "" || cfg.APIKey == "" || cfg.From == "" {
			return nil, fmt.Errorf("notification: sendcloud requires api_user, api_key, from")
		}
		return NewSendCloudClient(SendCloudConfig{
			APIUser:  cfg.APIUser,
			APIKey:   cfg.APIKey,
			From:     cfg.From,
			FromName: cfg.FromName,
		})
	default:
		if cfg.Host == "" || cfg.Port == 0 || cfg.From == "" {
			return nil, fmt.Errorf("notification: smtp requires host, port, from")
		}
		return NewSMTPClient(SMTPConfig{
			Host:     cfg.Host,
			Port:     cfg.Port,
			Username: cfg.Username,
			Password: cfg.Password,
			From:     cfg.From,
			FromName: cfg.FromName,
		})
	}
}
