// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
)

// 控制台「≈USD」展示倍率：与 new-api 控制台美元余额观感对齐；实际扣减仍以整数额度单位为准。
const dashboardUSDPerQuotaUnit = 0.01

type dashboardModelAgg struct {
	Model  string `json:"model"`
	Count  int64  `json:"count"`
	Tokens int64  `json:"tokens"`
	Quota  int64  `json:"quota"`
}

// getDashboardOverview GET /api/dashboard/overview?days=30
// 当前登录用户：账户额度、周期内 LLM 用量聚合与模型排行（对齐 new-api 数据面板信息架构）。
func (h *Handlers) getDashboardOverview(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	days := parseQueryInt(c, "days", 30)
	if days < 1 {
		days = 1
	}
	if days > 365 {
		days = 365
	}
	from := time.Now().AddDate(0, 0, -days)

	var fresh models.User
	if err := h.db.Where("id = ?", u.ID).First(&fresh).Error; err != nil {
		response.Fail(c, "用户查询失败", gin.H{"error": err.Error()})
		return
	}

	uid := strconv.FormatUint(uint64(u.ID), 10)
	fromDay := from.UTC().Format("2006-01-02")
	toDay := time.Now().UTC().Format("2006-01-02")

	var agg struct {
		TotalReq   int64 `gorm:"column:total_req"`
		SuccessReq int64 `gorm:"column:success_req"`
		TokenSum   int64 `gorm:"column:token_sum"`
		QuotaSum   int64 `gorm:"column:quota_sum"`
	}
	if err := h.db.Model(&models.LLMUsageUserDaily{}).
		Select(`COALESCE(SUM(request_count),0) AS total_req,
			COALESCE(SUM(success_count),0) AS success_req,
			COALESCE(SUM(token_sum),0) AS token_sum,
			COALESCE(SUM(quota_sum),0) AS quota_sum`).
		Where("user_id = ? AND stat_date >= ? AND stat_date <= ?", uid, fromDay, toDay).
		Scan(&agg).Error; err != nil {
		response.Fail(c, "统计失败", gin.H{"error": err.Error()})
		return
	}
	// 无上卷数据时回退一次明细表（升级后首次访问或历史数据）
	if agg.TotalReq == 0 && agg.SuccessReq == 0 && agg.TokenSum == 0 && agg.QuotaSum == 0 {
		q := h.db.Model(&models.LLMUsage{}).
			Select(`COUNT(1) AS total_req,
			COALESCE(SUM(CASE WHEN success THEN 1 ELSE 0 END),0) AS success_req,
			COALESCE(SUM(total_tokens),0) AS token_sum,
			COALESCE(SUM(quota_delta),0) AS quota_sum`).
			Where("user_id = ?", uid).
			Where("completed_at >= ?", from)
		if err := q.Scan(&agg).Error; err != nil {
			response.Fail(c, "统计失败", gin.H{"error": err.Error()})
			return
		}
	}

	elapsedMin := time.Since(from).Minutes()
	if elapsedMin < 1 {
		elapsedMin = 1
	}
	avgRPM := float64(agg.SuccessReq) / elapsedMin
	avgTPM := float64(agg.TokenSum) / elapsedMin
	if math.IsNaN(avgRPM) || math.IsInf(avgRPM, 0) {
		avgRPM = 0
	}
	if math.IsNaN(avgTPM) || math.IsInf(avgTPM, 0) {
		avgTPM = 0
	}

	var modelsTop []dashboardModelAgg
	_ = h.db.Raw(`
SELECT model,
       COALESCE(SUM(request_count),0) AS count,
       COALESCE(SUM(token_sum),0) AS tokens,
       COALESCE(SUM(quota_sum),0) AS quota
FROM llm_usage_user_model_daily
WHERE user_id = ? AND stat_date >= ? AND stat_date <= ?
GROUP BY model
ORDER BY count DESC
LIMIT 8`, uid, fromDay, toDay).Scan(&modelsTop).Error
	if len(modelsTop) == 0 {
		_ = h.db.Model(&models.LLMUsage{}).
			Select("model, COUNT(1) AS count, COALESCE(SUM(total_tokens),0) AS tokens, COALESCE(SUM(quota_delta),0) AS quota").
			Where("user_id = ? AND completed_at >= ? AND success = ?", uid, from, true).
			Group("model").
			Order("count DESC").
			Limit(8).
			Scan(&modelsTop).Error
	}

	var dailySeries []models.LLMUsageUserDaily
	_ = h.db.Where("user_id = ? AND stat_date >= ? AND stat_date <= ?", uid, fromDay, toDay).
		Order("stat_date ASC").
		Find(&dailySeries).Error

	type dashboardUserRankRow struct {
		UserID       string `json:"user_id"`
		Email        string `json:"email"`
		QuotaSum     int64  `json:"quota_sum"`
		SuccessCount int64  `json:"success_count"`
		TokenSum     int64  `json:"token_sum"`
	}
	var usersRank []dashboardUserRankRow
	if u.IsAdmin() {
		type rankAgg struct {
			UserID       string
			QuotaSum     int64
			SuccessCount int64
			TokenSum     int64
		}
		var aggs []rankAgg
		_ = h.db.Model(&models.LLMUsageUserDaily{}).
			Select(`user_id,
				COALESCE(SUM(quota_sum),0) AS quota_sum,
				COALESCE(SUM(success_count),0) AS success_count,
				COALESCE(SUM(token_sum),0) AS token_sum`).
			Where("stat_date >= ? AND stat_date <= ?", fromDay, toDay).
			Group("user_id").
			Order("quota_sum DESC").
			Limit(10).
			Scan(&aggs).Error
		for _, a := range aggs {
			row := dashboardUserRankRow{
				UserID:       a.UserID,
				QuotaSum:     a.QuotaSum,
				SuccessCount: a.SuccessCount,
				TokenSum:     a.TokenSum,
			}
			if idv, err := strconv.ParseUint(strings.TrimSpace(a.UserID), 10, 64); err == nil && idv > 0 {
				var urow models.User
				if err := h.db.Select("email").Where("id = ?", uint(idv)).First(&urow).Error; err == nil {
					row.Email = urow.Email
				}
			}
			usersRank = append(usersRank, row)
		}
	}

	name := strings.TrimSpace(fresh.DisplayName)
	if name == "" {
		name = strings.TrimSpace(fresh.Email)
		if at := strings.Index(name, "@"); at > 0 {
			name = name[:at]
		}
	}
	if name == "" {
		name = "用户"
	}

	balanceUSD := float64(fresh.RemainQuota) * dashboardUSDPerQuotaUnit
	spentUSD := float64(fresh.UsedQuota) * dashboardUSDPerQuotaUnit
	statQuotaUSD := float64(agg.QuotaSum) * dashboardUSDPerQuotaUnit

	payload := gin.H{
		"greeting_name": name,
		"email":         fresh.Email,
		"is_admin":      u.IsAdmin(),
		"period": gin.H{
			"days":         days,
			"from_rfc3339": from.UTC().Format(time.RFC3339),
			"to_rfc3339":   time.Now().UTC().Format(time.RFC3339),
		},
		"account": gin.H{
			"remain_quota":       fresh.RemainQuota,
			"used_quota":         fresh.UsedQuota,
			"unlimited_quota":    fresh.UnlimitedQuota,
			"balance_usd":        balanceUSD,
			"history_spend_usd":  spentUSD,
			"usd_per_quota_unit": dashboardUSDPerQuotaUnit,
		},
		"usage": gin.H{
			"request_count": agg.TotalReq,
			"stat_count":    agg.SuccessReq,
		},
		"resource": gin.H{
			"stat_quota_units": agg.QuotaSum,
			"stat_quota_usd":   statQuotaUSD,
			"stat_tokens":      agg.TokenSum,
		},
		"performance": gin.H{
			"avg_rpm": math.Round(avgRPM*1000) / 1000,
			"avg_tpm": math.Round(avgTPM*1000) / 1000,
		},
		"models":       modelsTop,
		"daily_series": dailySeries,
		"users_rank":   usersRank,
	}
	response.Success(c, "ok", payload)
}
