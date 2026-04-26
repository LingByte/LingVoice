// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package listeners

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/notification"
	"gorm.io/gorm"
)

// EnabledMailConfigs returns enabled email notification channels ordered by SortOrder.
func EnabledMailConfigs(db *gorm.DB) ([]notification.MailConfig, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	var rows []models.NotificationChannel
	if err := db.Where("type = ? AND enabled = ?", models.NotificationChannelTypeEmail, true).
		Order("sort_order ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]notification.MailConfig, 0, len(rows))
	for _, row := range rows {
		raw := strings.TrimSpace(row.ConfigJSON)
		if raw == "" {
			continue
		}
		var cfg notification.MailConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			continue
		}
		if strings.TrimSpace(cfg.Name) == "" {
			cfg.Name = row.Name
		}
		out = append(out, cfg)
	}
	if len(out) == 0 {
		return nil, errors.New("no enabled email notification channels")
	}
	return out, nil
}
