package models

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/cmd/bootstrap"
	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/pkg/constants"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	RoleSuperAdmin = "superadmin" // 超级管理员
	RoleAdmin      = "admin"      // 管理员
	RoleUser       = "user"       // 普通用户
)

const (
	UserSourceSystem = "SYSTEM"
	UserSourceAdmin  = "ADMIN"
	UserSourceWechat = "WECHAT"
	UserSourceGithub = "GITHUB"
)

const (
	UserStatusActive              = "active"
	UserStatusPendingVerification = "pending_verification"
	UserStatusSuspended           = "suspended"
	UserStatusBanned              = "banned"
)

// UserStatusAllowsLogin 是否允许登录（仅正常态）。
func UserStatusAllowsLogin(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), UserStatusActive)
}

type SendEmailVerifyEmail struct {
	Email     string `json:"email"`
	ClientIp  string `json:"clientIp"`
	UserAgent string `json:"userAgent"`
}

type UserBasicInfoUpdate struct {
	FatherCallName string `json:"fatherCallName"`
	MotherCallName string `json:"motherCallName"`
	WifiName       string `json:"wifiName"`
	WifiPassword   string `json:"wifiPassword"`
}

type LoginForm struct {
	Email         string `json:"email" comment:"Email address"`
	Password      string `json:"password,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
	Remember      bool   `json:"remember,omitempty"`
	AuthToken     string `json:"token,omitempty"`
	TwoFactorCode string `json:"twoFactorCode,omitempty"` // 两步验证码
	CaptchaID     string `json:"captchaId,omitempty"`     // 图形验证码ID
	CaptchaCode   string `json:"captchaCode,omitempty"`   // 图形验证码
	CaptchaType   string `json:"captchaType,omitempty"`   // 验证码类型: image/click
	CaptchaData   string `json:"captchaData,omitempty"`   // 点击验证码坐标数据(JSON)
}

type EmailOperatorForm struct {
	UserName    string `json:"userName"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email" comment:"Email address"`
	Code        string `json:"code"`
	Password    string `json:"password"`
	AuthToken   bool   `json:"AuthToken,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	CaptchaID   string `json:"captchaId,omitempty"`
	CaptchaCode string `json:"captchaCode,omitempty"`
	CaptchaType string `json:"captchaType,omitempty"`
	CaptchaData string `json:"captchaData,omitempty"`
}

type RegisterUserForm struct {
	Email            string `json:"email" binding:"required"`
	Password         string `json:"password" binding:"required"`
	DisplayName      string `json:"displayName"`
	FirstName        string `json:"firstName"`
	LastName         string `json:"lastName"`
	Locale           string `json:"locale"`
	Timezone         string `json:"timezone"`
	Source           string `json:"source"`
	CaptchaID        string `json:"captchaId"`
	CaptchaCode      string `json:"captchaCode"`
	CaptchaType      string `json:"captchaType"`
	CaptchaData      string `json:"captchaData"`
	MouseTrack       string `json:"mouseTrack"`
	FormFillTime     int64  `json:"formFillTime"`
	KeystrokePattern string `json:"keystrokePattern"`
}

type ChangePasswordForm struct {
	Password string `json:"password" binding:"required"`
}

type ResetPasswordForm struct {
	Email string `json:"email" binding:"required"`
}

type ResetPasswordDoneForm struct {
	Password string `json:"password" binding:"required"`
	Email    string `json:"email" binding:"required"`
	Token    string `json:"token" binding:"required"`
}

type UpdateUserRequest struct {
	Email       string `form:"email" json:"email"`
	Phone       string `form:"phone" json:"phone"`
	FirstName   string `form:"firstName" json:"firstName"`
	LastName    string `form:"lastName" json:"lastName"`
	DisplayName string `form:"displayName" json:"displayName"`
	Locale      string `form:"locale" json:"locale"`
	Timezone    string `form:"timezone" json:"timezone"`
	Gender      string `form:"gender" json:"gender"`
	City        string `form:"city" json:"city"`
	Region      string `form:"region" json:"region"`
	Extra       string `form:"extra" json:"extra"`
	Avatar      string `form:"avatar" json:"avatar"`
}

type User struct {
	BaseModel
	Email                      string     `json:"email" gorm:"size:128;uniqueIndex"`
	WechatOpenID               string     `json:"wechatOpenId,omitempty" gorm:"size:128;index"`
	WechatUnionID              string     `json:"wechatUnionId,omitempty" gorm:"size:128;index"`
	GithubID                   string     `json:"githubId,omitempty" gorm:"size:64;index"`
	GithubLogin                string     `json:"githubLogin,omitempty" gorm:"size:128;index"`
	Password                   string     `json:"-" gorm:"size:128"`
	Phone                      string     `json:"phone,omitempty" gorm:"size:64;index"`
	FirstName                  string     `json:"firstName,omitempty" gorm:"size:128"`
	LastName                   string     `json:"lastName,omitempty" gorm:"size:128"`
	DisplayName                string     `json:"displayName,omitempty" gorm:"size:128"`
	Status                     string     `json:"status" gorm:"size:32;index;default:'active';comment:Account status"`
	LastLogin                  *time.Time `json:"lastLogin,omitempty"`
	LastLoginIP                string     `json:"-" gorm:"size:128"`
	Source                     string     `json:"source" gorm:"size:64;index"`
	Locale                     string     `json:"locale,omitempty" gorm:"size:20"`
	Timezone                   string     `json:"timezone,omitempty" gorm:"size:200"`
	AuthToken                  string     `json:"token,omitempty" gorm:"-"`
	Avatar                     string     `json:"avatar,omitempty"`
	Gender                     string     `json:"gender,omitempty"`
	City                       string     `json:"city,omitempty"`
	Region                     string     `json:"region,omitempty"`
	EmailNotifications         bool       `json:"emailNotifications"`                           // 邮件通知
	EmailVerified              bool       `json:"emailVerified" gorm:"default:false"`           // 邮箱已验证
	PhoneVerified              bool       `json:"phoneVerified" gorm:"default:false"`           // 手机已验证
	TwoFactorEnabled           bool       `json:"twoFactorEnabled" gorm:"default:false"`        // 双因素认证
	TwoFactorSecret            string     `json:"-" gorm:"size:128"`                            // 双因素认证密钥
	EmailVerifyToken           string     `json:"-" gorm:"size:128"`                            // 邮箱验证令牌
	PhoneVerifyToken           string     `json:"-" gorm:"size:128"`                            // 手机验证令牌
	PasswordResetToken         string     `json:"-" gorm:"size:128"`                            // 密码重置令牌
	PasswordResetExpires       *time.Time `json:"-"`                                            // 密码重置过期时间
	EmailVerifyExpires         *time.Time `json:"-"`                                            // 邮箱验证过期时间
	LoginCount                 int        `json:"loginCount" gorm:"default:0"`                  // 登录次数
	RemainQuota                int        `json:"remainQuota" gorm:"default:0"`                 // 用户级剩余额度（与 new-api 用户配额概念对齐；实际扣减仍以凭证为主时可作展示/预留）
	UsedQuota                  int        `json:"usedQuota" gorm:"default:0"`                   // 用户级已用额度
	UnlimitedQuota             bool       `json:"unlimitedQuota" gorm:"default:false"`          // 用户级无限额度标记
	LastPasswordChange         *time.Time `json:"lastPasswordChange,omitempty"`                 // 最后密码修改时间
	ProfileComplete            int        `json:"profileComplete" gorm:"default:0"`             // 资料完整度百分比
	Role                       string     `json:"role,omitempty" gorm:"size:50;default:'user'"` // 用户角色
	DefaultOrgID               uint       `json:"defaultOrgId" gorm:"index;not null;default:0;comment:default organization id"`
	AccountDeletionRequestedAt *time.Time `json:"accountDeletionRequestedAt,omitempty"`
	AccountDeletionEffectiveAt *time.Time `json:"accountDeletionEffectiveAt,omitempty" gorm:"index"`
}

func (u *User) TableName() string {
	return constants.USER_TABLE_NAME
}

// Login Handle-User-Login
func Login(c *gin.Context, user *User) {
	db := c.MustGet(constants.DbField).(*gorm.DB)
	_ = EnsurePersonalOrg(db, user)
	err := SetLastLogin(db, user, c.ClientIP())
	if err != nil {
		logger.Error("user.login", zap.Error(err))
		response.AbortWithJSONError(c, http.StatusInternalServerError, err)
		return
	}

	// Increase login count
	err = IncrementLoginCount(db, user)
	if err != nil {
		logger.Error("user.login", zap.Error(err))
		response.AbortWithJSONError(c, http.StatusInternalServerError, err)
		return
	}

	// Update profile completeness
	err = UpdateProfileComplete(db, user)
	if err != nil {
		logger.Error("user.login", zap.Error(err))
		response.AbortWithJSONError(c, http.StatusInternalServerError, err)
		return
	}

	session := sessions.Default(c)
	session.Set(constants.UserField, user.ID)
	session.Save()
	utils.Sig().Emit(constants.SigUserLogin, user, db)
}

func Logout(c *gin.Context, user *User) {
	c.Set(constants.UserField, nil)
	session := sessions.Default(c)
	session.Delete(constants.UserField)
	session.Save()
	utils.Sig().Emit(constants.SigUserLogout, user, c)
}

// AuthRequired 依赖 CurrentUser：其中已包含 session cookie 与 Authorization Bearer access JWT 的解析与装库（见 CurrentUser 末尾 jwtauth.ParseAccessToken）。
func AuthRequired(c *gin.Context) {
	u := CurrentUser(c)
	if u == nil {
		if config.GlobalConfig == nil {
			response.AbortWithJSONError(c, http.StatusInternalServerError, errors.New("server configuration not initialized"))
			return
		}
		response.AbortWithJSONError(c, http.StatusUnauthorized, errors.New("authorization required"))
		return
	}

	if AccountDeletionPending(u) && !authPathExemptWhileAccountDeletionPending(c) {
		effective := ""
		if u.AccountDeletionEffectiveAt != nil {
			effective = u.AccountDeletionEffectiveAt.UTC().Format(time.RFC3339)
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code": http.StatusForbidden,
			"msg":  "账号正在注销冷静期内，暂时无法使用 SoulNexus 产品功能。请使用「撤销注销」完成验证后恢复，或等待冷静期结束后账号将被永久注销。",
			"data": gin.H{
				"accountDeletionPending":     true,
				"accountDeletionEffectiveAt": effective,
			},
		})
		return
	}
	c.Next()
}

// AdminRequired 须在 AuthRequired 之后注册；仅管理员（admin / superadmin）可继续。
func AdminRequired(c *gin.Context) {
	u := CurrentUser(c)
	if u == nil {
		response.AbortWithJSONError(c, http.StatusUnauthorized, errors.New("authorization required"))
		c.Abort()
		return
	}
	if !u.IsAdmin() {
		response.FailWithCode(c, 403, "需要管理员权限", nil)
		c.Abort()
		return
	}
	c.Next()
}

// authPathExemptWhileAccountDeletionPending 冷静期内仍允许访问的认证接口（获取信息、撤销注销）。
func authPathExemptWhileAccountDeletionPending(c *gin.Context) bool {
	method := c.Request.Method
	path := c.Request.URL.Path
	if method == http.MethodGet && (strings.Contains(path, "/auth/info") || strings.Contains(path, "/auth/me")) {
		return true
	}
	if method == http.MethodGet && strings.Contains(path, "/account-deletion/eligibility") {
		return true
	}
	if method == http.MethodPost && strings.HasSuffix(path, "/account-deletion/cancel") {
		return true
	}
	return false
}

// CurrentUser 解析顺序：① Gin 已缓存的 User ② session 中的 user id ③ Header（默认 Authorization）或 ?token= 中的 access JWT → jwtauth.ParseAccessToken → DB 加载并写入缓存。
func CurrentUser(c *gin.Context) *User {
	if cachedObj, exists := c.Get(constants.UserField); exists && cachedObj != nil {
		return cachedObj.(*User)
	}

	session := sessions.Default(c)
	userIDVal := session.Get(constants.UserField)
	if userIDVal != nil {
		uid, ok := userIDVal.(uint)
		if !ok {
			return nil
		}
		db := c.MustGet(constants.DbField).(*gorm.DB)
		user, err := GetUserByUID(db, uid)
		if err != nil {
			return nil
		}
		c.Set(constants.UserField, user)
		return user
	}

	if config.GlobalConfig == nil {
		return nil
	}
	raw := strings.TrimSpace(c.GetHeader(config.GlobalConfig.Auth.Header))
	if raw == "" {
		raw = strings.TrimSpace(c.Query("Authorization"))
	}
	raw = strings.TrimSpace(strings.TrimPrefix(raw, constants.AUTHORIZATION_PREFIX))
	if raw == "" {
		raw = strings.TrimSpace(c.Query("token"))
	}
	if raw == "" {
		return nil
	}
	var payload *utils.AccessPayload
	var err error
	if bootstrap.GlobalKeyManager != nil {
		payload, err = utils.ParseAccessTokenWithKey(raw, bootstrap.GlobalKeyManager)
		if err != nil {
			// Backward-compat: allow HS256 tokens issued before JWKS rollout.
			payload, err = utils.ParseAccessToken(raw, config.GlobalConfig.Auth.JWTSigningKey())
		}
	} else {
		payload, err = utils.ParseAccessToken(raw, config.GlobalConfig.Auth.JWTSigningKey())
	}
	if err != nil || payload == nil {
		return nil
	}
	db := c.MustGet(constants.DbField).(*gorm.DB)
	var fresh User
	// 使用 GORM 默认软删作用域：未删除行为 deleted_at IS NULL，勿与 is_deleted 整型常量混用。
	if err := db.Where("id = ?", payload.UserID).First(&fresh).Error; err != nil {
		return nil
	}
	if payload.Email != "" && !strings.EqualFold(strings.TrimSpace(payload.Email), strings.TrimSpace(fresh.Email)) {
		return nil
	}
	c.Set(constants.UserField, &fresh)
	return &fresh
}

func CheckPassword(user *User, password string) bool {
	if user.Password == "" {
		return false
	}
	return user.Password == HashPassword(password)
}

func SetPassword(db *gorm.DB, user *User, password string) (err error) {
	p := HashPassword(password)
	err = UpdateUserFields(db, user, map[string]any{
		"Password": p,
	})
	if err != nil {
		return
	}
	user.Password = p
	return
}

func HashPassword(password string) string {
	if password == "" {
		return ""
	}
	if strings.HasPrefix(password, "sha256$") {
		return password
	}
	hashVal := sha256.Sum256([]byte(password))
	return fmt.Sprintf("sha256$%x", hashVal)
}

// VerifyEncryptedPassword 验证加密密码
// 前端发送格式：passwordHash:encryptedHash:salt:timestamp
// passwordHash = SHA256(原始密码) - 用于验证密码正确性
// encryptedHash = SHA256(原始密码 + salt + timestamp) - 用于防重放
// 后端验证：
// 1. passwordHash 与存储的密码哈希匹配（去掉 sha256$ 前缀）
// 2. 时间戳在有效期内（5分钟）
// 3. 验证 salt 是否在缓存中（防重放）
func VerifyEncryptedPassword(encryptedPassword, storedPasswordHash string) bool {
	// 解析加密密码格式：passwordHash:encryptedHash:salt:timestamp
	parts := strings.Split(encryptedPassword, ":")
	if len(parts) != 4 {
		return false
	}

	passwordHash := parts[0]
	encryptedHash := parts[1]
	salt := parts[2]
	timestampStr := parts[3]

	// 验证时间戳（5分钟内有效）
	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return false
	}

	// 如果时间戳是毫秒级（13位数字），转换为秒级
	if timestamp > 9999999999 { // 大于10位数字，说明是毫秒时间戳
		timestamp = timestamp / 1000
	}

	now := time.Now().Unix()
	maxAge := int64(300) // 5分钟
	if now-timestamp > maxAge {
		logger.Info(fmt.Sprintf("DEBUG: Timestamp expired. now=%d, timestamp=%d, diff=%d\n",
			now, timestamp, now-timestamp))
		return false
	}
	storedHash := strings.TrimPrefix(storedPasswordHash, "sha256$")

	if passwordHash != storedHash {
		logger.Info(fmt.Sprintf("DEBUG: Password hash mismatch. Expected: %s, Got: %s\n",
			storedHash, passwordHash))
		return false
	}
	originalTimestamp, _ := strconv.ParseInt(timestampStr, 10, 64)
	hashInput := fmt.Sprintf("%s%s%d", passwordHash, salt, originalTimestamp)
	hashVal := sha256.Sum256([]byte(hashInput))
	expectedHash := fmt.Sprintf("%x", hashVal)

	isValid := encryptedHash == expectedHash
	if !isValid {
		fmt.Printf("DEBUG: Hash verification failed. Expected: %s, Got: %s\n",
			expectedHash, encryptedHash)
	}

	return isValid
}

func GetUserByUID(db *gorm.DB, userID uint) (*User, error) {
	var val User
	if err := db.Where("id = ? AND status = ?", userID, UserStatusActive).First(&val).Error; err != nil {
		return nil, err
	}
	return &val, nil
}

func GetUserByEmail(db *gorm.DB, email string) (user *User, err error) {
	var val User
	result := db.Table(constants.USER_TABLE_NAME).Where("email", strings.ToLower(email)).Take(&val)

	if result.Error != nil {
		return nil, result.Error
	}
	return &val, nil
}

func IsExistsByEmail(db *gorm.DB, email string) bool {
	_, err := GetUserByEmail(db, email)
	return err == nil
}

func CreateUserByEmail(db *gorm.DB, username, display, email, password string) (*User, error) {
	return CreateUserByEmailWithMeta(db, username, display, email, password, UserSourceSystem, UserStatusActive)
}

// CreateUserByEmailWithMeta 创建用户并写入来源与账号状态。
func CreateUserByEmailWithMeta(db *gorm.DB, username, display, email, password, source, status string) (*User, error) {
	// Properly handle Unicode characters (including Chinese)
	var firstName, lastName string
	if username != "" {
		runes := []rune(username)
		if len(runes) > 0 {
			firstName = string(runes[0]) // First character (rune) as FirstName
		}
		if len(runes) > 1 {
			lastName = string(runes[1:]) // Remaining characters as LastName
		}
	}

	user := User{
		BaseModel:          BaseModel{},
		DisplayName:        display,
		FirstName:          firstName,
		LastName:           lastName,
		Email:              email,
		Password:           HashPassword(password),
		Status:             status,
		Source:             source,
		EmailNotifications: true,
		Role:               RoleUser, // Explicitly set default role
	}
	operator := strings.ToLower(strings.TrimSpace(email))
	if operator == "" {
		operator = "system"
	}
	user.SetCreateInfo(operator)
	user.ID = utils.GenUintID()
	result := db.Create(&user)
	return &user, result.Error
}

func CreateUser(db *gorm.DB, email, password string) (*User, error) {
	return CreateUserWithMeta(db, email, password, UserSourceSystem, UserStatusActive)
}

// CreateUserWithMeta 使用邮箱+密码创建用户（如网页注册），可指定来源与状态。
func CreateUserWithMeta(db *gorm.DB, email, password, source, status string) (*User, error) {
	user := User{
		BaseModel: BaseModel{},
		Email:     email,
		Password:  HashPassword(password),
		Status:    status,
		Source:    source,
		Role:      RoleUser, // Explicitly set default role
	}
	operator := strings.ToLower(strings.TrimSpace(email))
	if operator == "" {
		operator = "system"
	}
	user.SetCreateInfo(operator)
	user.ID = utils.GenUintID()

	result := db.Create(&user)
	return &user, result.Error
}
func UpdateUserFields(db *gorm.DB, user *User, vals map[string]any) error {
	if _, ok := vals["update_by"]; !ok {
		vals["update_by"] = "system"
	}
	result := db.Model(user).Updates(vals)
	return result.Error
}

func SetLastLogin(db *gorm.DB, user *User, lastIp string) error {
	now := time.Now().Truncate(1 * time.Second)
	vals := map[string]any{
		"LastLoginIP": lastIp,
		"LastLogin":   &now,
	}
	user.LastLogin = &now
	user.LastLoginIP = lastIp
	result := db.Model(user).Updates(vals)
	return result.Error
}

func CheckUserAllowLogin(db *gorm.DB, user *User) error {
	if user == nil {
		return errors.New("user not allow login")
	}
	if !UserStatusAllowsLogin(user.Status) {
		return errors.New("user not allow login")
	}

	// Role validation - ensure user has a valid role
	if err := ValidateUserRole(user); err != nil {
		return err
	}

	return nil
}

// ValidateUserRole validates that the user has a valid role
func ValidateUserRole(user *User) error {
	if user.Role == "" {
		return errors.New("user role is not set")
	}

	// Check if role is one of the valid roles
	validRoles := []string{RoleSuperAdmin, RoleAdmin, RoleUser}
	for _, validRole := range validRoles {
		if user.Role == validRole {
			return nil
		}
	}

	return fmt.Errorf("invalid user role: %s", user.Role)
}

func InTimezone(c *gin.Context, timezone string) {
	tz, err := time.LoadLocation(timezone)
	if err != nil {
		return
	}
	c.Set(constants.TzField, tz)

	session := sessions.Default(c)
	session.Set(constants.TzField, timezone)
	session.Save()
}

func UpdateUser(db *gorm.DB, user *User, vals map[string]any) error {
	if _, ok := vals["update_by"]; !ok {
		vals["update_by"] = "system"
	}
	return db.Model(user).Updates(vals).Error
}

// ChangePassword 修改密码
func ChangePassword(db *gorm.DB, user *User, oldPassword, newPassword string) error {
	// 验证旧密码
	if !CheckPassword(user, oldPassword) {
		return errors.New("旧密码不正确")
	}

	// 设置新密码
	err := SetPassword(db, user, newPassword)
	if err != nil {
		return err
	}

	// 更新最后密码修改时间
	now := time.Now()
	err = UpdateUserFields(db, user, map[string]any{
		"LastPasswordChange": &now,
	})
	if err != nil {
		return err
	}

	user.LastPasswordChange = &now
	return nil
}

// ResetPassword 重置密码
func ResetPassword(db *gorm.DB, user *User, newPassword string) error {
	// 设置新密码
	err := SetPassword(db, user, newPassword)
	if err != nil {
		return err
	}

	// 清除密码重置令牌
	err = UpdateUserFields(db, user, map[string]any{
		"PasswordResetToken":   "",
		"PasswordResetExpires": nil,
		"LastPasswordChange":   time.Now(),
	})
	if err != nil {
		return err
	}

	user.PasswordResetToken = ""
	user.PasswordResetExpires = nil
	now := time.Now()
	user.LastPasswordChange = &now
	return nil
}

// GeneratePasswordResetToken 生成密码重置令牌
func GeneratePasswordResetToken(db *gorm.DB, user *User) (string, error) {
	token := utils.RandString(32)
	expires := time.Now().Add(24 * time.Hour) // 24小时过期

	err := UpdateUserFields(db, user, map[string]any{
		"PasswordResetToken":   token,
		"PasswordResetExpires": &expires,
	})
	if err != nil {
		return "", err
	}

	user.PasswordResetToken = token
	user.PasswordResetExpires = &expires
	return token, nil
}

// VerifyPasswordResetToken 验证密码重置令牌
func VerifyPasswordResetToken(db *gorm.DB, token string) (*User, error) {
	var user User
	err := db.Where("password_reset_token = ? AND password_reset_expires > ?", token, time.Now()).First(&user).Error
	if err != nil {
		return nil, errors.New("无效或过期的重置令牌")
	}
	return &user, nil
}

// GenerateEmailVerifyToken 生成邮箱登录用 6 位数字验证码（写入 email_verify_token，短期有效）。
func GenerateEmailVerifyToken(db *gorm.DB, user *User) (string, error) {
	token := utils.RandNumberText(6)
	expires := time.Now().Add(10 * time.Minute)

	err := UpdateUserFields(db, user, map[string]any{
		"EmailVerifyToken":   token,
		"EmailVerifyExpires": &expires,
	})
	if err != nil {
		return "", err
	}

	user.EmailVerifyToken = token
	user.EmailVerifyExpires = &expires
	return token, nil
}

func normalizeEmailLoginCode(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// VerifyEmailLoginCode 校验邮箱与邮件中的数字验证码，成功后标记邮箱已验证并清除令牌。
func VerifyEmailLoginCode(db *gorm.DB, email, code string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	code = normalizeEmailLoginCode(code)
	if email == "" || len(code) != 6 {
		return nil, errors.New("验证码无效或已过期")
	}
	var user User
	err := db.Where("email = ? AND email_verify_token = ? AND email_verify_expires > ?", email, code, time.Now()).First(&user).Error
	if err != nil {
		return nil, errors.New("验证码无效或已过期")
	}

	err = UpdateUserFields(db, &user, map[string]any{
		"EmailVerified":      true,
		"EmailVerifyToken":   "",
		"EmailVerifyExpires": nil,
	})
	if err != nil {
		return nil, err
	}

	user.EmailVerified = true
	user.EmailVerifyToken = ""
	user.EmailVerifyExpires = nil
	return &user, nil
}

// GeneratePhoneVerifyToken 生成手机验证令牌
func GeneratePhoneVerifyToken(db *gorm.DB, user *User) (string, error) {
	token := utils.RandNumberText(6) // 6位数字验证码
	err := UpdateUserFields(db, user, map[string]any{
		"PhoneVerifyToken": token,
	})
	if err != nil {
		return "", err
	}

	user.PhoneVerifyToken = token
	return token, nil
}

// VerifyPhone 验证手机
func VerifyPhone(db *gorm.DB, user *User, token string) error {
	if user.PhoneVerifyToken != token {
		return errors.New("验证码不正确")
	}

	// 更新手机验证状态
	err := UpdateUserFields(db, user, map[string]any{
		"PhoneVerified":    true,
		"PhoneVerifyToken": "",
	})
	if err != nil {
		return err
	}

	user.PhoneVerified = true
	user.PhoneVerifyToken = ""
	return nil
}

// UpdateNotificationSettings 更新通知设置
func UpdateNotificationSettings(db *gorm.DB, user *User, settings map[string]bool) error {
	vals := make(map[string]any)

	if emailNotif, ok := settings["emailNotifications"]; ok {
		vals["email_notifications"] = emailNotif
	}
	if len(vals) == 0 {
		return nil
	}

	err := UpdateUserFields(db, user, vals)
	if err != nil {
		return err
	}

	// 更新用户对象
	if emailNotif, ok := settings["emailNotifications"]; ok {
		user.EmailNotifications = emailNotif
	}
	return nil
}

// UpdatePreferences 更新用户偏好设置
// 只处理实际使用的字段：timezone 和 locale
func UpdatePreferences(db *gorm.DB, user *User, preferences map[string]string) error {
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

	err := UpdateUserFields(db, user, vals)
	if err != nil {
		return err
	}

	// 更新用户对象
	if timezone, ok := preferences["timezone"]; ok {
		user.Timezone = timezone
	}
	if locale, ok := preferences["locale"]; ok {
		user.Locale = locale
	}

	return nil
}

// CalculateProfileComplete 计算资料完整度
func CalculateProfileComplete(user *User) int {
	complete := 0
	total := 0

	// 基本信息 (40%)
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

	// 联系方式 (30%)
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

	// 地址信息 (20%)
	total += 2
	if user.City != "" {
		complete++
	}
	if user.Region != "" {
		complete++
	}

	// 偏好设置 (10%)
	total += 1
	if user.Timezone != "" {
		complete++
	}

	// 第三方认证 (20%)
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

// UpdateProfileComplete 更新资料完整度
func UpdateProfileComplete(db *gorm.DB, user *User) error {
	complete := CalculateProfileComplete(user)
	err := UpdateUserFields(db, user, map[string]any{
		"ProfileComplete": complete,
	})
	if err != nil {
		return err
	}
	user.ProfileComplete = complete
	return nil
}

// IncrementLoginCount 增加登录次数
func IncrementLoginCount(db *gorm.DB, user *User) error {
	err := UpdateUserFields(db, user, map[string]any{
		"LoginCount": user.LoginCount + 1,
	})
	if err != nil {
		return err
	}

	user.LoginCount++
	return nil
}

// IsAdmin 检查是否为管理员（基于角色）
func (u *User) IsAdmin() bool {
	return u.Role == RoleSuperAdmin || u.Role == RoleAdmin
}

// IsSuperAdmin 检查是否为超级管理员
func (u *User) IsSuperAdmin() bool {
	return u.Role == RoleSuperAdmin
}

// DefaultAccountDeletionCooldown 默认冷静期 72 小时（3 天），可通过环境变量 ACCOUNT_DELETION_COOLDOWN_HOURS 覆盖。
func DefaultAccountDeletionCooldown() time.Duration {
	h := 72
	if v := strings.TrimSpace(os.Getenv("ACCOUNT_DELETION_COOLDOWN_HOURS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 720 {
			h = n
		}
	}
	return time.Duration(h) * time.Hour
}

// AccountDeletionPending 是否在冷静期内（已申请、尚未到期、账号仍为正常态）。
func AccountDeletionPending(user *User) bool {
	if user == nil || user.AccountDeletionEffectiveAt == nil {
		return false
	}
	return time.Now().Before(*user.AccountDeletionEffectiveAt) && user.IsSoftDeleted()
}

// ThirdPartyBindings 是否仍绑定 GitHub / 微信。
func ThirdPartyBindings(user *User) (github bool, wechat bool) {
	if user == nil {
		return false, false
	}
	github = strings.TrimSpace(user.GithubID) != ""
	wechat = strings.TrimSpace(user.WechatOpenID) != ""
	return github, wechat
}

// HasRecentSuspiciousLogins 近期是否存在标记为可疑的成功登录（用于注销前风控）。
func HasRecentSuspiciousLogins(db *gorm.DB, userID uint, lookback time.Duration) (bool, error) {
	if userID == 0 {
		return false, nil
	}
	since := time.Now().Add(-lookback)
	var n int64
	err := db.Model(&LoginHistory{}).
		Where("user_id = ? AND is_suspicious = ? AND success = ? AND created_at >= ?", userID, true, true, since).
		Count(&n).Error
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// AccountDeletionEligibilityReasons 返回不满足注销申请条件的原因（空切片表示通过基础校验）。
func AccountDeletionEligibilityReasons(db *gorm.DB, user *User, accountLocked bool, remoteLoginRisk bool, recentSuspicious bool) []string {
	var reasons []string
	if user == nil {
		return []string{"用户不存在"}
	}
	if err := CheckUserAllowLogin(db, user); err != nil {
		reasons = append(reasons, "账号状态不允许（未激活、已封禁或角色异常）")
	}
	if user.Role == RoleSuperAdmin || user.Role == RoleAdmin {
		reasons = append(reasons, "管理员账号不支持自助注销")
	}
	if accountLocked {
		reasons = append(reasons, "账号因多次失败登录处于锁定中")
	}
	if remoteLoginRisk {
		reasons = append(reasons, "当前网络环境存在异地登录风险，请稍后在常用环境再试")
	}
	if recentSuspicious {
		reasons = append(reasons, "近期存在异常登录记录")
	}
	gh, wx := ThirdPartyBindings(user)
	if gh {
		reasons = append(reasons, "仍绑定 GitHub，请先解绑")
	}
	if wx {
		reasons = append(reasons, "仍绑定微信，请先解绑")
	}
	return reasons
}

// ScheduleAccountDeletion 进入冷静期。
func ScheduleAccountDeletion(db *gorm.DB, userID uint, operator string) error {
	now := time.Now()
	effective := now.Add(DefaultAccountDeletionCooldown())
	vals := map[string]any{
		"account_deletion_requested_at": &now,
		"account_deletion_effective_at": &effective,
		"update_by":                     operator,
	}
	return db.Model(&User{}).Where("id = ? AND is_deleted = ?", userID, SoftDeleteStatusActive).Updates(vals).Error
}

// CancelAccountDeletion 用户主动撤回注销申请。
func CancelAccountDeletion(db *gorm.DB, userID uint, operator string) error {
	vals := map[string]any{
		"account_deletion_requested_at": nil,
		"account_deletion_effective_at": nil,
		"update_by":                     operator,
	}
	return db.Model(&User{}).Where("id = ? AND is_deleted = ?", userID, SoftDeleteStatusActive).Updates(vals).Error
}

// ListUsersDueForAccountDeletion 冷静期已结束、待执行永久注销的用户。
func ListUsersDueForAccountDeletion(db *gorm.DB, before time.Time) ([]User, error) {
	var list []User
	err := db.Where("account_deletion_effective_at IS NOT NULL AND account_deletion_effective_at <= ?", before).
		Where("is_deleted = ? AND status = ?", SoftDeleteStatusActive, UserStatusActive).
		Find(&list).Error
	return list, err
}

// FinalizeAccountDeletion 永久注销：删除绑定类数据，将用户行匿名化并软删除；不删除助手、知识库等业务资源。
func FinalizeAccountDeletion(db *gorm.DB, userID uint, operator string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.Where("id = ?", userID).First(&u).Error; err != nil {
			return err
		}
		if u.IsSoftDeleted() {
			return nil
		}
		if u.AccountDeletionEffectiveAt == nil || u.AccountDeletionEffectiveAt.After(time.Now()) {
			return errors.New("account deletion cooling period not finished")
		}
		if err := tx.Model(&AccountLock{}).Where("user_id = ? OR email = ?", userID, strings.ToLower(strings.TrimSpace(u.Email))).
			Update("is_active", false).Error; err != nil {
			return err
		}

		tombstone := fmt.Sprintf("deleted.%d.%d@void.invalid", userID, time.Now().UnixNano())
		updates := map[string]any{
			"email":                         tombstone,
			"password":                      "",
			"display_name":                  "已注销用户",
			"first_name":                    "",
			"last_name":                     "",
			"phone":                         "",
			"avatar":                        "",
			"github_id":                     "",
			"github_login":                  "",
			"wechat_open_id":                "",
			"wechat_union_id":               "",
			"two_factor_enabled":            false,
			"two_factor_secret":             "",
			"email_verify_token":            "",
			"phone_verify_token":            "",
			"password_reset_token":          "",
			"password_reset_expires":        nil,
			"email_verify_expires":          nil,
			"email_verified":                false,
			"phone_verified":                false,
			"status":                        UserStatusBanned,
			"is_deleted":                    SoftDeleteStatusDeleted,
			"account_deletion_requested_at": nil,
			"account_deletion_effective_at": nil,
			"update_by":                     operator,
		}
		if err := tx.Model(&User{}).Where("id = ?", userID).Updates(updates).Error; err != nil {
			return err
		}
		return nil
	})
}

// AdminUserJSON 管理端用户视图：id 使用十进制字符串，避免前端 JSON number 超过 MAX_SAFE_INTEGER 时精度丢失。
func AdminUserJSON(u User) gin.H {
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
