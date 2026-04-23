package middleware

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// LoggerMiddleware 请求日志中间件
func LoggerMiddleware(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		method := c.Request.Method

		// 处理请求
		c.Next()

		// 过滤规则：
		// 1. 过滤监控相关的路径（/metrics, /monitor 等）
		// 2. 过滤一般的 GET 请求（只记录 POST, PUT, DELETE, PATCH 等）
		shouldLog := true
		if method == "GET" {
			shouldLog = false
		}
		if shouldLog {
			end := time.Now()
			latency := end.Sub(start)
			logger.Info("Request",
				zap.Int("status", c.Writer.Status()),
				zap.String("method", method),
				zap.String("path", path),
				zap.String("query", query),
				zap.String("ip", c.ClientIP()),
				zap.String("user-agent", c.Request.UserAgent()),
				zap.Duration("latency", latency),
			)
		}
	}
}
