// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package listeners

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/llm"
	"github.com/LingByte/LingVoice/pkg/utils"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func newLLMUsageRowID() string {
	if utils.SnowflakeUtil != nil {
		return utils.SnowflakeUtil.GenID()
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
	utils.Sig().Connect(llm.SignalLLMUsage, func(sender any, params ...any) {
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
		}
	})
}
