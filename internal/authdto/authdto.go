// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

// Package authdto holds JSON shapes and builders for /api/auth and related user payloads.
package authdto

import (
	"errors"
	"strconv"
	"time"

	"github.com/LingByte/LingVoice/cmd/bootstrap"
	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/utils/accessutils"
	"gorm.io/gorm"
)

// UserResponse is the public user slice returned on auth endpoints.
type UserResponse struct {
	// 字符串避免雪花 ID 超过 JS Number 安全整数时前端精度丢失。
	ID              string `json:"id"`
	Email           string `json:"email"`
	DisplayName     string `json:"displayName,omitempty"`
	FirstName       string `json:"firstName,omitempty"`
	LastName        string `json:"lastName,omitempty"`
	Gender          string `json:"gender,omitempty"`
	City            string `json:"city,omitempty"`
	Region          string `json:"region,omitempty"`
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
	RemainQuota     int    `json:"remainQuota"`
	UsedQuota       int    `json:"usedQuota"`
	UnlimitedQuota  bool   `json:"unlimitedQuota"`
	CreatedAt       string `json:"createdAt,omitempty"` // RFC3339 UTC
	LastLogin       string `json:"lastLogin,omitempty"` // RFC3339 UTC
}

// LoginResponse is returned after login, register, magic-link login, or token refresh.
type LoginResponse struct {
	User             UserResponse `json:"user"`
	AccessToken      string       `json:"accessToken"`
	RefreshToken     string       `json:"refreshToken"`
	TokenType        string       `json:"tokenType"` // "Bearer"
	ExpiresIn        int64        `json:"expiresIn"` // access lifetime seconds
	RefreshExpiresIn int64        `json:"refreshExpiresIn"`
}

// MeResponse is returned by GET /api/auth/me.
type MeResponse struct {
	User UserResponse `json:"user"`
}

func authTimeRFC3339UTC(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// NewUserResponse maps a domain user（及可选 user_profiles）到对外 JSON。
func NewUserResponse(u *models.User, prof *models.UserProfile) UserResponse {
	if u == nil {
		return UserResponse{}
	}
	gender, city, region, loc, tz := "", "", "", "", ""
	pc := 0
	if prof != nil {
		gender = prof.Gender
		city = prof.City
		region = prof.Region
		loc = prof.Locale
		tz = prof.Timezone
		pc = prof.ProfileComplete
	}
	return UserResponse{
		ID:              strconv.FormatUint(uint64(u.ID), 10),
		Email:           u.Email,
		DisplayName:     u.DisplayName,
		FirstName:       u.FirstName,
		LastName:        u.LastName,
		Gender:          gender,
		City:            city,
		Region:          region,
		Role:            u.Role,
		Status:          u.Status,
		Source:          u.Source,
		Locale:          loc,
		Timezone:        tz,
		Avatar:          u.Avatar,
		EmailVerified:   u.EmailVerified,
		PhoneVerified:   u.PhoneVerified,
		ProfileComplete: pc,
		LoginCount:      u.LoginCount,
		RemainQuota:     u.RemainQuota,
		UsedQuota:       u.UsedQuota,
		UnlimitedQuota:  u.UnlimitedQuota,
		CreatedAt:       u.CreatedAt.UTC().Format(time.RFC3339),
		LastLogin:       authTimeRFC3339UTC(u.LastLogin),
	}
}

// BuildLoginResponse issues access/refresh tokens and wraps the user payload.
func BuildLoginResponse(db *gorm.DB, u *models.User) (*LoginResponse, error) {
	if u == nil {
		return nil, errors.New("nil user")
	}
	prof, err := models.EnsureUserProfile(db, u.ID)
	if err != nil {
		return nil, err
	}
	cfg := config.GlobalConfig
	if cfg == nil {
		return nil, errors.New("config not loaded")
	}
	accessTTL := cfg.Auth.AccessTokenTTL()
	refreshTTL := cfg.Auth.RefreshTokenTTL()
	var access string
	if bootstrap.GlobalKeyManager != nil {
		access, err = accessutils.SignAccessTokenWithKey(accessutils.AccessPayload{
			UserID: u.ID,
			Email:  u.Email,
			Role:   u.Role,
		}, bootstrap.GlobalKeyManager, accessTTL)
	} else {
		access, err = accessutils.SignAccessToken(accessutils.AccessPayload{
			UserID: u.ID,
			Email:  u.Email,
			Role:   u.Role,
		}, cfg.Auth.JWTSigningKey(), accessTTL)
	}
	if err != nil {
		return nil, err
	}
	var refresh string
	if bootstrap.GlobalKeyManager != nil {
		refresh, err = accessutils.SignRefreshTokenWithKey(accessutils.RefreshPayload{UserID: u.ID}, bootstrap.GlobalKeyManager, refreshTTL)
	} else {
		refresh, err = accessutils.SignRefreshToken(accessutils.RefreshPayload{UserID: u.ID}, cfg.Auth.RefreshJWTSigningKey(), refreshTTL)
	}
	if err != nil {
		return nil, err
	}
	return &LoginResponse{
		User:             NewUserResponse(u, prof),
		AccessToken:      access,
		RefreshToken:     refresh,
		TokenType:        "Bearer",
		ExpiresIn:        int64(accessTTL.Seconds()),
		RefreshExpiresIn: int64(refreshTTL.Seconds()),
	}, nil
}
