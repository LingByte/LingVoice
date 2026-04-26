// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

// Package mailtemplate holds built-in mail template metadata (codes expected by send logic).
package mailtemplate

// Preset is a suggested row for admin UI; business code loads templates by Code from DB.
type Preset struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	HTMLBody    string `json:"htmlBody"`
	Variables   string `json:"variables"` // JSON array string
}

// DefaultPresets returns known template codes and default HTML skeletons (plain text is derived from HTML on save).
func DefaultPresets() []Preset {
	return []Preset{
		{
			Code:        "welcome",
			Name:        "欢迎邮件",
			Description: "新用户注册后首封邮件（占位符按业务注入为准，可改）。",
			HTMLBody: `<p>您好 {{.UserName}}，</p>
<p>欢迎加入 <strong>{{.SiteName}}</strong>。</p>
<p><a href="{{.SignInURL}}">前往登录</a></p>`,
			Variables: `["SiteName","UserName","SignInURL"]`,
		},
		{
			Code:        "verify_code",
			Name:        "验证码邮件",
			Description: "验证码类通知。",
			HTMLBody: `<p>您好，</p>
<p>您在 <strong>{{.SiteName}}</strong> 的验证码为：</p>
<p style="font-size:20px;letter-spacing:4px;font-weight:600">{{.Code}}</p>
<p>有效期 {{.ExpireMinutes}} 分钟，请勿泄露给他人。</p>`,
			Variables: `["SiteName","Code","ExpireMinutes"]`,
		},
		{
			Code:        "password_reset",
			Name:        "重置密码",
			Description: "带重置链接的邮件。",
			HTMLBody: `<p>您好 {{.UserName}}，</p>
<p>我们收到了重置您在 <strong>{{.SiteName}}</strong> 密码的请求。</p>
<p><a href="{{.ResetURL}}">点击重置密码</a>（若无法点击请复制链接到浏览器）</p>
<p>如非本人操作，请忽略本邮件。</p>`,
			Variables: `["SiteName","UserName","ResetURL"]`,
		},
	}
}
