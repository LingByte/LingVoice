// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"strconv"

	"gorm.io/gorm"
)

// CredentialUserIDString returns user id as string for usage rows; empty if unset.
func CredentialUserIDString(userID int) string {
	if userID <= 0 {
		return ""
	}
	return strconv.Itoa(userID)
}

func applyUserQuotaAfterCredentialSpend(db *gorm.DB, userID int, delta int) {
	if db == nil || userID <= 0 || delta < 1 {
		return
	}
	var row User
	if err := db.Select("id", "unlimited_quota").Where("id = ?", userID).First(&row).Error; err != nil {
		return
	}
	if row.UnlimitedQuota {
		_ = db.Model(&User{}).Where("id = ?", userID).
			Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error
		return
	}
	_ = db.Model(&User{}).Where("id = ? AND remain_quota > ?", userID, 0).
		Update("remain_quota", gorm.Expr("remain_quota - ?", delta)).Error
	_ = db.Model(&User{}).Where("id = ?", userID).
		Update("used_quota", gorm.Expr("used_quota + ?", delta)).Error
}

// DecrementCredentialAndUserQuota bumps credential used / remain and mirrors delta on linked user quota.
func DecrementCredentialAndUserQuota(db *gorm.DB, cred *Credential, delta int) {
	if cred == nil || db == nil || delta < 1 {
		return
	}
	_ = BumpCredentialUsedAndDecrementRemain(db, cred.Id, cred.UnlimitedQuota, delta)
	applyUserQuotaAfterCredentialSpend(db, cred.UserId, delta)
}
