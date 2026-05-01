package models

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/constants"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"github.com/gin-gonic/gin"
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
		m.ID = base.GenUintID()
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

const orgHeader = "X-Org-ID"

// CurrentOrgID resolves organization id for this request.
// Resolution: header X-Org-ID (if member) -> user's DefaultOrgID -> 0 (system).
func CurrentOrgID(c *gin.Context) uint {
	u := CurrentUser(c)
	if u == nil {
		return 0
	}
	if dbAny, ok := c.Get(constants.DbField); ok {
		if db, ok := dbAny.(*gorm.DB); ok && db != nil {
			_ = EnsurePersonalOrg(db, u)
		}
	}
	if raw := strings.TrimSpace(c.GetHeader(orgHeader)); raw != "" {
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil && n > 0 {
			orgID := uint(n)
			if dbAny, ok := c.Get(constants.DbField); ok {
				if db, ok := dbAny.(*gorm.DB); ok && db != nil {
					if okm, _ := IsOrgMember(db, orgID, u.ID); okm {
						return orgID
					}
				}
			}
		}
	}
	return u.DefaultOrgID
}

// OperatorFromUser returns a stable operator string for audit fields.
func OperatorFromUser(u *User) string {
	if u == nil {
		return ""
	}
	if strings.TrimSpace(u.Email) != "" {
		return strings.TrimSpace(u.Email)
	}
	return fmt.Sprintf("uid:%d", u.ID)
}

// ParseUintParam parses a positive uint path param.
func ParseUintParam(c *gin.Context, name string) (uint, bool) {
	s := strings.TrimSpace(c.Param(name))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 {
		return 0, false
	}
	max := uint64(^uint(0))
	if n > max {
		return 0, false
	}
	return uint(n), true
}

// ParseInt64Param parses a positive path param (e.g. snowflake id).
func ParseInt64Param(c *gin.Context, name string) (int64, bool) {
	s := strings.TrimSpace(c.Param(name))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ParseIntParam parses a positive int path param.
func ParseIntParam(c *gin.Context, name string) (int, bool) {
	s := strings.TrimSpace(c.Param(name))
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ParseQueryInt parses a query int, falling back to def on empty or invalid input.
func ParseQueryInt(c *gin.Context, name string, def int) int {
	s := strings.TrimSpace(c.Query(name))
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// ClampPageSize normalizes list page size to (0,100], default 20.
func ClampPageSize(n int) int {
	switch {
	case n <= 0:
		return 20
	case n > 100:
		return 100
	default:
		return n
	}
}

// ParseQueryUint parses a positive uint query param.
func ParseQueryUint(c *gin.Context, name string) (uint, bool) {
	s := strings.TrimSpace(c.Query(name))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 {
		return 0, false
	}
	max := uint64(^uint(0))
	if n > max {
		return 0, false
	}
	return uint(n), true
}

func ParseQueryTime(c *gin.Context, name string) (time.Time, bool) {
	s := strings.TrimSpace(c.Query(name))
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func ParseQueryBool(c *gin.Context, name string) (bool, bool) {
	s := strings.ToLower(strings.TrimSpace(c.Query(name)))
	if s == "" {
		return false, false
	}
	switch s {
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return false, true
	default:
		return false, false
	}
}
