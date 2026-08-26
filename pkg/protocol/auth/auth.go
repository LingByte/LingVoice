// Package auth provides token/signature authentication middleware for HTTP APIs.
//
// 支持两种鉴权方式：
//   - Token:  ?token=xxx  或  Authorization: Bearer xxx
//   - 签名:   ?sign=md5(path + timestamp + secret)  &ts=xxx
//
// 用法：
//
//	authCfg := auth.Config{Mode: auth.ModeToken, Tokens: map[string]bool{"my-secret": true}}
//	handler := auth.Middleware(authCfg, apiServer.Handler())
//	http.ListenAndServe(":8090", handler)
package auth

import (
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Mode 鉴权模式
type Mode int

const (
	ModeDisabled Mode = iota // 不鉴权
	ModeToken                // Token 鉴权
	ModeSign                 // 签名鉴权
)

// Config 鉴权配置
type Config struct {
	Mode       Mode
	Tokens     map[string]bool // 允许的 token 列表
	Secret     string          // 签名密钥
	MaxSkewSec int64           // 签名时间戳最大偏移（秒），默认 300
}

// DefaultConfig 默认配置（不鉴权）
func DefaultConfig() Config {
	return Config{Mode: ModeDisabled, MaxSkewSec: 300}
}

// Middleware 鉴权中间件
func Middleware(cfg Config, next http.Handler) http.Handler {
	if cfg.Mode == ModeDisabled {
		return next
	}

	if cfg.MaxSkewSec == 0 {
		cfg.MaxSkewSec = 300
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := false
		switch cfg.Mode {
		case ModeToken:
			ok = checkToken(cfg, r)
		case ModeSign:
			ok = checkSign(cfg, r)
		}

		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer realm=\"LingVoice\"")
			http.Error(w, `{"success":false,"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// checkToken 校验 token
func checkToken(cfg Config, r *http.Request) bool {
	// 优先从 Authorization header 读取
	token := r.Header.Get("Authorization")
	if strings.HasPrefix(token, "Bearer ") {
		token = strings.TrimPrefix(token, "Bearer ")
	} else {
		// 从 query param 读取
		token = r.URL.Query().Get("token")
	}

	if token == "" {
		return false
	}

	return cfg.Tokens[token]
}

// checkSign 校验签名
// 签名规则: md5(path + timestamp + secret)
func checkSign(cfg Config, r *http.Request) bool {
	sign := r.URL.Query().Get("sign")
	tsStr := r.URL.Query().Get("ts")
	if sign == "" || tsStr == "" {
		return false
	}

	// 校验时间戳
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return false
	}
	now := time.Now().Unix()
	if now-ts > cfg.MaxSkewSec || ts-now > cfg.MaxSkewSec {
		return false
	}

	// 计算期望签名
	h := md5.Sum([]byte(r.URL.Path + tsStr + cfg.Secret))
	expected := hex.EncodeToString(h[:])

	return sign == expected
}
