// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package jwtauth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// RefreshPayload is embedded in long-lived refresh tokens.
type RefreshPayload struct {
	UserID uint `json:"uid"`
}

type refreshClaims struct {
	UserID uint `json:"uid"`
	jwt.RegisteredClaims
}

// SignRefreshToken issues a refresh JWT (distinct issuer from access).
func SignRefreshToken(p RefreshPayload, secret string, ttl time.Duration) (string, error) {
	if len(secret) < 8 {
		return "", errors.New("jwt: refresh signing secret too short (min 8 bytes)")
	}
	if ttl <= 0 {
		return "", errors.New("jwt: ttl must be positive")
	}
	now := time.Now()
	claims := refreshClaims{
		UserID: p.UserID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    RefreshIssuer,
			Subject:   fmt.Sprintf("refresh:%d", p.UserID),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, &claims)
	return t.SignedString([]byte(secret))
}

// ParseRefreshToken validates a refresh JWT.
func ParseRefreshToken(tokenString, secret string) (*RefreshPayload, error) {
	if tokenString == "" || len(secret) < 8 {
		return nil, ErrInvalidToken
	}
	token, err := jwt.ParseWithClaims(tokenString, &refreshClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil || token == nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	rc, ok := token.Claims.(*refreshClaims)
	if !ok {
		return nil, ErrInvalidToken
	}
	if rc.Issuer != RefreshIssuer {
		return nil, ErrInvalidToken
	}
	return &RefreshPayload{UserID: rc.UserID}, nil
}
