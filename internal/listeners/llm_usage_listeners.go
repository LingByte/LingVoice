// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package listeners

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func newLLMUsageRowID() string {
	if base.SnowflakeUtil != nil {
		return base.SnowflakeUtil.GenID()
	}
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

// InitLLMUsageListener 订阅 llm.SignalLLMUsage，将用量写入 llm_usage（按 request_id 去重）。
func InitLLMUsageListener(db *gorm.DB, lg *zap.Logger) {
	if db == nil {
		if lg != nil {
			lg.Warn("InitLLMUsageListener: nil db, skipped")
		}
		return
	}
	base.Sig().Connect(llm.SignalLLMUsage, func(sender any, params ...any) {
		p, ok := sender.(*llm.LLMUsageSignalPayload)
		if !ok || p == nil {
			return
		}
		if p.RequestID == "" {
			return
		}
		var existing models.LLMUsage
		if err := db.Where("request_id = ?", p.RequestID).First(&existing).Error; err == nil {
			return
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			if lg != nil {
				lg.Warn("llm usage listener: lookup request_id", zap.Error(err), zap.String("request_id", p.RequestID))
			}
			return
		}

		var attempts models.LLMUsageChannelAttempts
		if len(p.ChannelAttempts) > 0 {
			b, mErr := json.Marshal(p.ChannelAttempts)
			if mErr == nil {
				_ = json.Unmarshal(b, &attempts)
			}
		}

		row := models.LLMUsage{
			ID:              newLLMUsageRowID(),
			RequestID:       p.RequestID,
			UserID:          p.UserID,
			Provider:        p.Provider,
			Model:           p.Model,
			BaseURL:         p.BaseURL,
			RequestType:     p.RequestType,
			InputTokens:     p.InputTokens,
			OutputTokens:    p.OutputTokens,
			TotalTokens:     p.TotalTokens,
			QuotaDelta:      p.QuotaDelta,
			LatencyMs:       p.LatencyMs,
			TTFTMs:          p.TTFTMs,
			TPS:             p.TPS,
			QueueTimeMs:     p.QueueTimeMs,
			RequestContent:  p.RequestContent,
			ResponseContent: p.ResponseContent,
			UserAgent:       p.UserAgent,
			IPAddress:       p.IPAddress,
			StatusCode:      p.StatusCode,
			Success:         p.Success,
			ErrorCode:       p.ErrorCode,
			ErrorMessage:    p.ErrorMessage,
			ChannelID:       p.ChannelID,
			ChannelAttempts: attempts,
			RequestedAt:     msToTime(p.RequestedAtMs),
			StartedAt:       msToTime(p.StartedAtMs),
			FirstTokenAt:    msToTime(p.FirstTokenAtMs),
			CompletedAt:     msToTime(p.CompletedAtMs),
		}
		if err := db.Create(&row).Error; err != nil && lg != nil {
			lg.Warn("llm usage listener: create failed", zap.Error(err), zap.String("request_id", p.RequestID))
			return
		}
		upsertLLMUsageRollupsFromPayload(db, lg, p, row.CompletedAt)
	})
}

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
