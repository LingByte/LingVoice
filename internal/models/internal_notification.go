// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package models

import (
	"time"

	"gorm.io/gorm"
)

// InternalNotification 站内通知（非邮件）；邮件通道仍使用 pkg/notification 与 MailTemplate。
// 前台与站点公告（SiteAnnouncement）同置于「系统消息」弹窗的「通知」页签，需登录后拉取 /api/internal-notifications。
// 字段与 BaseModel 对齐，并对 DeletedAt 使用 json:"deletedAt" 便于前端展示/同步。
type InternalNotification struct {
	ID        uint           `json:"id" gorm:"primaryKey"`
	CreatedAt time.Time      `json:"createdAt" gorm:"autoCreateTime;comment:Creation time"`
	UpdatedAt time.Time      `json:"updatedAt,omitempty" gorm:"autoUpdateTime;comment:Update time"`
	DeletedAt gorm.DeletedAt `json:"deletedAt,omitempty" gorm:"index"`
	CreateBy  string         `json:"createBy,omitempty" gorm:"size:128;comment:Creator"`
	UpdateBy  string         `json:"updateBy,omitempty" gorm:"size:128;comment:Updater"`
	Remark    string         `json:"remark,omitempty" gorm:"size:128;comment:Remark"`

	UserID uint `json:"userId" gorm:"index;not null;comment:接收用户 ID"`

	Title string `json:"title" gorm:"size:255;not null;comment:标题"`

	Content string `json:"content" gorm:"type:text;not null;comment:正文"`

	Read bool `json:"read" gorm:"default:false;index;comment:是否已读"`
}

// TableName GORM 表名
func (InternalNotification) TableName() string {
	return "internal_notifications"
}

// SetCreateInfo 设置创建人（与 BaseModel 行为一致）
func (m *InternalNotification) SetCreateInfo(operator string) {
	m.CreateBy = operator
	m.UpdateBy = operator
}

// SetUpdateInfo 设置更新人
func (m *InternalNotification) SetUpdateInfo(operator string) {
	m.UpdateBy = operator
}

// IsSoftDeleted 是否已软删除（与 BaseModel 判定方式一致）
func (m *InternalNotification) IsSoftDeleted() bool {
	return !m.DeletedAt.Time.IsZero()
}
