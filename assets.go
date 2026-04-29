package LingVoice

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	texttemplate "text/template"

	"github.com/gin-gonic/gin"
)

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

//go:embed templates/html/*.html
var files embed.FS

//go:embed all:web/dist
var EmbedWebAssets embed.FS

//go:embed templates/not_found.html
var NotFoundHTML string

type EmbedFS struct {
	EmbedRoot string
	Embedfs   embed.FS
}

type CombineEmbedFS struct {
	embeds    []EmbedFS
	assertDir string
}

func (c *CombineEmbedFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}

	if c.assertDir != "" {
		f, err := os.Open(filepath.Join(c.assertDir, name))
		if err == nil {
			return f, nil
		}
	}

	var lastErr error
	for _, efs := range c.embeds {
		p := path.Join(efs.EmbedRoot, name)
		f, err := efs.Embedfs.Open(p)
		if err == nil {
			return f, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fs.ErrNotExist
	}
	return nil, lastErr
}

func NewCombineEmbedFS(assertDir string, es ...EmbedFS) *CombineEmbedFS {
	return &CombineEmbedFS{
		embeds:    es,
		assertDir: assertDir,
	}
}

func HintAssetsRoot(dirName string) string {
	for _, dir := range []string{".", ".."} {
		testDirName := filepath.Join(os.ExpandEnv(dir), dirName)
		st, err := os.Stat(testDirName)
		if err == nil && st.IsDir() {
			return testDirName
		}
	}
	return ""
}

func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || p == "/api" || strings.HasPrefix(p, "/v1/") || p == "/v1"
}

// Mount 挂载 Vite 产物根（含 index.html、assets/）。不可使用 StaticFS("", …)，会与 /api 等根前缀冲突。
// Note: Mount no longer sets NoRoute. Use WebFallback(dist, ...) explicitly to control 404 behavior.
func Mount(r *gin.Engine, dist fs.FS) {
	indexHTML, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		panic("webembed: missing index.html in dist fs: " + err.Error())
	}

	serveIndex := func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	}
	serveIndexHead := func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.Header("Content-Length", strconv.Itoa(len(indexHTML)))
		c.Status(http.StatusOK)
	}

	if assetsSub, err := fs.Sub(dist, "assets"); err == nil {
		r.StaticFS("/assets", http.FS(assetsSub))
	}

	r.GET("/", serveIndex)
	r.HEAD("/", serveIndexHead)
}

func tryServeDistFile(c *gin.Context, dist fs.FS, urlPath string) bool {
	rel := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if rel == "" || rel == "." {
		return false
	}
	rel = path.Clean(rel)
	if strings.Contains(rel, "..") {
		return false
	}
	b, err := fs.ReadFile(dist, rel)
	if err != nil {
		return false
	}
	ext := path.Ext(rel)
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Data(http.StatusOK, ct, b)
	return true
}

// WebFallback returns a gin.NoRoute handler for embedded web assets.
//
// - For GET/HEAD non-API paths: serves dist files when present; otherwise serves index.html (SPA fallback).
// - For API paths ("/api", "/v1"): delegates to apiFallback when provided, otherwise returns {"error":"not found"}.
// - For non-GET/HEAD: always 404.
func WebFallback(dist fs.FS, apiFallback gin.HandlerFunc) gin.HandlerFunc {
	indexHTML, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		panic("webembed: missing index.html in dist fs: " + err.Error())
	}
	serveIndex := func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	}
	serveIndexHead := func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.Header("Content-Length", strconv.Itoa(len(indexHTML)))
		c.Status(http.StatusOK)
	}
	defaultAPIFallback := func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	}
	if apiFallback == nil {
		apiFallback = defaultAPIFallback
	}

	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusNotFound)
			return
		}
		p := c.Request.URL.Path
		if isAPIPath(p) {
			apiFallback(c)
			return
		}
		if tryServeDistFile(c, dist, p) {
			return
		}
		if c.Request.Method == http.MethodHead {
			serveIndexHead(c)
			return
		}
		serveIndex(c)
	}
}

// RenderNotFoundPage 渲染 404 页面
func RenderNotFoundPage(c *gin.Context, path, method string) {
	tmpl, err := template.New("404").Parse(NotFoundHTML)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":  "Page not found",
			"path":   path,
			"method": method,
		})
		return
	}

	data := map[string]string{
		"path":   path,
		"method": method,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":  "Page not found",
			"path":   path,
			"method": method,
		})
		return
	}
	c.Data(http.StatusNotFound, "text/html; charset=utf-8", buf.Bytes())
}

// 嵌入模版路径（相对本包 embed 根）。
const (
	TplEmailLoginCode     = "html/email_login_code.html"
	TplEmailVerification  = "html/email_verification.html"
	TplVerification       = "html/verification.html"
	TplPasswordReset      = "html/password_reset.html"
	TplWelcome            = "html/welcome.html"
	TplNewDeviceLogin     = "html/new_device_login.html"
	TplDeviceVerification = "html/device_verification.html"
	TplGroupInvitation    = "html/group_invitation.html"
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
