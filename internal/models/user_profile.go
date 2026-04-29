// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

// UserProfile 用户非核心资料与偏好（与 users 表 1:1，按 user_id 主键）。
type UserProfile struct {
	UserID             uint   `json:"userId" gorm:"primaryKey;comment:users.id"`
	Locale             string `json:"locale,omitempty" gorm:"size:20"`
	Timezone           string `json:"timezone,omitempty" gorm:"size:200"`
	Gender             string `json:"gender,omitempty" gorm:"size:32"`
	City               string `json:"city,omitempty" gorm:"size:128"`
	Region             string `json:"region,omitempty" gorm:"size:128"`
	EmailNotifications bool   `json:"emailNotifications" gorm:"default:true"`
	ProfileComplete    int    `json:"profileComplete" gorm:"default:0"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (UserProfile) TableName() string {
	return "user_profiles"
}

// GetUserProfile 读取资料行；无记录时返回 (nil, nil)。
func GetUserProfile(db *gorm.DB, userID uint) (*UserProfile, error) {
	if userID == 0 {
		return nil, nil
	}
	var p UserProfile
	err := db.Where("user_id = ?", userID).First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// EnsureUserProfile 保证存在 user_profiles 行（默认开启邮件通知）。
func EnsureUserProfile(db *gorm.DB, userID uint) (*UserProfile, error) {
	if userID == 0 {
		return nil, errors.New("invalid user id")
	}
	p := UserProfile{UserID: userID}
	err := db.Where(UserProfile{UserID: userID}).
		Attrs(UserProfile{EmailNotifications: true}).
		FirstOrCreate(&p).Error
	return &p, err
}

// UpdateUserProfileFields 按 map 更新资料表（键为 snake_case 列名）。
func UpdateUserProfileFields(db *gorm.DB, userID uint, vals map[string]any) error {
	if userID == 0 || len(vals) == 0 {
		return nil
	}
	if _, err := EnsureUserProfile(db, userID); err != nil {
		return err
	}
	return db.Model(&UserProfile{}).Where("user_id = ?", userID).Updates(vals).Error
}

// CalculateProfileComplete 计算资料完整度（需同时传入用户主表与资料表）。
func CalculateProfileComplete(user *User, prof *UserProfile) int {
	if user == nil {
		return 0
	}
	city, region, tz, loc := "", "", "", ""
	if prof != nil {
		city = prof.City
		region = prof.Region
		tz = prof.Timezone
		loc = prof.Locale
	}

	complete := 0
	total := 0

	total += 4
	if user.DisplayName != "" {
		complete++
	}
	if user.FirstName != "" {
		complete++
	}
	if user.LastName != "" {
		complete++
	}
	if user.Avatar != "" {
		complete++
	}

	total += 3
	if user.Email != "" {
		complete++
	}
	if user.Phone != "" {
		complete++
	}
	if user.EmailVerified {
		complete++
	}

	total += 2
	if city != "" {
		complete++
	}
	if region != "" {
		complete++
	}

	total += 1
	if tz != "" || loc != "" {
		complete++
	}

	total += 2
	if user.WechatOpenID != "" {
		complete++
	}
	if user.GithubID != "" {
		complete++
	}

	percentage := (complete * 100) / total
	if percentage > 100 {
		percentage = 100
	}
	return percentage
}

// UpdateProfileComplete 将完整度写回 user_profiles。
func UpdateProfileComplete(db *gorm.DB, user *User) error {
	if user == nil {
		return errors.New("nil user")
	}
	prof, err := EnsureUserProfile(db, user.ID)
	if err != nil {
		return err
	}
	complete := CalculateProfileComplete(user, prof)
	return db.Model(&UserProfile{}).Where("user_id = ?", user.ID).Update("profile_complete", complete).Error
}

// UpdateNotificationSettings 更新通知设置（资料表）。
func UpdateNotificationSettings(db *gorm.DB, user *User, settings map[string]bool) error {
	if user == nil {
		return errors.New("nil user")
	}
	vals := make(map[string]any)
	if emailNotif, ok := settings["emailNotifications"]; ok {
		vals["email_notifications"] = emailNotif
	}
	if len(vals) == 0 {
		return nil
	}
	return UpdateUserProfileFields(db, user.ID, vals)
}

// UpdatePreferences 更新 locale / timezone（资料表）。
func UpdatePreferences(db *gorm.DB, user *User, preferences map[string]string) error {
	if user == nil {
		return errors.New("nil user")
	}
	vals := make(map[string]any)
	if timezone, ok := preferences["timezone"]; ok {
		vals["timezone"] = timezone
	}
	if locale, ok := preferences["locale"]; ok {
		vals["locale"] = locale
	}
	if len(vals) == 0 {
		return nil
	}
	return UpdateUserProfileFields(db, user.ID, vals)
}
