// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/config"
	"github.com/LingByte/LingVoice/pkg/constants"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/LingByte/LingVoice/pkg/utils"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type sendVerifyEmailRequest struct {
	Email string `json:"email" binding:"required"`
}

// postSendVerifyEmail 向已注册邮箱发送 6 位数字登录验证码（邮件内为验证码，非链接）。
func (h *Handlers) postSendVerifyEmail(c *gin.Context) {
	var req sendVerifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.FailWithCode(c, 400, "无效的请求", nil)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	db := h.db
	user, err := models.GetUserByEmail(db, email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Success(c, "若该邮箱已注册，将收到验证码邮件", nil)
			return
		}
		response.FailWithCode(c, 500, "查询失败", nil)
		return
	}
	code, err := models.GenerateEmailVerifyToken(db, user)
	if err != nil {
		response.FailWithCode(c, 500, "无法生成验证码", nil)
		return
	}
	utils.Sig().Emit(constants.SigUserVerifyEmail, user, code, c.ClientIP(), c.Request.UserAgent(), db)
	response.Success(c, "若该邮箱已注册，将收到验证码邮件", nil)
}

func (h *Handlers) postLogin(c *gin.Context) {
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
	payload, err := buildAuthLoginResponse(user)
	if err != nil {
		logger.Error("auth.jwt.sign_failed", zap.Error(err))
		response.FailWithCode(c, 500, "登录令牌签发失败", nil)
		return
	}
	response.Success(c, "登录成功", payload)
}

func (h *Handlers) postRegister(c *gin.Context) {
	var form models.RegisterUserForm
	if err := c.ShouldBindJSON(&form); err != nil {
		response.FailWithCode(c, 400, "无效的请求", nil)
		return
	}
	email := strings.ToLower(strings.TrimSpace(form.Email))
	if email == "" || strings.TrimSpace(form.Password) == "" {
		response.FailWithCode(c, 400, "邮箱和密码不能为空", nil)
		return
	}
	if models.IsExistsByEmail(h.db, email) {
		response.FailWithCode(c, 400, "邮箱已被注册", nil)
		return
	}
	source := models.NormalizeUserSource(form.Source)
	user, err := models.CreateUserWithMeta(h.db, email, form.Password, source, models.UserStatusActive)
	if err != nil {
		response.FailWithCode(c, 500, "注册失败", nil)
		return
	}
	if strings.TrimSpace(form.DisplayName) != "" || strings.TrimSpace(form.FirstName) != "" || strings.TrimSpace(form.LastName) != "" {
		_ = models.UpdateUserFields(h.db, user, map[string]any{
			"display_name": strings.TrimSpace(form.DisplayName),
			"first_name":   strings.TrimSpace(form.FirstName),
			"last_name":    strings.TrimSpace(form.LastName),
			"locale":       strings.TrimSpace(form.Locale),
			"timezone":     strings.TrimSpace(form.Timezone),
		})
		if ref, e2 := models.GetUserByEmail(h.db, email); e2 == nil && ref != nil {
			user = ref
		}
	}
	utils.Sig().Emit(constants.SigUserCreate, user, c)
	models.Login(c, user)
	payload, err := buildAuthLoginResponse(user)
	if err != nil {
		logger.Error("auth.jwt.sign_failed", zap.Error(err))
		response.FailWithCode(c, 500, "注册成功但令牌签发失败", nil)
		return
	}
	response.Success(c, "注册成功", payload)
}

func (h *Handlers) postLogout(c *gin.Context) {
	u := models.CurrentUser(c)
	models.Logout(c, u)
	response.Success(c, "已退出", nil)
}

func (h *Handlers) getAuthMe(c *gin.Context) {
	u := models.CurrentUser(c)
	if u == nil {
		response.FailWithCode(c, 401, "未登录", nil)
		return
	}
	response.Success(c, "ok", AuthMeResponse{User: newAuthUserResponse(u)})
}

type verifyEmailLoginRequest struct {
	Email string `json:"email" binding:"required"`
	Code  string `json:"code" binding:"required"`
}

// postVerifyEmailLogin 校验邮箱与邮件中的数字验证码并完成会话（同时会把邮箱标为已验证）。
func (h *Handlers) postVerifyEmailLogin(c *gin.Context) {
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
	payload, err := buildAuthLoginResponse(user)
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

// postRefresh exchanges a valid refresh JWT for a new access + refresh pair.
func (h *Handlers) postRefresh(c *gin.Context) {
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
	payload, err := utils.ParseRefreshToken(rt, config.GlobalConfig.Auth.RefreshJWTSigningKey())
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
	out, err := buildAuthLoginResponse(&fresh)
	if err != nil {
		logger.Error("auth.jwt.sign_failed", zap.Error(err))
		response.FailWithCode(c, 500, "令牌签发失败", nil)
		return
	}
	response.Success(c, "ok", out)
}

type adminPatchUserBody struct {
	Status      string `json:"status"`
	Role        string `json:"role"`
	DisplayName string `json:"display_name"`
	Locale      string `json:"locale"`
}

// listAdminUsers GET /api/admin/users
func (h *Handlers) listAdminUsers(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	page := parseQueryInt(c, "page", 1)
	if page < 1 {
		page = 1
	}
	pageSize := clampPageSize(parseQueryInt(c, "pageSize", 20))
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
	totalPage := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPage++
	}
	response.Success(c, "ok", gin.H{
		"list":      list,
		"total":     total,
		"page":      page,
		"pageSize":  pageSize,
		"totalPage": totalPage,
	})
}

// getAdminUser GET /api/admin/users/:id
func (h *Handlers) getAdminUser(c *gin.Context) {
	if !requireAdmin(c) {
		return
	}
	id, ok := parseUintParam(c, "id")
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
	response.Success(c, "ok", gin.H{"user": row})
}

// patchAdminUser PATCH /api/admin/users/:id
func (h *Handlers) patchAdminUser(c *gin.Context) {
	op := models.CurrentUser(c)
	if !requireAdmin(c) {
		return
	}
	id, ok := parseUintParam(c, "id")
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
	if s := strings.TrimSpace(body.Status); s != "" {
		norm := models.NormalizeUserStatus(s)
		if norm == "" {
			response.FailWithCode(c, 400, "无效的 status", nil)
			return
		}
		if op.ID == row.ID && !models.UserStatusAllowsLogin(norm) {
			response.FailWithCode(c, 400, "不能将本人账号设为不可登录状态", nil)
			return
		}
		vals["status"] = norm
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
	if body.DisplayName != "" {
		vals["display_name"] = strings.TrimSpace(body.DisplayName)
	}
	if body.Locale != "" {
		vals["locale"] = strings.TrimSpace(body.Locale)
	}
	if len(vals) == 0 {
		response.FailWithCode(c, 400, "无可更新字段", nil)
		return
	}
	vals["update_by"] = operatorFromUser(op)
	if err := models.UpdateUserFields(h.db, &row, vals); err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		response.Success(c, "已更新", gin.H{"user": gin.H{"id": id}})
		return
	}
	response.Success(c, "已更新", gin.H{"user": row})
}
