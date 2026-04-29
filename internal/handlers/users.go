// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/cmd/bootstrap"
	"github.com/LingByte/LingVoice/internal/authdto"
	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/constants"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/LingByte/lingstorage-sdk-go"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type sendVerifyEmailRequest struct {
	Email string `json:"email" binding:"required"`
}

type registerRequest struct {
	Email       string `json:"email" binding:"required"`
	Password    string `json:"password" binding:"required"`
	Code        string `json:"code" binding:"required"`
	DisplayName string `json:"displayName"`
	Source      string `json:"source"`
}

// authSendVerifyEmailHandler 向邮箱发送 6 位数字验证码（登录或注册用，邮件内为验证码，非链接）。
func (h *Handlers) authSendVerifyEmailHandler(c *gin.Context) {
	var req sendVerifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "无效的请求", nil)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	db := h.db
	user, err := models.GetUserByEmail(db, email)
	isRegistration := false
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// For registration, create a temporary user record to store the verification code
			// This will be replaced when the actual registration happens
			isRegistration = true
			tempUser := &models.User{
				Email:         email,
				Status:        models.UserStatusPendingVerification,
				EmailVerified: false,
			}
			if err := db.Create(tempUser).Error; err != nil {
				response.FailWithCode(c, 500, "无法创建临时记录", nil)
				return
			}
			user = tempUser
		} else {
			response.FailWithCode(c, 500, "查询失败", nil)
			return
		}
	}
	// Rate limiting: check if verification code was sent within last 60 seconds
	if user.EmailVerifyExpires != nil && time.Since(*user.EmailVerifyExpires) < -9*time.Minute {
		response.FailWithCode(c, 429, "验证码发送过于频繁，请60秒后再试", nil)
		return
	}
	code, err := models.GenerateEmailVerifyToken(db, user)
	if err != nil {
		response.FailWithCode(c, 500, "无法生成验证码", nil)
		return
	}
	utils.Sig().Emit(constants.SigUserVerifyEmail, user, code, c.ClientIP(), c.Request.UserAgent(), db)
	if isRegistration {
		response.Success(c, "验证码已发送，请查收邮箱", nil)
	} else {
		response.Success(c, "若该邮箱已注册，将收到验证码邮件", nil)
	}
}

func (h *Handlers) authLoginHandler(c *gin.Context) {
	var form models.LoginForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.FailWithCode(c, 400, "无效的请求", nil)
		return
	}
	email := strings.ToLower(strings.TrimSpace(form.Email))
	user, err := models.GetUserByEmail(h.db, email)
	if err != nil || user == nil {
		response.FailWithCode(c, 401, "邮箱或密码错误", nil)
		return
	}
	pw := strings.TrimSpace(form.Password)
	if pw == "" {
		response.FailWithCode(c, 401, "邮箱或密码错误", nil)
		return
	}
	ok := false
	if strings.Count(pw, ":") == 3 {
		ok = models.VerifyEncryptedPassword(pw, user.Password)
	} else {
		ok = models.CheckPassword(user, pw)
	}
	if !ok {
		response.FailWithCode(c, 401, "邮箱或密码错误", nil)
		return
	}
	if err := models.CheckUserAllowLogin(h.db, user); err != nil {
		response.FailWithCode(c, 403, "账号当前不可登录", nil)
		return
	}
	if strings.TrimSpace(form.Timezone) != "" {
		models.InTimezone(c, strings.TrimSpace(form.Timezone))
	}
	models.Login(c, user)
	payload, err := authdto.BuildLoginResponse(user)
	if err != nil {
		logger.Error("auth.jwt.sign_failed", zap.Error(err))
		response.FailWithCode(c, 500, "登录令牌签发失败", nil)
		return
	}
	response.Success(c, "登录成功", payload)
}

func (h *Handlers) authRegisterHandler(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "无效的请求", nil)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	code := strings.TrimSpace(req.Code)
	password := strings.TrimSpace(req.Password)
	if email == "" || password == "" || code == "" {
		response.FailWithCode(c, 400, "邮箱、密码和验证码不能为空", nil)
		return
	}
	// Verify email code before allowing registration
	var tempUser models.User
	err := h.db.Where("email = ? AND email_verify_token = ? AND email_verify_expires > ?", email, code, time.Now()).First(&tempUser).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 400, "验证码无效或已过期", nil)
		} else {
			response.FailWithCode(c, 500, "验证码验证失败", nil)
		}
		return
	}
	// Check if this is a pending registration (status = pending_verification) or an existing user
	if tempUser.Status == models.UserStatusActive && tempUser.Password != "" {
		response.FailWithCode(c, 400, "该邮箱已被注册", nil)
		return
	}
	// Update the temporary user record with the actual registration data
	hashedPassword := models.HashPassword(password)
	updateData := map[string]any{
		"password":           hashedPassword,
		"status":             models.UserStatusActive,
		"email_verified":     true,
		"email_verify_token": "",
		"source":             req.Source,
	}
	if strings.TrimSpace(req.DisplayName) != "" {
		updateData["display_name"] = strings.TrimSpace(req.DisplayName)
	}
	if err := models.UpdateUserFields(h.db, &tempUser, updateData); err != nil {
		response.FailWithCode(c, 500, "注册失败", nil)
		return
	}
	// Reload the user to get updated data
	user, err := models.GetUserByEmail(h.db, email)
	if err != nil {
		response.FailWithCode(c, 500, "注册失败", nil)
		return
	}
	// Ensure personal organization and default org binding for multi-tenancy.
	_ = models.EnsurePersonalOrg(h.db, user)
	utils.Sig().Emit(constants.SigUserCreate, user, c)
	models.Login(c, user)
	payload, err := authdto.BuildLoginResponse(user)
	if err != nil {
		logger.Error("auth.jwt.sign_failed", zap.Error(err))
		response.FailWithCode(c, 500, "注册成功但令牌签发失败", nil)
		return
	}
	response.Success(c, "注册成功", payload)
}

func (h *Handlers) authLogoutHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	models.Logout(c, u)
	response.Success(c, "已退出", nil)
}

func (h *Handlers) authMeHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	response.Success(c, "ok", authdto.MeResponse{User: authdto.NewUserResponse(u)})
}

type verifyEmailLoginRequest struct {
	Email string `json:"email" binding:"required"`
	Code  string `json:"code" binding:"required"`
}

// authVerifyEmailLoginHandler 校验邮箱与邮件中的数字验证码并完成会话（同时会把邮箱标为已验证）。
func (h *Handlers) authVerifyEmailLoginHandler(c *gin.Context) {
	var req verifyEmailLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "无效的请求", nil)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	user, err := models.VerifyEmailLoginCode(h.db, email, req.Code)
	if err != nil {
		response.FailWithCode(c, 400, err.Error(), nil)
		return
	}
	if err := models.CheckUserAllowLogin(h.db, user); err != nil {
		response.FailWithCode(c, 403, "账号当前不可登录", nil)
		return
	}
	models.Login(c, user)
	payload, err := authdto.BuildLoginResponse(user)
	if err != nil {
		logger.Error("auth.jwt.sign_failed", zap.Error(err))
		response.FailWithCode(c, 500, "登录令牌签发失败", nil)
		return
	}
	response.Success(c, "登录成功", payload)
}

type refreshTokenRequest struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

// authRefreshHandler exchanges a valid refresh JWT for a new access + refresh pair.
func (h *Handlers) authRefreshHandler(c *gin.Context) {
	if config.GlobalConfig == nil {
		response.FailWithCode(c, 500, "服务未初始化", nil)
		return
	}
	var req refreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "无效的请求", nil)
		return
	}
	rt := strings.TrimSpace(req.RefreshToken)
	var payload *utils.RefreshPayload
	var err error
	if bootstrap.GlobalKeyManager != nil {
		payload, err = utils.ParseRefreshTokenWithKey(rt, bootstrap.GlobalKeyManager)
		if err != nil {
			// Backward-compat: allow HS256 refresh tokens issued before JWKS rollout.
			payload, err = utils.ParseRefreshToken(rt, config.GlobalConfig.Auth.RefreshJWTSigningKey())
		}
	} else {
		payload, err = utils.ParseRefreshToken(rt, config.GlobalConfig.Auth.RefreshJWTSigningKey())
	}
	if err != nil {
		response.FailWithCode(c, 401, "刷新令牌无效或已过期", nil)
		return
	}
	var fresh models.User
	if err := h.db.Where("id = ?", payload.UserID).First(&fresh).Error; err != nil {
		response.FailWithCode(c, 401, "刷新令牌无效", nil)
		return
	}
	if err := models.CheckUserAllowLogin(h.db, &fresh); err != nil {
		response.FailWithCode(c, 403, "账号当前不可登录", nil)
		return
	}
	models.Login(c, &fresh)
	out, err := authdto.BuildLoginResponse(&fresh)
	if err != nil {
		logger.Error("auth.jwt.sign_failed", zap.Error(err))
		response.FailWithCode(c, 500, "令牌签发失败", nil)
		return
	}
	response.Success(c, "ok", out)
}

type adminPatchUserBody struct {
	Status             string  `json:"status"`
	Role               string  `json:"role"`
	DisplayName        *string `json:"display_name"`
	Locale             *string `json:"locale"`
	Phone              *string `json:"phone"`
	FirstName          *string `json:"first_name"`
	LastName           *string `json:"last_name"`
	Avatar             *string `json:"avatar"`
	Timezone           *string `json:"timezone"`
	Gender             *string `json:"gender"`
	City               *string `json:"city"`
	Region             *string `json:"region"`
	EmailNotifications *bool   `json:"email_notifications"`
	PhoneVerified      *bool   `json:"phone_verified"`
	EmailVerified      *bool   `json:"email_verified"`
	RemainQuota        *int    `json:"remain_quota"`
	UsedQuota          *int    `json:"used_quota"`
	UnlimitedQuota     *bool   `json:"unlimited_quota"`
}

// adminUsersListHandler GET /api/admin/users
func (h *Handlers) adminUsersListHandler(c *gin.Context) {
	if !models.RequireAdmin(c) {
		return
	}
	page := models.ParseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))
	offset := (page - 1) * pageSize

	q := h.db.Model(&models.User{})
	if s := strings.TrimSpace(c.Query("email")); s != "" {
		q = q.Where("email LIKE ?", "%"+strings.TrimSpace(strings.ToLower(s))+"%")
	}
	if s := strings.TrimSpace(c.Query("status")); s != "" {
		q = q.Where("status = ?", s)
	}
	if s := strings.TrimSpace(c.Query("role")); s != "" {
		q = q.Where("role = ?", s)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}

	listQ := h.db.Model(&models.User{})
	if s := strings.TrimSpace(c.Query("email")); s != "" {
		listQ = listQ.Where("email LIKE ?", "%"+strings.TrimSpace(strings.ToLower(s))+"%")
	}
	if s := strings.TrimSpace(c.Query("status")); s != "" {
		listQ = listQ.Where("status = ?", s)
	}
	if s := strings.TrimSpace(c.Query("role")); s != "" {
		listQ = listQ.Where("role = ?", s)
	}

	var list []models.User
	if err := listQ.Order("id DESC").Offset(offset).Limit(pageSize).Find(&list).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	listOut := make([]gin.H, 0, len(list))
	for _, row := range list {
		listOut = append(listOut, models.AdminUserJSON(row))
	}
	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	response.Success(c, "ok", gin.H{
		"list":      listOut,
		"total":     total,
		"page":      page,
		"pageSize":  pageSize,
		"totalPage": totalPage,
	})
}

// adminUserDetailHandler GET /api/admin/users/:id
func (h *Handlers) adminUserDetailHandler(c *gin.Context) {
	if !models.RequireAdmin(c) {
		return
	}
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的用户 id", nil)
		return
	}
	var row models.User
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "用户不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"user": models.AdminUserJSON(row)})
}

// adminUserPatchHandler PATCH /api/admin/users/:id
func (h *Handlers) adminUserPatchHandler(c *gin.Context) {
	op := models.CurrentUser(c)
	if !models.RequireAdmin(c) {
		return
	}
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的用户 id", nil)
		return
	}
	var body adminPatchUserBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var row models.User
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "用户不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}

	if row.IsSuperAdmin() && !op.IsSuperAdmin() {
		response.FailWithCode(c, 403, "无权修改超级管理员账号", nil)
		return
	}

	vals := map[string]any{}
	if status := strings.TrimSpace(body.Status); status != "" {
		if op.ID == row.ID && !models.UserStatusAllowsLogin(status) {
			response.FailWithCode(c, 400, "不能将本人账号设为不可登录状态", nil)
			return
		}
		vals["status"] = status
	}
	if r := strings.TrimSpace(body.Role); r != "" {
		if r != models.RoleUser && r != models.RoleAdmin && r != models.RoleSuperAdmin {
			response.FailWithCode(c, 400, "无效的 role", nil)
			return
		}
		if r != row.Role {
			if !op.IsSuperAdmin() {
				response.FailWithCode(c, 403, "仅超级管理员可变更用户角色", nil)
				return
			}
			vals["role"] = r
		}
	}
	if body.DisplayName != nil {
		vals["display_name"] = strings.TrimSpace(*body.DisplayName)
	}
	if body.Locale != nil {
		vals["locale"] = strings.TrimSpace(*body.Locale)
	}
	if body.Phone != nil {
		p := strings.TrimSpace(*body.Phone)
		if len(p) > 64 {
			response.FailWithCode(c, 400, "phone 过长", nil)
			return
		}
		vals["phone"] = p
	}
	if body.FirstName != nil {
		vals["first_name"] = strings.TrimSpace(*body.FirstName)
	}
	if body.LastName != nil {
		vals["last_name"] = strings.TrimSpace(*body.LastName)
	}
	if body.Avatar != nil {
		vals["avatar"] = strings.TrimSpace(*body.Avatar)
	}
	if body.Timezone != nil {
		vals["timezone"] = strings.TrimSpace(*body.Timezone)
	}
	if body.Gender != nil {
		vals["gender"] = strings.TrimSpace(*body.Gender)
	}
	if body.City != nil {
		vals["city"] = strings.TrimSpace(*body.City)
	}
	if body.Region != nil {
		vals["region"] = strings.TrimSpace(*body.Region)
	}
	if body.EmailNotifications != nil {
		vals["email_notifications"] = *body.EmailNotifications
	}
	if body.PhoneVerified != nil {
		vals["phone_verified"] = *body.PhoneVerified
	}
	if body.EmailVerified != nil {
		vals["email_verified"] = *body.EmailVerified
	}
	if body.RemainQuota != nil {
		v := *body.RemainQuota
		if v < 0 {
			response.FailWithCode(c, 400, "remain_quota 不能为负", nil)
			return
		}
		vals["remain_quota"] = v
	}
	if body.UsedQuota != nil {
		v := *body.UsedQuota
		if v < 0 {
			response.FailWithCode(c, 400, "used_quota 不能为负", nil)
			return
		}
		vals["used_quota"] = v
	}
	if body.UnlimitedQuota != nil {
		vals["unlimited_quota"] = *body.UnlimitedQuota
	}
	if len(vals) == 0 {
		response.FailWithCode(c, 400, "无可更新字段", nil)
		return
	}
	vals["update_by"] = models.OperatorFromUser(op)
	if err := models.UpdateUserFields(h.db, &row, vals); err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		response.Success(c, "已更新", gin.H{"user": gin.H{"id": strconv.FormatUint(uint64(id), 10)}})
		return
	}
	response.Success(c, "已更新", gin.H{"user": models.AdminUserJSON(row)})
}

// adminUserDeleteHandler DELETE /api/admin/users/:id（软删除；不可删除本人；非超管不可删超级管理员）
func (h *Handlers) adminUserDeleteHandler(c *gin.Context) {
	op := models.CurrentUser(c)
	if !models.RequireAdmin(c) {
		return
	}
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的用户 id", nil)
		return
	}
	if op.ID == id {
		response.FailWithCode(c, 400, "不能删除本人账号", nil)
		return
	}
	var row models.User
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "用户不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	if row.IsSuperAdmin() && !op.IsSuperAdmin() {
		response.FailWithCode(c, 403, "无权删除超级管理员账号", nil)
		return
	}
	if err := h.db.Delete(&row).Error; err != nil {
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "已删除", gin.H{"id": strconv.FormatUint(uint64(id), 10)})
}

type patchUserProfileBody struct {
	DisplayName string `json:"displayName"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	Gender      string `json:"gender"`
	City        string `json:"city"`
	Region      string `json:"region"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
}

// userProfilePatchHandler PATCH /api/user/profile - update current user's profile
func (h *Handlers) userProfilePatchHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	var body patchUserProfileBody
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", nil)
		return
	}
	vals := map[string]any{}
	if body.DisplayName != "" {
		vals["display_name"] = strings.TrimSpace(body.DisplayName)
	}
	if body.FirstName != "" {
		vals["first_name"] = strings.TrimSpace(body.FirstName)
	}
	if body.LastName != "" {
		vals["last_name"] = strings.TrimSpace(body.LastName)
	}
	if body.Gender != "" {
		vals["gender"] = strings.TrimSpace(body.Gender)
	}
	if body.City != "" {
		vals["city"] = strings.TrimSpace(body.City)
	}
	if body.Region != "" {
		vals["region"] = strings.TrimSpace(body.Region)
	}
	if body.Locale != "" {
		vals["locale"] = strings.TrimSpace(body.Locale)
	}
	if body.Timezone != "" {
		vals["timezone"] = strings.TrimSpace(body.Timezone)
	}
	if len(vals) == 0 {
		response.FailWithCode(c, 400, "无可更新字段", nil)
		return
	}
	vals["update_by"] = models.OperatorFromUser(u)
	if err := models.UpdateUserFields(h.db, u, vals); err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	if err := h.db.Where("id = ?", u.ID).First(u).Error; err != nil {
		response.Success(c, "已更新", nil)
		return
	}
	response.Success(c, "已更新", gin.H{"user": authdto.NewUserResponse(u)})
}

type uploadAvatarResponse struct {
	URL string `json:"url"`
}

// userAvatarUploadHandler POST /api/user/avatar - upload user avatar
func (h *Handlers) userAvatarUploadHandler(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		response.FailWithCode(c, 400, "请上传文件", nil)
		return
	}
	if file.Size > 5*1024*1024 {
		response.FailWithCode(c, 400, "文件大小不能超过 5MB", nil)
		return
	}
	allowedTypes := []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
	isAllowed := false
	for _, t := range allowedTypes {
		if file.Header.Get("Content-Type") == t {
			isAllowed = true
			break
		}
	}
	if !isAllowed {
		response.FailWithCode(c, 400, "仅支持 JPG、PNG、GIF、WebP 格式", nil)
		return
	}
	src, err := file.Open()
	if err != nil {
		response.FailWithCode(c, 500, "文件读取失败", nil)
		return
	}
	defer src.Close()
	if config.GlobalStore == nil {
		response.FailWithCode(c, 500, "存储服务未初始化", nil)
		return
	}
	fileBytes, err := io.ReadAll(src)
	if err != nil {
		response.FailWithCode(c, 500, "文件读取失败", nil)
		return
	}
	objectKey := fmt.Sprintf("avatars/%d/%d%s", u.ID, time.Now().UnixMilli(), filepath.Ext(file.Filename))
	up, upErr := config.GlobalStore.UploadBytes(&lingstorage.UploadBytesRequest{
		Data:     fileBytes,
		Filename: file.Filename,
		Bucket:   config.GlobalConfig.Services.Storage.Bucket,
		Key:      objectKey,
	})
	if upErr != nil {
		logger.Error("avatar.upload_failed", zap.Error(upErr))
		response.FailWithCode(c, 500, "上传失败", nil)
		return
	}
	if err := models.UpdateUserFields(h.db, u, map[string]any{"avatar": up.URL}); err != nil {
		logger.Error("avatar.update_failed", zap.Error(err))
		response.FailWithCode(c, 500, "保存失败", nil)
		return
	}
	u.Avatar = up.URL
	response.Success(c, "上传成功", uploadAvatarResponse{URL: up.URL})
}
