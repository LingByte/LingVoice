package config

// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"github.com/LingByte/lingstorage-sdk-go"
)

func init() {
	// 3. Load Global Configuration
	if err := Load(); err != nil {
		panic("config load failed: " + err.Error())
	}
}

// Config main configuration structure
type Config struct {
	MachineID  int64            `env:"MACHINE_ID"`
	Server     ServerConfig     `mapstructure:"server"`
	Database   DatabaseConfig   `mapstructure:"database"`
	Log        logger.LogConfig `mapstructure:"log"`
	Auth       AuthConfig       `mapstructure:"auth"`
	Services   ServicesConfig   `mapstructure:"services"`
	Middleware MiddlewareConfig `mapstructure:"middleware"`
	JWT        JWTConfig
}

// JWTConfig JWT related configuration
type JWTConfig struct {
	Algorithm    string `env:"JWT_ALGORITHM"`
	KeyFile      string `env:"JWT_KEY_FILE"`
	RotationDays int    `env:"JWT_ROTATION_DAYS"`
	KeepOldKeys  int    `env:"JWT_KEEP_OLD_KEYS"`
}

// ServerConfig server configuration
type ServerConfig struct {
	Name        string `env:"SERVER_NAME"`
	Desc        string `env:"SERVER_DESC"`
	URL         string `env:"SERVER_URL"`
	Logo        string `env:"SERVER_LOGO"`
	TermsURL    string `env:"SERVER_TERMS_URL"`
	Version     string `env:"SERVER_VERSION"`
	Addr        string `env:"ADDR"`
	Mode        string `env:"MODE"`
	DocsPrefix  string `env:"DOCS_PREFIX"`
	APIPrefix   string `env:"API_PREFIX"`
	WebAppURL   string `env:"WEB_APP_URL"`
	SSLEnabled  bool   `env:"SSL_ENABLED"`
	SSLCertFile string `env:"SSL_CERT_FILE"`
	SSLKeyFile  string `env:"SSL_KEY_FILE"`
}

// DatabaseConfig database configuration
type DatabaseConfig struct {
	Driver string `env:"DB_DRIVER"`
	DSN    string `env:"DSN"`
}

// AuthConfig authentication configuration
type AuthConfig struct {
	Header           string `env:"AUTH_HEADER"`
	SessionSecret    string `env:"SESSION_SECRET"`
	SecretExpireDays string `env:"SESSION_EXPIRE_DAYS"`
	APISecretKey     string `env:"API_SECRET_KEY"`
	// JWTSecret signs access tokens; empty means use APISecretKey (see JWTSigningKey).
	JWTSecret string `env:"JWT_SECRET"`
	// JWTExpireHours access token lifetime in hours (default 168 = 7d).
	JWTExpireHours int `env:"JWT_EXPIRE_HOURS"`
	// JWTRefreshSecret signs refresh tokens; empty falls back to JWTSigningKey() (see RefreshJWTSigningKey).
	JWTRefreshSecret string `env:"JWT_REFRESH_SECRET"`
	// JWTRefreshExpireHours refresh token lifetime (default 720 = 30d).
	JWTRefreshExpireHours int `env:"JWT_REFRESH_EXPIRE_HOURS"`
}

// ServicesConfig services configuration
type ServicesConfig struct {
	LLM                     LLMConfig          `mapstructure:"llm"`
	Storage                 StorageConfig      `mapstructure:"storage"`
	OpenAPIQuotaGroupRatios map[string]float64 `mapstructure:"-"`
	SpeechQuota             SpeechQuotaConfig  `mapstructure:"-"`
}

// SpeechQuotaConfig 语音 OpenAPI 扣费；时长单位为秒；rate 为每「计费秒」扣多少额度单位。
type SpeechQuotaConfig struct {
	ASRUnitsPerBillableSecond float64 `mapstructure:"-"` // OPENAPI_SPEECH_ASR_UNITS_PER_SEC，默认 1
	TTSUnitsPerBillableSecond float64 `mapstructure:"-"` // OPENAPI_SPEECH_TTS_UNITS_PER_SEC，默认 1
	// ASRInputBytesPerSec 由输入音频字节估算时长（如 32000≈16kHz 单声道 16bit PCM）；0 表示不用字节只用墙钟。
	ASRInputBytesPerSec int64 `mapstructure:"-"` // OPENAPI_SPEECH_ASR_BYTES_PER_SEC 默认 32000
	// TTSOutputBytesPerSec 由输出音频字节估算播放时长；0 表示不用输出字节只用墙钟。
	TTSOutputBytesPerSec int64 `mapstructure:"-"` // OPENAPI_SPEECH_TTS_BYTES_PER_SEC 默认 16000
	MinDeltaOnSuccess    int   `mapstructure:"-"` // OPENAPI_SPEECH_QUOTA_MIN_ON_SUCCESS 默认 1
}

// LLMConfig LLM service configuration
type LLMConfig struct {
	APIKey  string `env:"LLM_API_KEY"`
	BaseURL string `env:"LLM_BASE_URL"`
	Model   string `env:"LLM_MODEL"`
}

// StorageConfig storage configuration
type StorageConfig struct {
	BaseURL   string `env:"LINGSTORAGE_BASE_URL"`
	APIKey    string `env:"LINGSTORAGE_API_KEY"`
	APISecret string `env:"LINGSTORAGE_API_SECRET"`
	Bucket    string `env:"LINGSTORAGE_BUCKET"`
}

// MiddlewareConfig middleware configuration
type MiddlewareConfig struct {
	// Rate limiting configuration
	RateLimit RateLimiterConfig
	// Timeout configuration
	Timeout TimeoutConfig
	// Circuit breaker configuration
	CircuitBreaker CircuitBreakerConfig
	// Whether to enable each middleware
	EnableRateLimit      bool `env:"ENABLE_RATE_LIMIT"`
	EnableTimeout        bool `env:"ENABLE_TIMEOUT"`
	EnableCircuitBreaker bool `env:"ENABLE_CIRCUIT_BREAKER"`
	EnableOperationLog   bool `env:"ENABLE_OPERATION_LOG"`
}

// RateLimiterConfig rate limiting configuration
type RateLimiterConfig struct {
	GlobalRPS    int           `env:"RATE_LIMIT_GLOBAL_RPS"`   // Global requests per second
	GlobalBurst  int           `env:"RATE_LIMIT_GLOBAL_BURST"` // Global burst requests
	GlobalWindow time.Duration // Global time window
	UserRPS      int           `env:"RATE_LIMIT_USER_RPS"`   // User requests per second
	UserBurst    int           `env:"RATE_LIMIT_USER_BURST"` // User burst requests
	UserWindow   time.Duration // User time window
	IPRPS        int           `env:"RATE_LIMIT_IP_RPS"`   // IP requests per second
	IPBurst      int           `env:"RATE_LIMIT_IP_BURST"` // IP burst requests
	IPWindow     time.Duration // IP time window
}

// TimeoutConfig timeout configuration
type TimeoutConfig struct {
	DefaultTimeout   time.Duration `env:"DEFAULT_TIMEOUT"`
	FallbackResponse interface{}
}

// CircuitBreakerConfig circuit breaker configuration
type CircuitBreakerConfig struct {
	FailureThreshold      int           `env:"CIRCUIT_BREAKER_FAILURE_THRESHOLD"`
	SuccessThreshold      int           `env:"CIRCUIT_BREAKER_SUCCESS_THRESHOLD"`
	Timeout               time.Duration `env:"CIRCUIT_BREAKER_TIMEOUT"`
	OpenTimeout           time.Duration `env:"CIRCUIT_BREAKER_OPEN_TIMEOUT"`
	MaxConcurrentRequests int           `env:"CIRCUIT_BREAKER_MAX_CONCURRENT"`
}

var GlobalConfig *Config

var GlobalStore *lingstorage.Client

func Load() error {
	// 1. Load .env file based on environment (don't error if it doesn't exist, use default values)
	env := os.Getenv("MODE")
	err := base.LoadEnv(env)
	if err != nil {
		// Only log when .env file doesn't exist, don't affect startup
		log.Printf("Note: .env file not found or failed to load: %v (using default values)", err)
	}

	// 2. Load global configuration
	GlobalConfig = &Config{
		MachineID: base.GetIntEnv("MACHINE_ID"),
		Server: ServerConfig{
			Name:        getStringOrDefault("SERVER_NAME", ""),
			Desc:        getStringOrDefault("SERVER_DESC", ""),
			URL:         getStringOrDefault("SERVER_URL", ""),
			Logo:        getStringOrDefault("SERVER_LOGO", ""),
			Version:     getStringOrDefault("SERVER_VERSION", "v0.0.0"),
			TermsURL:    getStringOrDefault("SERVER_TERMS_URL", ""),
			Addr:        getStringOrDefault("ADDR", ":7070"),
			Mode:        getStringOrDefault("MODE", "development"),
			DocsPrefix:  getStringOrDefault("DOCS_PREFIX", "/api/docs"),
			APIPrefix:   getStringOrDefault("API_PREFIX", "/api"),
			WebAppURL:   getStringOrDefault("WEB_APP_URL", ""),
			SSLEnabled:  getBoolOrDefault("SSL_ENABLED", false),
			SSLCertFile: getStringOrDefault("SSL_CERT_FILE", ""),
			SSLKeyFile:  getStringOrDefault("SSL_KEY_FILE", ""),
		},
		Database: DatabaseConfig{
			Driver: getStringOrDefault("DB_DRIVER", "sqlite"),
			DSN:    getStringOrDefault("DSN", "./ling.db"),
		},
		Log: logger.LogConfig{
			Level:      getStringOrDefault("LOG_LEVEL", "info"),
			Filename:   getStringOrDefault("LOG_FILENAME", "./logs/app.log"),
			MaxSize:    getIntOrDefault("LOG_MAX_SIZE", 100),
			MaxAge:     getIntOrDefault("LOG_MAX_AGE", 30),
			MaxBackups: getIntOrDefault("LOG_MAX_BACKUPS", 5),
			Daily:      getBoolOrDefault("LOG_DAILY", true),
		},
		Auth: AuthConfig{
			Header:                getStringOrDefault("AUTH_HEADER", "Authorization"),
			SessionSecret:         getStringOrDefault("SESSION_SECRET", generateDefaultSessionSecret()),
			SecretExpireDays:      getStringOrDefault("SESSION_EXPIRE_DAYS", "7"),
			APISecretKey:          getStringOrDefault("API_SECRET_KEY", generateDefaultSessionSecret()),
			JWTSecret:             getStringOrDefault("JWT_SECRET", ""),
			JWTExpireHours:        getIntOrDefault("JWT_EXPIRE_HOURS", 24),
			JWTRefreshSecret:      getStringOrDefault("JWT_REFRESH_SECRET", ""),
			JWTRefreshExpireHours: getIntOrDefault("JWT_REFRESH_EXPIRE_HOURS", 720),
		},
		Services: ServicesConfig{
			LLM: LLMConfig{
				APIKey:  getStringOrDefault("LLM_API_KEY", ""),
				BaseURL: getStringOrDefault("LLM_BASE_URL", "https://api.openai.com/v1"),
				Model:   getStringOrDefault("LLM_MODEL", "gpt-3.5-turbo"),
			},
			Storage: StorageConfig{
				BaseURL:   getStringOrDefault("LINGSTORAGE_BASE_URL", "https://api.lingstorage.com"),
				APIKey:    getStringOrDefault("LINGSTORAGE_API_KEY", ""),
				APISecret: getStringOrDefault("LINGSTORAGE_API_SECRET", ""),
				Bucket:    getStringOrDefault("LINGSTORAGE_BUCKET", "default"),
			},
			OpenAPIQuotaGroupRatios: parseOpenAPIQuotaGroupRatiosJSON(getStringOrDefault("OPENAPI_QUOTA_GROUP_RATIOS", "")),
			SpeechQuota: SpeechQuotaConfig{
				ASRUnitsPerBillableSecond: getFloatOrDefault("OPENAPI_SPEECH_ASR_UNITS_PER_SEC", 1),
				TTSUnitsPerBillableSecond: getFloatOrDefault("OPENAPI_SPEECH_TTS_UNITS_PER_SEC", 1),
				ASRInputBytesPerSec:       getInt64EnvParsed("OPENAPI_SPEECH_ASR_BYTES_PER_SEC", 32000),
				TTSOutputBytesPerSec:      getInt64EnvParsed("OPENAPI_SPEECH_TTS_BYTES_PER_SEC", 16000),
				MinDeltaOnSuccess:         getIntOrDefault("OPENAPI_SPEECH_QUOTA_MIN_ON_SUCCESS", 1),
			},
		},
		Middleware: loadMiddlewareConfig(),
		JWT: JWTConfig{
			Algorithm:    getStringOrDefault("JWT_ALGORITHM", "RS256"),
			KeyFile:      getStringOrDefault("JWT_KEY_FILE", "./keys/jwks.json"),
			RotationDays: getIntOrDefault("JWT_ROTATION_DAYS", 30),
			KeepOldKeys:  getIntOrDefault("JWT_KEEP_OLD_KEYS", 2),
		},
	}
	GlobalStore = lingstorage.NewClient(&lingstorage.Config{
		BaseURL:   GlobalConfig.Services.Storage.BaseURL,
		APIKey:    GlobalConfig.Services.Storage.APIKey,
		APISecret: GlobalConfig.Services.Storage.APISecret,
	})

	return nil
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate database configuration
	if c.Database.DSN == "" {
		return errors.New("database DSN is required")
	}

	// Validate server configuration
	if c.Server.Addr == "" {
		return errors.New("server address is required")
	}

	return nil
}

// getStringOrDefault gets environment variable value, returns default if empty
func getStringOrDefault(key, defaultValue string) string {
	value := base.GetEnv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// getBoolOrDefault gets boolean environment variable value, returns default if empty
func getBoolOrDefault(key string, defaultValue bool) bool {
	value := base.GetEnv(key)
	if value == "" {
		return defaultValue
	}
	return base.GetBoolEnv(key)
}

// getIntOrDefault gets integer environment variable value, returns default if empty
func getIntOrDefault(key string, defaultValue int) int {
	value := base.GetIntEnv(key)
	if value == 0 {
		return defaultValue
	}
	return int(value)
}

// getInt64EnvParsed parses int64 from env; empty string returns default; allows explicit 0 (unlike getIntOrDefault).
func getInt64EnvParsed(key string, defaultValue int64) int64 {
	s := strings.TrimSpace(base.GetEnv(key))
	if s == "" {
		return defaultValue
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return defaultValue
	}
	return n
}

// getFloatOrDefault gets float environment variable value, returns default if empty
func getFloatOrDefault(key string, defaultValue float64) float64 {
	value := base.GetEnv(key)
	if value == "" {
		return defaultValue
	}
	// 简单的字符串到float64转换
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f
	}
	return defaultValue
}

// parseDuration parses duration string with default fallback
func parseDuration(s string, defaultVal time.Duration) time.Duration {
	if s == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return d
}

// generateDefaultSessionSecret returns SESSION_SECRET from env when set; otherwise a default.
// Non-production uses a stable default so local restarts do not invalidate existing session cookies.
func generateDefaultSessionSecret() string {
	if secret := base.GetEnv("SESSION_SECRET"); secret != "" {
		return secret
	}
	mode := strings.ToLower(strings.TrimSpace(base.GetEnv("MODE")))
	if mode == "production" {
		return "default-secret-key-change-in-production-" + base.RandText(16)
	}
	return "default-dev-session-secret-not-for-production"
}

// loadMiddlewareConfig loads middleware configuration
func loadMiddlewareConfig() MiddlewareConfig {
	mode := getStringOrDefault("MODE", "development")
	var defaultConfig MiddlewareConfig

	if mode == "production" {
		defaultConfig = MiddlewareConfig{
			RateLimit: RateLimiterConfig{
				GlobalRPS:    2000,
				GlobalBurst:  4000,
				GlobalWindow: time.Minute,
				UserRPS:      200,
				UserBurst:    400,
				UserWindow:   time.Minute,
				IPRPS:        100,
				IPBurst:      200,
				IPWindow:     time.Minute,
			},
			Timeout: TimeoutConfig{
				DefaultTimeout: 30 * time.Second,
				FallbackResponse: map[string]interface{}{
					"error":   "service_unavailable",
					"message": "Service temporarily unavailable, please try again later",
					"code":    503,
				},
			},
			CircuitBreaker: CircuitBreakerConfig{
				FailureThreshold:      3,
				SuccessThreshold:      2,
				Timeout:               30 * time.Second,
				OpenTimeout:           30 * time.Second,
				MaxConcurrentRequests: 200,
			},
			EnableRateLimit:      true,
			EnableTimeout:        true,
			EnableCircuitBreaker: true,
			EnableOperationLog:   true,
		}
	} else {
		defaultConfig = MiddlewareConfig{
			RateLimit: RateLimiterConfig{
				GlobalRPS:    10000,
				GlobalBurst:  20000,
				GlobalWindow: time.Minute,
				UserRPS:      1000,
				UserBurst:    2000,
				UserWindow:   time.Minute,
				IPRPS:        500,
				IPBurst:      1000,
				IPWindow:     time.Minute,
			},
			Timeout: TimeoutConfig{
				DefaultTimeout: 60 * time.Second,
				FallbackResponse: map[string]interface{}{
					"error":   "service_unavailable",
					"message": "Service temporarily unavailable, please try again later",
					"code":    503,
				},
			},
			CircuitBreaker: CircuitBreakerConfig{
				FailureThreshold:      10,
				SuccessThreshold:      5,
				Timeout:               60 * time.Second,
				OpenTimeout:           60 * time.Second,
				MaxConcurrentRequests: 1000,
			},
			EnableRateLimit:      true,
			EnableTimeout:        true,
			EnableCircuitBreaker: false,
			EnableOperationLog:   true,
		}
	}
	return MiddlewareConfig{
		RateLimit: RateLimiterConfig{
			GlobalRPS:    getIntOrDefault("RATE_LIMIT_GLOBAL_RPS", defaultConfig.RateLimit.GlobalRPS),
			GlobalBurst:  getIntOrDefault("RATE_LIMIT_GLOBAL_BURST", defaultConfig.RateLimit.GlobalBurst),
			GlobalWindow: parseDuration(getStringOrDefault("RATE_LIMIT_GLOBAL_WINDOW", "1m"), defaultConfig.RateLimit.GlobalWindow),
			UserRPS:      getIntOrDefault("RATE_LIMIT_USER_RPS", defaultConfig.RateLimit.UserRPS),
			UserBurst:    getIntOrDefault("RATE_LIMIT_USER_BURST", defaultConfig.RateLimit.UserBurst),
			UserWindow:   parseDuration(getStringOrDefault("RATE_LIMIT_USER_WINDOW", "1m"), defaultConfig.RateLimit.UserWindow),
			IPRPS:        getIntOrDefault("RATE_LIMIT_IP_RPS", defaultConfig.RateLimit.IPRPS),
			IPBurst:      getIntOrDefault("RATE_LIMIT_IP_BURST", defaultConfig.RateLimit.IPBurst),
			IPWindow:     parseDuration(getStringOrDefault("RATE_LIMIT_IP_WINDOW", "1m"), defaultConfig.RateLimit.IPWindow),
		},
		Timeout: TimeoutConfig{
			DefaultTimeout:   parseDuration(getStringOrDefault("DEFAULT_TIMEOUT", "30s"), defaultConfig.Timeout.DefaultTimeout),
			FallbackResponse: defaultConfig.Timeout.FallbackResponse,
		},
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold:      getIntOrDefault("CIRCUIT_BREAKER_FAILURE_THRESHOLD", defaultConfig.CircuitBreaker.FailureThreshold),
			SuccessThreshold:      getIntOrDefault("CIRCUIT_BREAKER_SUCCESS_THRESHOLD", defaultConfig.CircuitBreaker.SuccessThreshold),
			Timeout:               parseDuration(getStringOrDefault("CIRCUIT_BREAKER_TIMEOUT", "30s"), defaultConfig.CircuitBreaker.Timeout),
			OpenTimeout:           parseDuration(getStringOrDefault("CIRCUIT_BREAKER_OPEN_TIMEOUT", "30s"), defaultConfig.CircuitBreaker.OpenTimeout),
			MaxConcurrentRequests: getIntOrDefault("CIRCUIT_BREAKER_MAX_CONCURRENT", defaultConfig.CircuitBreaker.MaxConcurrentRequests),
		},
		EnableRateLimit:      getBoolOrDefault("ENABLE_RATE_LIMIT", defaultConfig.EnableRateLimit),
		EnableTimeout:        getBoolOrDefault("ENABLE_TIMEOUT", defaultConfig.EnableTimeout),
		EnableCircuitBreaker: getBoolOrDefault("ENABLE_CIRCUIT_BREAKER", defaultConfig.EnableCircuitBreaker),
		EnableOperationLog:   getBoolOrDefault("ENABLE_OPERATION_LOG", defaultConfig.EnableOperationLog),
	}
}

// JWTSigningKey returns JWT_SECRET when set; otherwise API_SECRET_KEY.
func (a *AuthConfig) JWTSigningKey() string {
	if a == nil {
		return ""
	}
	s := strings.TrimSpace(a.JWTSecret)
	if s != "" {
		return s
	}
	return a.APISecretKey
}

// AccessTokenTTL returns a positive duration for access JWT lifetime.
func (a *AuthConfig) AccessTokenTTL() time.Duration {
	if a == nil {
		return 24 * time.Hour
	}
	h := a.JWTExpireHours
	if h <= 0 {
		h = 24
	}
	return time.Duration(h) * time.Hour
}

// RefreshJWTSigningKey prefers JWT_REFRESH_SECRET; otherwise JWTSigningKey().
func (a *AuthConfig) RefreshJWTSigningKey() string {
	if a == nil {
		return ""
	}
	s := strings.TrimSpace(a.JWTRefreshSecret)
	if s != "" {
		return s
	}
	return a.JWTSigningKey()
}

// RefreshTokenTTL returns refresh JWT lifetime.
func (a *AuthConfig) RefreshTokenTTL() time.Duration {
	if a == nil {
		return 720 * time.Hour
	}
	h := a.JWTRefreshExpireHours
	if h <= 0 {
		h = 720
	}
	return time.Duration(h) * time.Hour
}

func parseOpenAPIQuotaGroupRatiosJSON(raw string) map[string]float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]float64
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		log.Printf("OPENAPI_QUOTA_GROUP_RATIOS: invalid JSON, ignored: %v", err)
		return nil
	}
	return m
}

// OpenAPIQuotaGroupRatio returns the billing multiplier for an API credential group (new-api-style 分组倍率). Defaults to 1.
func (c *Config) OpenAPIQuotaGroupRatio(group string) float64 {
	if c == nil {
		return 1
	}
	m := c.Services.OpenAPIQuotaGroupRatios
	if len(m) == 0 {
		return 1
	}
	g := strings.TrimSpace(group)
	if g == "" {
		g = "default"
	}
	if v, ok := m[g]; ok && v > 0 {
		return v
	}
	if v, ok := m["default"]; ok && v > 0 {
		return v
	}
	return 1
}
