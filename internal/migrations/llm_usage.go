// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package migrations

import (
	"github.com/LingByte/LingVoice/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// llmUsageLegacy 仅用于 Migrator 检测/删除 session_id 列（GORM AutoMigrate 不会删列）。
type llmUsageLegacy struct {
	SessionID string `gorm:"column:session_id;type:varchar(64)"`
}

func (llmUsageLegacy) TableName() string { return "llm_usage" }

// DropLLMUsageSessionIDColumn 移除已废弃的 session_id（用量与平台会话解耦）。
func DropLLMUsageSessionIDColumn(db *gorm.DB) {
	if db == nil {
		return
	}
	if !db.Migrator().HasTable(&llmUsageLegacy{}) {
		return
	}
	if !db.Migrator().HasColumn(&llmUsageLegacy{}, "SessionID") {
		return
	}
	if err := db.Migrator().DropColumn(&llmUsageLegacy{}, "SessionID"); err != nil {
		logger.Warn("could not drop llm_usage.session_id (ignore if fresh DB)", zap.Error(err))
	}
}
