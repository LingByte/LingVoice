// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package middleware

import (
	"strings"

	"github.com/LingByte/LingVoice/i18n"
	"github.com/gin-gonic/gin"
)

// AcceptLanguage parses Accept-Language and stores a normalized tag on the Gin context
// for lingi18n.GetLangFromContext (see i18n.GinContextKeyLanguage).
func AcceptLanguage() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := strings.TrimSpace(c.GetHeader("Accept-Language"))
		lang := i18n.ParseAcceptLanguage(h)
		if lang != "" {
			c.Set(i18n.GinContextKeyLanguage, lang)
		}
		c.Next()
	}
}
