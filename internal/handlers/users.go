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
