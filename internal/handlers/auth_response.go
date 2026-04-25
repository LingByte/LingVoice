// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strconv"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/config"
	"github.com/LingByte/LingVoice/pkg/jwtauth"
)

// AuthUserResponse is the public user slice returned on auth endpoints.
type AuthUserResponse struct {
	// 字符串避免雪花 ID 超过 JS Number 安全整数时前端精度丢失。
	ID              string `json:"id"`
	Email           string `json:"email"`
	DisplayName     string `json:"displayName,omitempty"`
	FirstName       string `json:"firstName,omitempty"`
	LastName        string `json:"lastName,omitempty"`
	Role            string `json:"role"`
	Status          string `json:"status"`
	Source          string `json:"source,omitempty"`
	Locale          string `json:"locale,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
	Avatar          string `json:"avatar,omitempty"`
	EmailVerified   bool   `json:"emailVerified"`
	PhoneVerified   bool   `json:"phoneVerified"`
	ProfileComplete int    `json:"profileComplete"`
	LoginCount      int    `json:"loginCount"`
	CreatedAt       string `json:"createdAt,omitempty"` // RFC3339 UTC
	LastLogin       string `json:"lastLogin,omitempty"` // RFC3339 UTC
}

// AuthLoginResponse is returned after login, register, magic-link login, or token refresh.
type AuthLoginResponse struct {
	User             AuthUserResponse `json:"user"`
	AccessToken      string           `json:"accessToken"`
	RefreshToken     string           `json:"refreshToken"`
	TokenType        string           `json:"tokenType"` // "Bearer"
	ExpiresIn        int64            `json:"expiresIn"` // access lifetime seconds
	RefreshExpiresIn int64            `json:"refreshExpiresIn"`
}

// AuthMeResponse is returned by GET /api/auth/me.
type AuthMeResponse struct {
	User AuthUserResponse `json:"user"`
}

func authTimeRFC3339UTC(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func newAuthUserResponse(u *models.User) AuthUserResponse {
	if u == nil {
		return AuthUserResponse{}
	}
	return AuthUserResponse{
		ID:              strconv.FormatUint(uint64(u.ID), 10),
		Email:           u.Email,
		DisplayName:     u.DisplayName,
		FirstName:       u.FirstName,
		LastName:        u.LastName,
		Role:            u.Role,
		Status:          u.Status,
		Source:          u.Source,
		Locale:          u.Locale,
		Timezone:        u.Timezone,
		Avatar:          u.Avatar,
		EmailVerified:   u.EmailVerified,
		PhoneVerified:   u.PhoneVerified,
		ProfileComplete: u.ProfileComplete,
		LoginCount:      u.LoginCount,
		CreatedAt:       u.CreatedAt.UTC().Format(time.RFC3339),
		LastLogin:       authTimeRFC3339UTC(u.LastLogin),
	}
}

func buildAuthLoginResponse(u *models.User) (*AuthLoginResponse, error) {
	if u == nil {
		return nil, errors.New("nil user")
	}
	cfg := config.GlobalConfig
	if cfg == nil {
		return nil, errors.New("config not loaded")
	}
	accessTTL := cfg.Auth.AccessTokenTTL()
	refreshTTL := cfg.Auth.RefreshTokenTTL()
	access, err := jwtauth.SignAccessToken(jwtauth.AccessPayload{
		UserID: u.ID,
		Email:  u.Email,
		Role:   u.Role,
	}, cfg.Auth.JWTSigningKey(), accessTTL)
	if err != nil {
		return nil, err
	}
	refresh, err := jwtauth.SignRefreshToken(jwtauth.RefreshPayload{UserID: u.ID}, cfg.Auth.RefreshJWTSigningKey(), refreshTTL)
	if err != nil {
		return nil, err
	}
	return &AuthLoginResponse{
		User:             newAuthUserResponse(u),
		AccessToken:      access,
		RefreshToken:     refresh,
		TokenType:        "Bearer",
		ExpiresIn:        int64(accessTTL.Seconds()),
		RefreshExpiresIn: int64(refreshTTL.Seconds()),
	}, nil
}
