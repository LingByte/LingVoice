// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/response"
	"github.com/gin-gonic/gin"
)

func operatorFromUser(u *models.User) string {
	if u == nil {
		return ""
	}
	if strings.TrimSpace(u.Email) != "" {
		return strings.TrimSpace(u.Email)
	}
	return fmt.Sprintf("uid:%d", u.ID)
}

func requireAdmin(c *gin.Context) bool {
	u := models.CurrentUser(c)
	if u == nil || !u.IsAdmin() {
		response.FailWithCode(c, 403, "需要管理员权限", nil)
		return false
	}
	return true
}

func parseUintParam(c *gin.Context, name string) (uint, bool) {
	s := strings.TrimSpace(c.Param(name))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 {
		return 0, false
	}
	max := uint64(^uint(0))
	if n > max {
		return 0, false
	}
	return uint(n), true
}

// parseInt64Param parses a positive path param (e.g. snowflake mail_logs id).
func parseInt64Param(c *gin.Context, name string) (int64, bool) {
	s := strings.TrimSpace(c.Param(name))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func parseIntParam(c *gin.Context, name string) (int, bool) {
	s := strings.TrimSpace(c.Param(name))
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func parseQueryInt(c *gin.Context, name string, def int) int {
	s := strings.TrimSpace(c.Query(name))
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func clampPageSize(n int) int {
	switch {
	case n <= 0:
		return 20
	case n > 100:
		return 100
	default:
		return n
	}
}

func parseQueryUint(c *gin.Context, name string) (uint, bool) {
	s := strings.TrimSpace(c.Query(name))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 {
		return 0, false
	}
	max := uint64(^uint(0))
	if n > max {
		return 0, false
	}
	return uint(n), true
}
