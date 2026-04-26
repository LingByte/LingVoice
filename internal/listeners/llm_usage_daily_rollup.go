// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package listeners

import (
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/llm"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func statDateUTC(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	} else {
		t = t.UTC()
	}
	return t.Format("2006-01-02")
}

// upsertLLMUsageRollupsFromPayload 在 llm_usage 落库后增量更新日聚合表（与 new-api 按日建标思路一致）。
func upsertLLMUsageRollupsFromPayload(db *gorm.DB, lg *zap.Logger, p *llm.LLMUsageSignalPayload, completedAt time.Time) {
	if db == nil || p == nil {
		return
	}
	uid := strings.TrimSpace(p.UserID)
	if uid == "" {
		return
	}
	day := statDateUTC(completedAt)

	reqInc := int64(1)
	sucInc := int64(0)
	if p.Success {
		sucInc = 1
	}
	tok := int64(p.TotalTokens)
	if tok <= 0 {
		tok = int64(p.InputTokens + p.OutputTokens)
	}
	if tok < 0 {
		tok = 0
	}
	q := int64(p.QuotaDelta)
	if q < 0 {
		q = 0
	}

	userRow := models.LLMUsageUserDaily{
		UserID:       uid,
		StatDate:     day,
		RequestCount: reqInc,
		SuccessCount: sucInc,
		TokenSum:     tok,
		QuotaSum:     q,
	}
	if err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "stat_date"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"request_count": gorm.Expr("request_count + ?", reqInc),
			"success_count": gorm.Expr("success_count + ?", sucInc),
			"token_sum":     gorm.Expr("token_sum + ?", tok),
			"quota_sum":     gorm.Expr("quota_sum + ?", q),
		}),
	}).Create(&userRow).Error; err != nil && lg != nil {
		lg.Warn("llm usage rollup user_daily", zap.Error(err), zap.String("user_id", uid), zap.String("stat_date", day))
	}

	mod := strings.TrimSpace(p.Model)
	if mod == "" {
		return
	}
	modelRow := models.LLMUsageUserModelDaily{
		UserID:       uid,
		StatDate:     day,
		Model:        mod,
		RequestCount: reqInc,
		SuccessCount: sucInc,
		TokenSum:     tok,
		QuotaSum:     q,
	}
	if err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "stat_date"}, {Name: "model"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"request_count": gorm.Expr("request_count + ?", reqInc),
			"success_count": gorm.Expr("success_count + ?", sucInc),
			"token_sum":     gorm.Expr("token_sum + ?", tok),
			"quota_sum":     gorm.Expr("quota_sum + ?", q),
		}),
	}).Create(&modelRow).Error; err != nil && lg != nil {
		lg.Warn("llm usage rollup model_daily", zap.Error(err), zap.String("user_id", uid), zap.String("model", mod))
	}
}
