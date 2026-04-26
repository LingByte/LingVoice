// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const ctxOpenAPILLMCredential = "openapi_llm_credential"

// OpenAPILLMProxyAuthStyle 鉴权失败时的错误 JSON 形态（贴近各协议常见字段）。
type OpenAPILLMProxyAuthStyle string

const (
	OpenAPILLMStyleOpenAI    OpenAPILLMProxyAuthStyle = "openai"
	OpenAPILLMStyleAnthropic OpenAPILLMProxyAuthStyle = "anthropic"
)

// OpenAPILLMCredentialFromContext 由 OpenAPILLMProxyAuth 注入，kind=llm 的凭证。
func OpenAPILLMCredentialFromContext(c *gin.Context) (*models.Credential, bool) {
	v, ok := c.Get(ctxOpenAPILLMCredential)
	if !ok {
		return nil, false
	}
	cred, ok := v.(*models.Credential)
	return cred, ok
}

// OpenAPILLMProxyAuth 校验 LLM 代理调用：Authorization: Bearer <key>，或 x-api-key（Anthropic 客户端常用）。
// 使用 credentials 表中 kind=llm、status=1 的密钥；可选 IP 白名单与配额（与邮件 OpenAPI 一致）。
func OpenAPILLMProxyAuth(db *gorm.DB, errStyle OpenAPILLMProxyAuthStyle) gin.HandlerFunc {
	return func(c *gin.Context) {
		if db == nil {
			abortLLMAuth(c, errStyle, http.StatusInternalServerError, "service_unavailable", "database not configured")
			return
		}
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		token := extractLLMProxyBearerOrAPIKey(c)
		if token == "" {
			abortLLMAuth(c, errStyle, http.StatusUnauthorized, "invalid_api_key", "Missing or invalid authentication")
			return
		}

		var cred models.Credential
		if err := db.Where("`key` = ? AND status = ? AND kind = ?", token, 1, models.CredentialKindLLM).
			First(&cred).Error; err != nil {
			abortLLMAuth(c, errStyle, http.StatusUnauthorized, "invalid_api_key", "Incorrect API key provided")
			return
		}
		now := time.Now().Unix()
		if cred.ExpiredTime != -1 && cred.ExpiredTime > 0 && cred.ExpiredTime < now {
			abortLLMAuth(c, errStyle, http.StatusUnauthorized, "invalid_api_key", "API key expired")
			return
		}
		if !openAPICredentialIPAllowed(&cred, c.ClientIP()) {
			abortLLMAuth(c, errStyle, http.StatusForbidden, "permission_denied", "Client IP not allowed")
			return
		}
		if !cred.UnlimitedQuota && cred.RemainQuota <= 0 {
			abortLLMAuth(c, errStyle, http.StatusForbidden, "insufficient_quota", "Quota exceeded")
			return
		}

		if cred.UserId > 0 {
			var owner models.User
			if err := db.Select("id", "remain_quota", "unlimited_quota").Where("id = ?", cred.UserId).First(&owner).Error; err != nil {
				abortLLMAuth(c, errStyle, http.StatusForbidden, "insufficient_quota", "User account not found")
				return
			}
			if !owner.UnlimitedQuota && owner.RemainQuota <= 0 {
				abortLLMAuth(c, errStyle, http.StatusForbidden, "insufficient_quota", "User quota exceeded")
				return
			}
		}

		_ = db.Model(&models.Credential{}).Where("id = ?", cred.Id).Update("accessed_time", now).Error
		c.Set(ctxOpenAPILLMCredential, &cred)
		c.Next()
	}
}

func extractLLMProxyBearerOrAPIKey(c *gin.Context) string {
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	low := strings.ToLower(auth)
	if strings.HasPrefix(low, "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if k := strings.TrimSpace(c.GetHeader("X-Api-Key")); k != "" {
		return k
	}
	if k := strings.TrimSpace(c.GetHeader("x-api-key")); k != "" {
		return k
	}
	return ""
}

func abortLLMAuth(c *gin.Context, style OpenAPILLMProxyAuthStyle, status int, code, msg string) {
	switch style {
	case OpenAPILLMStyleAnthropic:
		errType := "invalid_request_error"
		if status == http.StatusUnauthorized {
			errType = "authentication_error"
		}
		if status == http.StatusForbidden {
			errType = "permission_error"
		}
		c.AbortWithStatusJSON(status, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    errType,
				"message": msg,
			},
		})
	default:
		c.AbortWithStatusJSON(status, gin.H{
			"error": gin.H{
				"message": msg,
				"type":    "invalid_request_error",
				"code":    code,
				"param":   nil,
			},
		})
	}
}
