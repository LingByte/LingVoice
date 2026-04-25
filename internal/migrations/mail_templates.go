// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package migrations

import (
	"github.com/LingByte/LingVoice/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// legacyMailTpl exists only so Migrator can address subject_tpl before the field is removed from models.MailTemplate.
type legacyMailTpl struct {
	SubjectTpl string `gorm:"column:subject_tpl"`
}

func (legacyMailTpl) TableName() string { return "mail_templates" }

// DropMailTemplateSubjectTplColumn removes deprecated subject_tpl when present (GORM AutoMigrate does not drop columns).
func DropMailTemplateSubjectTplColumn(db *gorm.DB) {
	if db == nil {
		return
	}
	if !db.Migrator().HasTable(&legacyMailTpl{}) {
		return
	}
	if !db.Migrator().HasColumn(&legacyMailTpl{}, "SubjectTpl") {
		return
	}
	if err := db.Migrator().DropColumn(&legacyMailTpl{}, "SubjectTpl"); err != nil {
		logger.Warn("could not drop mail_templates.subject_tpl (ignore if fresh DB)", zap.Error(err))
	}
}
