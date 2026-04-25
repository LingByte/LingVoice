// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

// Package notification provides unified HTML email sending (SMTP or SendCloud),
// optional persistence on mail_logs, retries, and SendCloud webhook-driven status updates.
package notification

import (
	"context"
	"strings"
	"time"
)

// ProviderKind identifies how mail is sent and how status is tracked.
type ProviderKind string

const (
	ProviderSMTP      ProviderKind = "smtp"
	ProviderSendCloud ProviderKind = "sendcloud"
)

// Mail delivery / engagement status stored on MailLog.
// SMTP: after a successful handoff to the server we only record StatusSent (no callbacks).
// SendCloud: starts as StatusSent after API success; webhooks may refine to delivered, opened, etc.
const (
	StatusQueued        = "queued"
	StatusSent          = "sent"
	StatusDelivered    = "delivered"
	StatusFailed        = "failed"
	StatusSoftBounce    = "soft_bounce"
	StatusBounced       = "bounced"
	StatusInvalid       = "invalid"
	StatusSpam          = "spam"
	StatusClicked       = "clicked"
	StatusOpened        = "opened"
	StatusUnsubscribed  = "unsubscribed"
	StatusUnknown       = "unknown"
)

// MailProvider sends HTML mail and reports a provider-specific message id (may be empty).
type MailProvider interface {
	Kind() ProviderKind
	SendHTML(to, subject, htmlBody string) (messageID string, err error)
}

// MailConfig selects provider and credentials (JSON tags for config files).
// Name is optional and used in mail_logs and ops logs to identify the channel when multiple are configured.
type MailConfig struct {
	Provider string `json:"provider"` // "smtp" | "sendcloud"
	Name     string `json:"name"`     // channel label, e.g. "primary-smtp", "backup-sendcloud"

	Host     string `json:"host"`
	Port     int64  `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`

	APIUser string `json:"api_user"`
	APIKey  string `json:"api_key"`

	From     string `json:"from"`               // 发件邮箱；也可写 RFC 格式「显示名 <email>」
	FromName string `json:"from_name,omitempty"` // 可选显示名（如 解忧造物）；与 From 中 Name 二选一，此项在纯邮箱 From 时生效
}

// MultiChannelMailConfig is a convenience wrapper for JSON/YAML config: ordered list of channels
// passed to NewMailerMulti / NewMailerMultiWithDB (first channel is default primary; order is preserved for failover).
type MultiChannelMailConfig struct {
	Channels []MailConfig `json:"channels"`
}

// RetryPolicy controls send retries (exponential backoff between attempts).
type RetryPolicy struct {
	MaxAttempts    int           // total tries including the first; default 1 = no retry
	InitialBackoff time.Duration // delay before 2nd attempt; default 200ms
	MaxBackoff     time.Duration // cap; default 5s
}

func defaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: 200 * time.Millisecond,
		MaxBackoff:     5 * time.Second,
	}
}

func (p RetryPolicy) normalized() RetryPolicy {
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = 200 * time.Millisecond
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = 5 * time.Second
	}
	if p.MaxBackoff < p.InitialBackoff {
		p.MaxBackoff = p.InitialBackoff
	}
	return p
}

// MailerOption configures optional behaviour for Mailer.
type MailerOption func(*mailerOptions)

type mailerOptions struct {
	retry RetryPolicy
}

// WithRetry sets retry policy (merged with defaults for zero fields if you only set MaxAttempts).
func WithRetry(p RetryPolicy) MailerOption {
	return func(o *mailerOptions) {
		o.retry = p
	}
}

// InitialMailStatus returns the DB status right after a successful provider send.
func InitialMailStatus(kind ProviderKind) string {
	switch kind {
	case ProviderSMTP:
		return StatusSent
	case ProviderSendCloud:
		return StatusSent
	default:
		return StatusSent
	}
}

// SendCloudEventToStatus maps SendCloud webhook event codes (numeric or common names) to MailLog status.
func SendCloudEventToStatus(event string) string {
	e := strings.TrimSpace(strings.ToLower(event))
	switch e {
	case "1", "deliver", "delivered":
		return StatusDelivered
	case "3", "spam":
		return StatusSpam
	case "4", "invalid":
		return StatusInvalid
	case "5", "soft_bounce", "softbounce":
		return StatusSoftBounce
	case "10", "click", "clicked":
		return StatusClicked
	case "11", "open", "opened":
		return StatusOpened
	case "12", "unsubscribe", "unsubscribed":
		return StatusUnsubscribed
	case "18", "request":
		return StatusSent
	default:
		return StatusUnknown
	}
}

// sleepCtx waits up to d or returns ctx.Err().
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
