// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package middleware

import (
	"net/http"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const ctxOpenAPISpeechCredential = "openapi_speech_credential"

// OpenAPISpeechCredentialFromContext 由 OpenAPISpeechProxyAuth 注入，kind 为 asr 或 tts。
func OpenAPISpeechCredentialFromContext(c *gin.Context) (*models.Credential, bool) {
	v, ok := c.Get(ctxOpenAPISpeechCredential)
	if !ok {
		return nil, false
	}
	cred, ok := v.(*models.Credential)
	return cred, ok
}

// OpenAPISpeechProxyAuth 与 LLM OpenAPI 相同 Bearer / x-api-key，凭证 kind 为 asr 或 tts。
func OpenAPISpeechProxyAuth(db *gorm.DB, kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if db == nil {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusInternalServerError, "service_unavailable", "database not configured")
			return
		}
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		token := extractLLMProxyBearerOrAPIKey(c)
		if token == "" {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusUnauthorized, "invalid_api_key", "Missing or invalid authentication")
			return
		}
		var cred models.Credential
		if err := db.Where("`key` = ? AND status = ? AND kind = ?", token, 1, kind).First(&cred).Error; err != nil {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusUnauthorized, "invalid_api_key", "Incorrect API key provided")
			return
		}
		now := time.Now().Unix()
		if cred.ExpiredTime != -1 && cred.ExpiredTime > 0 && cred.ExpiredTime < now {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusUnauthorized, "invalid_api_key", "API key expired")
			return
		}
		if !openAPICredentialIPAllowed(&cred, c.ClientIP()) {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusForbidden, "permission_denied", "Client IP not allowed")
			return
		}
		if !cred.UnlimitedQuota && cred.RemainQuota <= 0 {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusForbidden, "insufficient_quota", "Quota exceeded")
			return
		}
		if cred.UserId > 0 {
			var owner models.User
			if err := db.Select("id", "remain_quota", "unlimited_quota").Where("id = ?", cred.UserId).First(&owner).Error; err != nil {
				abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusForbidden, "insufficient_quota", "User account not found")
				return
			}
			if !owner.UnlimitedQuota && owner.RemainQuota <= 0 {
				abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusForbidden, "insufficient_quota", "User quota exceeded")
				return
			}
		}
		_ = db.Model(&models.Credential{}).Where("id = ?", cred.Id).Update("accessed_time", now).Error
		c.Set(ctxOpenAPISpeechCredential, &cred)
		c.Next()
	}
}
