// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package utils

import (
	"bytes"
	"html/template"
	texttemplate "text/template"
)

// RenderMailHTML 使用 html/template 渲染邮件 HTML（占位符与模版中 {{.Key}} 一致，自动转义值）。
func RenderMailHTML(tplStr string, data map[string]any) (string, error) {
	if data == nil {
		data = map[string]any{}
	}
	t, err := template.New("mail_html").Option("missingkey=zero").Parse(tplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RenderMailText 使用 text/template 渲染纯文本主题等（占位符 {{.Key}}；未传键按 zero 处理）。
func RenderMailText(tplStr string, data map[string]any) (string, error) {
	if data == nil {
		data = map[string]any{}
	}
	t, err := texttemplate.New("mail_text").Option("missingkey=zero").Parse(tplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
