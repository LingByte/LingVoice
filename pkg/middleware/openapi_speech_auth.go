// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package middleware

import (
	"errors"
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
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusInternalServerError, "service_unavailable", "数据库未配置")
			return
		}
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		token := extractLLMProxyBearerOrAPIKey(c)
		if token == "" {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusUnauthorized, "invalid_api_key", "缺少或无效的认证信息")
			return
		}
		var cred models.Credential
		if err := db.Where("`key` = ? AND status = ? AND kind = ?", token, 1, kind).First(&cred).Error; err != nil {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusUnauthorized, "invalid_api_key", "API Key 不正确")
			return
		}
		now := time.Now().Unix()
		if cred.ExpiredTime != -1 && cred.ExpiredTime > 0 && cred.ExpiredTime < now {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusUnauthorized, "invalid_api_key", "API Key 已过期")
			return
		}
		if !openAPICredentialIPAllowed(&cred, c.ClientIP()) {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusForbidden, "permission_denied", "当前 IP 不在凭证允许列表中")
			return
		}
		if !models.CredentialHasRemainingQuota(&cred) {
			abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusForbidden, models.OpenAPIQuotaReasonCredentialExhausted,
				"该 API Key 凭证额度已用尽")
			return
		}
		if cred.UserId > 0 {
			ok, err := models.UserHasSpendableQuota(db, uint(cred.UserId))
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusForbidden, models.OpenAPIQuotaReasonUserNotFound,
						"该 API Key 绑定的用户不存在")
					return
				}
				abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusInternalServerError, "service_error", "校验用户额度失败")
				return
			}
			if !ok {
				abortLLMAuth(c, OpenAPILLMStyleOpenAI, http.StatusForbidden, models.OpenAPIQuotaReasonUserExhausted,
					"该用户账号用户级额度已用尽")
				return
			}
		}
		_ = db.Model(&models.Credential{}).Where("id = ?", cred.Id).Update("accessed_time", now).Error
		c.Set(ctxOpenAPISpeechCredential, &cred)
		c.Next()
	}
}
