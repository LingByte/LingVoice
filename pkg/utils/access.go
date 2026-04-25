// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

// Package jwtauth provides a small HS256 access-token template (claims + sign/parse).
package utils

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	// ErrInvalidToken is returned when the token is malformed, expired, or signature-invalid.
	ErrInvalidToken = errors.New("jwt: invalid token")
)

// AccessPayload is the application data carried in an access token.
type AccessPayload struct {
	UserID uint   `json:"uid"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type accessClaims struct {
	UserID uint   `json:"uid"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

// SignAccessToken issues an HS256 JWT with subject user:<id> and standard time claims.
func SignAccessToken(p AccessPayload, secret string, ttl time.Duration) (string, error) {
	if len(secret) < 8 {
		return "", errors.New("jwt: signing secret too short (min 8 bytes)")
	}
	if ttl <= 0 {
		return "", errors.New("jwt: ttl must be positive")
	}
	now := time.Now()
	claims := accessClaims{
		UserID: p.UserID,
		Email:  p.Email,
		Role:   p.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    AccessIssuer,
			Subject:   fmt.Sprintf("user:%d", p.UserID),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, &claims)
	return t.SignedString([]byte(secret))
}

// ParseAccessToken validates signature and expiry and returns the embedded payload.
func ParseAccessToken(tokenString, secret string) (*AccessPayload, error) {
	if tokenString == "" || len(secret) < 8 {
		return nil, ErrInvalidToken
	}
	token, err := jwt.ParseWithClaims(tokenString, &accessClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil || token == nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	ac, ok := token.Claims.(*accessClaims)
	if !ok {
		return nil, ErrInvalidToken
	}
	if ac.Issuer != AccessIssuer {
		return nil, ErrInvalidToken
	}
	return &AccessPayload{
		UserID: ac.UserID,
		Email:  ac.Email,
		Role:   ac.Role,
	}, nil
}
