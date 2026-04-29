package models

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/utils"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var ErrTwoFANotEnabled = errors.New("2fa not enabled")

const (
	twoFAMaxFailedAttempts = 5
	twoFALockoutSeconds    = 300
)

func normalizeTwoFABackupCode(code string) string {
	s := strings.ToUpper(strings.TrimSpace(code))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	return s
}

func isValidTwoFABackupCodeInput(code string) bool {
	n := normalizeTwoFABackupCode(code)
	if len(n) < 8 || len(n) > 32 {
		return false
	}
	for _, r := range n {
		if unicode.IsDigit(r) || (r >= 'A' && r <= 'Z') {
			continue
		}
		return false
	}
	return true
}

func twoFABackupCodeMatchesPlain(plainNormalized, storedHash string) bool {
	if plainNormalized == "" || storedHash == "" {
		return false
	}
	return HashPassword(plainNormalized) == storedHash
}

// GenerateTwoFATOTPSetupMaterial 生成绑定 TOTP 所需的密钥、otpauth URL 与二维码（依赖 pkg/utils/totp）。
// 将 Secret 写入 TwoFA 前应由用户用验证器校验首码。
func GenerateTwoFATOTPSetupMaterial(issuer string, user *User) (*utils.TOTPSetup, error) {
	if user == nil {
		return nil, errors.New("用户不能为空")
	}
	account := strings.TrimSpace(user.Email)
	if account == "" {
		account = fmt.Sprintf("user-%d", user.ID)
	}
	return utils.GenerateTOTPSetup(issuer, account, 0)
}

// TwoFA 用户2FA设置表
type TwoFA struct {
	Id             int            `json:"id" gorm:"primaryKey"`
	UserId         int            `json:"user_id" gorm:"unique;not null;index"`
	Secret         string         `json:"-" gorm:"type:varchar(255);not null"` // TOTP密钥，不返回给前端
	IsEnabled      bool           `json:"is_enabled"`
	FailedAttempts int            `json:"failed_attempts" gorm:"default:0"`
	LockedUntil    *time.Time     `json:"locked_until,omitempty"`
	LastUsedAt     *time.Time     `json:"last_used_at,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"-" gorm:"index"`
}

// TwoFABackupCode 备用码使用记录表
type TwoFABackupCode struct {
	Id        int            `json:"id" gorm:"primaryKey"`
	UserId    int            `json:"user_id" gorm:"not null;index"`
	CodeHash  string         `json:"-" gorm:"type:varchar(255);not null"` // 备用码哈希
	IsUsed    bool           `json:"is_used"`
	UsedAt    *time.Time     `json:"used_at,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

// GetTwoFAByUserId 根据用户ID获取2FA设置
func GetTwoFAByUserId(db *gorm.DB, userId int) (*TwoFA, error) {
	if userId == 0 {
		return nil, errors.New("用户ID不能为空")
	}

	var twoFA TwoFA
	err := db.Where("user_id = ?", userId).First(&twoFA).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // 返回nil表示未设置2FA
		}
		return nil, err
	}

	return &twoFA, nil
}

// IsTwoFAEnabled 检查用户是否启用了2FA
func IsTwoFAEnabled(db *gorm.DB, userId int) bool {
	twoFA, err := GetTwoFAByUserId(db, userId)
	if err != nil || twoFA == nil {
		return false
	}
	return twoFA.IsEnabled
}

// CreateTwoFA 创建2FA设置
func (t *TwoFA) Create(db *gorm.DB) error {
	// 检查用户是否已存在2FA设置
	existing, err := GetTwoFAByUserId(db, t.UserId)
	if err != nil {
		return err
	}
	if existing != nil {
		return errors.New("用户已存在2FA设置")
	}

	// 验证用户存在
	var user User
	if err := db.First(&user, t.UserId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("用户不存在")
		}
		return err
	}

	return db.Create(t).Error
}

// Update 更新2FA设置
func (t *TwoFA) Update(db *gorm.DB) error {
	if t.Id == 0 {
		return errors.New("2FA记录ID不能为空")
	}
	return db.Save(t).Error
}

// Delete 删除2FA设置
func (t *TwoFA) Delete(db *gorm.DB) error {
	if t.Id == 0 {
		return errors.New("2FA记录ID不能为空")
	}

	// 使用事务确保原子性
	return db.Transaction(func(tx *gorm.DB) error {
		// 同时删除相关的备用码记录（硬删除）
		if err := tx.Unscoped().Where("user_id = ?", t.UserId).Delete(&TwoFABackupCode{}).Error; err != nil {
			return err
		}

		// 硬删除2FA记录
		return tx.Unscoped().Delete(t).Error
	})
}

// ResetFailedAttempts 重置失败尝试次数
func (t *TwoFA) ResetFailedAttempts(db *gorm.DB) error {
	t.FailedAttempts = 0
	t.LockedUntil = nil
	return t.Update(db)
}

// IncrementFailedAttempts 增加失败尝试次数
func (t *TwoFA) IncrementFailedAttempts(db *gorm.DB) error {
	t.FailedAttempts++

	// 检查是否需要锁定
	if t.FailedAttempts >= twoFAMaxFailedAttempts {
		lockUntil := time.Now().Add(twoFALockoutSeconds * time.Second)
		t.LockedUntil = &lockUntil
	}

	return t.Update(db)
}

// IsLocked 检查账户是否被锁定
func (t *TwoFA) IsLocked() bool {
	if t.LockedUntil == nil {
		return false
	}
	return time.Now().Before(*t.LockedUntil)
}

// CreateBackupCodes 创建备用码
func CreateBackupCodes(db *gorm.DB, userId int, codes []string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// 先删除现有的备用码
		if err := tx.Where("user_id = ?", userId).Delete(&TwoFABackupCode{}).Error; err != nil {
			return err
		}

		// 创建新的备用码记录
		for _, code := range codes {
			plain := normalizeTwoFABackupCode(code)
			if plain == "" {
				return errors.New("备用码不能为空")
			}
			hashedCode := HashPassword(plain)
			if hashedCode == "" {
				return errors.New("备用码哈希失败")
			}

			backupCode := TwoFABackupCode{
				UserId:   userId,
				CodeHash: hashedCode,
				IsUsed:   false,
			}

			if err := tx.Create(&backupCode).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// ValidateBackupCode 验证并使用备用码
func ValidateBackupCode(db *gorm.DB, userId int, code string) (bool, error) {
	if !isValidTwoFABackupCodeInput(code) {
		return false, errors.New("验证码或备用码不正确")
	}

	normalizedCode := normalizeTwoFABackupCode(code)

	// 查找未使用的备用码
	var backupCodes []TwoFABackupCode
	if err := db.Where("user_id = ? AND is_used = false", userId).Find(&backupCodes).Error; err != nil {
		return false, err
	}

	// 验证备用码
	for _, bc := range backupCodes {
		if twoFABackupCodeMatchesPlain(normalizedCode, bc.CodeHash) {
			// 标记为已使用
			now := time.Now()
			bc.IsUsed = true
			bc.UsedAt = &now

			if err := db.Save(&bc).Error; err != nil {
				return false, err
			}

			return true, nil
		}
	}

	return false, nil
}

// GetUnusedBackupCodeCount 获取未使用的备用码数量
func GetUnusedBackupCodeCount(db *gorm.DB, userId int) (int, error) {
	var count int64
	err := db.Model(&TwoFABackupCode{}).Where("user_id = ? AND is_used = false", userId).Count(&count).Error
	return int(count), err
}

// DisableTwoFA 禁用用户的2FA
func DisableTwoFA(db *gorm.DB, userId int) error {
	twoFA, err := GetTwoFAByUserId(db, userId)
	if err != nil {
		return err
	}
	if twoFA == nil {
		return ErrTwoFANotEnabled
	}

	// 删除2FA设置和备用码
	return twoFA.Delete(db)
}

// EnableTwoFA 启用2FA
func (t *TwoFA) Enable(db *gorm.DB) error {
	t.IsEnabled = true
	t.FailedAttempts = 0
	t.LockedUntil = nil
	return t.Update(db)
}

// ValidateTOTPAndUpdateUsage 验证TOTP并更新使用记录
func (t *TwoFA) ValidateTOTPAndUpdateUsage(db *gorm.DB, code string) (bool, error) {
	// 检查是否被锁定
	if t.IsLocked() {
		return false, fmt.Errorf("账户已被锁定，请在%v后重试", t.LockedUntil.Format("2006-01-02 15:04:05"))
	}

	// 验证TOTP码（pkg/utils/totp.ValidateTOTP）
	if !utils.ValidateTOTP(code, t.Secret) {
		// 增加失败次数
		if err := t.IncrementFailedAttempts(db); err != nil {
			logger.Warn("twofa.increment_failed", zap.Error(err))
		}
		return false, nil
	}

	// 验证成功，重置失败次数并更新最后使用时间
	now := time.Now()
	t.FailedAttempts = 0
	t.LockedUntil = nil
	t.LastUsedAt = &now

	if err := t.Update(db); err != nil {
		logger.Warn("twofa.update_usage", zap.Error(err))
	}

	return true, nil
}

// ValidateBackupCodeAndUpdateUsage 验证备用码并更新使用记录
func (t *TwoFA) ValidateBackupCodeAndUpdateUsage(db *gorm.DB, code string) (bool, error) {
	// 检查是否被锁定
	if t.IsLocked() {
		return false, fmt.Errorf("账户已被锁定，请在%v后重试", t.LockedUntil.Format("2006-01-02 15:04:05"))
	}

	// 验证备用码
	valid, err := ValidateBackupCode(db, t.UserId, code)
	if err != nil {
		return false, err
	}

	if !valid {
		// 增加失败次数
		if err := t.IncrementFailedAttempts(db); err != nil {
			logger.Warn("twofa.increment_failed", zap.Error(err))
		}
		return false, nil
	}

	// 验证成功，重置失败次数并更新最后使用时间
	now := time.Now()
	t.FailedAttempts = 0
	t.LockedUntil = nil
	t.LastUsedAt = &now

	if err := t.Update(db); err != nil {
		logger.Warn("twofa.update_usage", zap.Error(err))
	}

	return true, nil
}

// GetTwoFAStats 获取2FA统计信息（管理员使用）
func GetTwoFAStats(db *gorm.DB) (map[string]interface{}, error) {
	var totalUsers, enabledUsers int64

	// 总用户数
	if err := db.Model(&User{}).Count(&totalUsers).Error; err != nil {
		return nil, err
	}

	// 启用2FA的用户数
	if err := db.Model(&TwoFA{}).Where("is_enabled = true").Count(&enabledUsers).Error; err != nil {
		return nil, err
	}

	enabledRate := float64(0)
	if totalUsers > 0 {
		enabledRate = float64(enabledUsers) / float64(totalUsers) * 100
	}

	return map[string]interface{}{
		"total_users":   totalUsers,
		"enabled_users": enabledUsers,
		"enabled_rate":  fmt.Sprintf("%.1f%%", enabledRate),
	}, nil
}
