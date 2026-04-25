// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const ctxOpenAPICredential = "openapi_credential"

// OpenAPICredentialFromContext returns the credential bound by OpenAPIEmailCredential middleware.
func OpenAPICredentialFromContext(c *gin.Context) (*models.Credential, bool) {
	v, ok := c.Get(ctxOpenAPICredential)
	if !ok {
		return nil, false
	}
	cred, ok := v.(*models.Credential)
	return cred, ok
}

var (
	openAPINonceMu sync.Mutex
	openAPINonce   = make(map[string]time.Time)
)

func openAPIConsumeNonce(nonce string, ttl time.Duration) bool {
	if len(nonce) < 8 || len(nonce) > 128 {
		return false
	}
	now := time.Now()
	openAPINonceMu.Lock()
	defer openAPINonceMu.Unlock()
	for k, exp := range openAPINonce {
		if now.After(exp) {
			delete(openAPINonce, k)
		}
	}
	if _, dup := openAPINonce[nonce]; dup {
		return false
	}
	openAPINonce[nonce] = now.Add(ttl)
	return true
}

func openAPIParseBearerLAuth(raw string) (token string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	low := strings.ToLower(raw)
	if strings.HasPrefix(low, "bearer ") {
		return strings.TrimSpace(raw[7:]), true
	}
	return raw, true
}

func openAPICredentialIPAllowed(cred *models.Credential, clientIP string) bool {
	if cred == nil || cred.AllowIps == nil {
		return true
	}
	list := strings.TrimSpace(*cred.AllowIps)
	if list == "" {
		return true
	}
	ip := strings.TrimSpace(clientIP)
	if ip == "" {
		return false
	}
	for _, p := range strings.Split(list, ",") {
		if strings.TrimSpace(p) == ip {
			return true
		}
	}
	return false
}

// OpenAPIEmailCredential 校验 LAuthorization: Bearer <APIKEY>，并要求 L-Timestamp（Unix 秒）与 L-Nonce（防重放）。
// 仅 kind=email 且启用、未过期的凭证可通过。
func OpenAPIEmailCredential(db *gorm.DB) gin.HandlerFunc {
	const skew = 5 * time.Minute
	const nonceTTL = 10 * time.Minute

	return func(c *gin.Context) {
		if db == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"code": 500, "msg": "服务未就绪", "data": nil})
			return
		}
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		auth := strings.TrimSpace(c.GetHeader("LAuthorization"))
		token, ok := openAPIParseBearerLAuth(auth)
		if !ok || token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "缺少或无效的 LAuthorization（需 Bearer APIKEY）", "data": nil})
			return
		}
		tsStr := strings.TrimSpace(c.GetHeader("L-Timestamp"))
		nonce := strings.TrimSpace(c.GetHeader("L-Nonce"))
		if tsStr == "" || nonce == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "缺少 L-Timestamp 或 L-Nonce", "data": nil})
			return
		}
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"code": 400, "msg": "L-Timestamp 须为 Unix 秒", "data": nil})
			return
		}
		now := time.Now().Unix()
		if ts < now-int64(skew.Seconds()) || ts > now+int64(skew.Seconds()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "L-Timestamp 超出允许时间窗", "data": nil})
			return
		}

		var cred models.Credential
		if err := db.Where("`key` = ? AND status = ? AND kind = ?", token, 1, models.CredentialKindEmail).
			First(&cred).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "API Key 无效或已禁用", "data": nil})
			return
		}
		if !openAPIConsumeNonce(nonce, nonceTTL) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "L-Nonce 重复或无效", "data": nil})
			return
		}
		if cred.ExpiredTime != -1 && cred.ExpiredTime > 0 && cred.ExpiredTime < now {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": 401, "msg": "API Key 已过期", "data": nil})
			return
		}
		if !openAPICredentialIPAllowed(&cred, c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": 403, "msg": "当前 IP 不在凭证允许列表中", "data": nil})
			return
		}

		_ = db.Model(&models.Credential{}).Where("id = ?", cred.Id).Update("accessed_time", now).Error

		c.Set(ctxOpenAPICredential, &cred)
		c.Next()
	}
}
