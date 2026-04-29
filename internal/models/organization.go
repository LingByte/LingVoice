package models

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

const (
	OrgTypePersonal = "personal"
	OrgTypeTeam     = "team"
)

// Organization is the tenant boundary for resources (notification channels, templates, logs, etc.).
type Organization struct {
	BaseModel
	Type        string `json:"type" gorm:"size:32;not null;index;comment:personal|team"`
	Name        string `json:"name" gorm:"size:128;not null;comment:organization name"`
	OwnerUserID uint   `json:"ownerUserId" gorm:"index;not null;comment:creator/owner user id"`
}

func (Organization) TableName() string { return "organizations" }

// OrgMember links users to organizations.
type OrgMember struct {
	BaseModel
	OrgID  uint   `json:"orgId" gorm:"index;not null"`
	UserID uint   `json:"userId" gorm:"index;not null"`
	Role   string `json:"role" gorm:"size:32;not null;default:'member'"`
}

func (OrgMember) TableName() string { return "org_members" }

// EnsurePersonalOrg ensures the user has a personal org and sets user.DefaultOrgID when missing.
func EnsurePersonalOrg(db *gorm.DB, user *User) error {
	if db == nil || user == nil || user.ID == 0 {
		return nil
	}
	if user.DefaultOrgID != 0 {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		// Re-check under transaction.
		var fresh User
		if err := tx.Where("id = ?", user.ID).First(&fresh).Error; err != nil {
			return err
		}
		if fresh.DefaultOrgID != 0 {
			user.DefaultOrgID = fresh.DefaultOrgID
			return nil
		}
		name := strings.TrimSpace(user.DisplayName)
		if name == "" {
			name = strings.TrimSpace(user.Email)
		}
		if name == "" {
			name = "个人空间"
		}
		org := Organization{
			Type:        OrgTypePersonal,
			Name:        name,
			OwnerUserID: user.ID,
		}
		org.SetCreateInfo(strings.TrimSpace(user.Email))
		if err := tx.Create(&org).Error; err != nil {
			return err
		}
		member := OrgMember{
			OrgID:  org.ID,
			UserID: user.ID,
			Role:   "owner",
		}
		member.SetCreateInfo(strings.TrimSpace(user.Email))
		if err := tx.Create(&member).Error; err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", user.ID).Update("default_org_id", org.ID).Error; err != nil {
			return err
		}
		user.DefaultOrgID = org.ID
		return nil
	})
}

// IsOrgMember checks if user is a member of org.
func IsOrgMember(db *gorm.DB, orgID, userID uint) (bool, error) {
	if db == nil {
		return false, errors.New("nil db")
	}
	if orgID == 0 || userID == 0 {
		return false, nil
	}
	var cnt int64
	if err := db.Model(&OrgMember{}).Where("org_id = ? AND user_id = ?", orgID, userID).Count(&cnt).Error; err != nil {
		return false, err
	}
	return cnt > 0, nil
}
