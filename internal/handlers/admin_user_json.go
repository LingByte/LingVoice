// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"strconv"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/gin-gonic/gin"
)

// adminUserJSON 管理端用户视图：id 使用十进制字符串，避免前端 JSON number 超过 MAX_SAFE_INTEGER 时精度丢失。
func adminUserJSON(u models.User) gin.H {
	out := gin.H{
		"id":                 strconv.FormatUint(uint64(u.ID), 10),
		"email":              u.Email,
		"displayName":        u.DisplayName,
		"phone":              u.Phone,
		"firstName":          u.FirstName,
		"lastName":           u.LastName,
		"avatar":             u.Avatar,
		"gender":             u.Gender,
		"city":               u.City,
		"region":             u.Region,
		"timezone":           u.Timezone,
		"status":             u.Status,
		"role":               u.Role,
		"locale":             u.Locale,
		"source":             u.Source,
		"emailVerified":      u.EmailVerified,
		"phoneVerified":      u.PhoneVerified,
		"emailNotifications": u.EmailNotifications,
		"twoFactorEnabled":   u.TwoFactorEnabled,
		"loginCount":         u.LoginCount,
		"profileComplete":    u.ProfileComplete,
		"githubLogin":        u.GithubLogin,
		"wechatOpenId":       u.WechatOpenID,
		"remainQuota":        u.RemainQuota,
		"usedQuota":          u.UsedQuota,
		"unlimitedQuota":     u.UnlimitedQuota,
		"createdAt":          u.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":          u.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if u.LastLogin != nil && !u.LastLogin.IsZero() {
		out["lastLogin"] = u.LastLogin.UTC().Format(time.RFC3339)
	}
	if u.LastPasswordChange != nil && !u.LastPasswordChange.IsZero() {
		out["lastPasswordChange"] = u.LastPasswordChange.UTC().Format(time.RFC3339)
	}
	if u.AccountDeletionRequestedAt != nil && !u.AccountDeletionRequestedAt.IsZero() {
		out["accountDeletionRequestedAt"] = u.AccountDeletionRequestedAt.UTC().Format(time.RFC3339)
	}
	if u.AccountDeletionEffectiveAt != nil && !u.AccountDeletionEffectiveAt.IsZero() {
		out["accountDeletionEffectiveAt"] = u.AccountDeletionEffectiveAt.UTC().Format(time.RFC3339)
	}
	return out
}
