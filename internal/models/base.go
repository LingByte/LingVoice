package models

import (
	"time"

	"github.com/LingByte/LingVoice/pkg/utils"
	"gorm.io/gorm"
)

const (
	SoftDeleteStatusActive  int8 = 0 // Not deleted
	SoftDeleteStatusDeleted int8 = 1 // Deleted
)

type BaseModel struct {
	ID        uint           `json:"id,string" gorm:"primaryKey;autoIncrement:false"`
	CreatedAt time.Time      `json:"createdAt" gorm:"autoCreateTime;comment:Creation time"`
	UpdatedAt time.Time      `json:"updatedAt,omitempty" gorm:"autoUpdateTime;comment:Update time"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
	CreateBy  string         `json:"createBy,omitempty" gorm:"size:128;comment:Creator"`
	UpdateBy  string         `json:"updateBy,omitempty" gorm:"size:128;comment:Updater"`
	Remark    string         `json:"remark,omitempty" gorm:"size:128;comment:Remark"`
}

// BeforeCreate GORM hook to generate snowflake ID before creating a record
func (m *BaseModel) BeforeCreate(tx *gorm.DB) error {
	if m.ID == 0 {
		m.ID = utils.GenUintID()
	}
	return nil
}

// IsSoftDeleted 判断是否已删除
func (m *BaseModel) IsSoftDeleted() bool {
	return !m.DeletedAt.Time.IsZero()
}

// SetCreateInfo 设置创建人信息
func (m *BaseModel) SetCreateInfo(operator string) {
	m.CreateBy = operator
	m.UpdateBy = operator
}

// SetUpdateInfo 设置更新人信息
func (m *BaseModel) SetUpdateInfo(operator string) {
	m.UpdateBy = operator
}

// GetCreatedAtString 获取格式化创建时间
func (m *BaseModel) GetCreatedAtString() string {
	return m.CreatedAt.Format("2006-01-02 15:04:05")
}

// GetUpdatedAtString 获取格式化更新时间
func (m *BaseModel) GetUpdatedAtString() string {
	if m.UpdatedAt.IsZero() {
		return ""
	}
	return m.UpdatedAt.Format("2006-01-02 15:04:05")
}

// GetCreatedAtUnix 获取创建时间戳
func (m *BaseModel) GetCreatedAtUnix() int64 {
	return m.CreatedAt.Unix()
}

// GetUpdatedAtUnix 获取更新时间戳
func (m *BaseModel) GetUpdatedAtUnix() int64 {
	if m.UpdatedAt.IsZero() {
		return 0
	}
	return m.UpdatedAt.Unix()
}
