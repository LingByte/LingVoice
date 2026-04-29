// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import "time"

type Announcement struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Title     string    `json:"title" gorm:"size:255;not null"`
	Body      string    `json:"body" gorm:"type:text"`
	Pinned    bool      `json:"pinned" gorm:"default:false;index"`
	Enabled   bool      `json:"enabled" gorm:"default:true;index"`
	SortOrder int       `json:"sort_order" gorm:"default:0;index"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Announcement) TableName() string {
	return "announcements"
}
