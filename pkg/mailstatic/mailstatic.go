// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

// Package mailstatic holds embedded HTML email layouts (//go:embed).
// Runtime 发信应使用本包渲染，不依赖数据库 MailTemplate。
package mailstatic

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"path"
	"strings"
)

//go:embed html/*.html
var files embed.FS

// 嵌入模版路径（相对本包 embed 根）。
const (
	TplEmailLoginCode      = "html/email_login_code.html"
	TplEmailVerification   = "html/email_verification.html"
	TplVerification        = "html/verification.html"
	TplPasswordReset     = "html/password_reset.html"
	TplWelcome           = "html/welcome.html"
	TplNewDeviceLogin    = "html/new_device_login.html"
	TplDeviceVerification = "html/device_verification.html"
	TplGroupInvitation   = "html/group_invitation.html"
)

// UsernameFromEmail 用邮箱 @ 前作为称呼占位。
func UsernameFromEmail(email string) string {
	email = strings.TrimSpace(strings.ToLower(email))
	i := strings.IndexByte(email, '@')
	if i <= 0 || i >= len(email)-1 {
		return ""
	}
	return email[:i]
}

// RenderHTML 读取嵌入文件并用 html/template 渲染（自动转义文本字段）。
func RenderHTML(relPath string, data any) (string, error) {
	relPath = strings.TrimPrefix(relPath, "/")
	b, err := files.ReadFile(relPath)
	if err != nil {
		return "", fmt.Errorf("mailstatic: read %q: %w", relPath, err)
	}
	name := path.Base(relPath)
	t, err := template.New(name).Parse(string(b))
	if err != nil {
		return "", fmt.Errorf("mailstatic: parse %q: %w", relPath, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("mailstatic: execute %q: %w", relPath, err)
	}
	return buf.String(), nil
}
