package i18n

import (
	"embed"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"

	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

const (
	LangZhCN = "zh-CN"
	LangZhTW = "zh-TW"
	LangEn   = "en"
	LangJa   = "ja"
	// GinContextKeyLanguage is where middleware stores the normalized Accept-Language tag.
	GinContextKeyLanguage = "lingvoice.i18n.accept_language"
	DefaultLang           = LangEn // Fallback when no match (overridable via LINGVOICE_DEFAULT_LANG)
)

//go:embed locales/zh-CN.yaml locales/zh-TW.yaml locales/en.yaml locales/ja.yaml
var localeFS embed.FS

var (
	bundle     *i18n.Bundle
	localizers = make(map[string]*i18n.Localizer)
	mu         sync.RWMutex
	initOnce   sync.Once
	// globalDefaultLang is the process-wide fallback after context/header/user resolution (env LINGVOICE_DEFAULT_LANG).
	globalDefaultLang = DefaultLang
	globalDefaultMu   sync.RWMutex
)

type UserSetting struct {
	NotifyType                       string  `json:"notify_type,omitempty"`                          // QuotaWarningType 额度预警类型
	QuotaWarningThreshold            float64 `json:"quota_warning_threshold,omitempty"`              // QuotaWarningThreshold 额度预警阈值
	WebhookUrl                       string  `json:"webhook_url,omitempty"`                          // WebhookUrl webhook地址
	WebhookSecret                    string  `json:"webhook_secret,omitempty"`                       // WebhookSecret webhook密钥
	NotificationEmail                string  `json:"notification_email,omitempty"`                   // NotificationEmail 通知邮箱地址
	BarkUrl                          string  `json:"bark_url,omitempty"`                             // BarkUrl Bark推送URL
	GotifyUrl                        string  `json:"gotify_url,omitempty"`                           // GotifyUrl Gotify服务器地址
	GotifyToken                      string  `json:"gotify_token,omitempty"`                         // GotifyToken Gotify应用令牌
	GotifyPriority                   int     `json:"gotify_priority"`                                // GotifyPriority Gotify消息优先级
	UpstreamModelUpdateNotifyEnabled bool    `json:"upstream_model_update_notify_enabled,omitempty"` // 是否接收上游模型更新定时检测通知（仅管理员）
	AcceptUnsetRatioModel            bool    `json:"accept_unset_model_ratio_model,omitempty"`       // AcceptUnsetRatioModel 是否接受未设置价格的模型
	RecordIpLog                      bool    `json:"record_ip_log,omitempty"`                        // 是否记录请求和错误日志IP
	SidebarModules                   string  `json:"sidebar_modules,omitempty"`                      // SidebarModules 左侧边栏模块配置
	BillingPreference                string  `json:"billing_preference,omitempty"`                   // BillingPreference 扣费策略（订阅/钱包）
	Language                         string  `json:"language,omitempty"`                             // Language 用户语言偏好 (zh, en)
}

var TranslateMessage func(c *gin.Context, key string, args ...map[string]any) string

func GetContextKeyType[T any](c *gin.Context, key string) (T, bool) {
	if value, ok := c.Get(string(key)); ok {
		if v, ok := value.(T); ok {
			return v, true
		}
	}
	var t T
	return t, false
}

// Init initializes the i18n bundle and loads all translation files.
// Call once at process startup (e.g. cmd/server/main.go after config load).
func Init() error {
	var initErr error
	initOnce.Do(func() {
		if v := strings.TrimSpace(os.Getenv("LINGVOICE_DEFAULT_LANG")); v != "" {
			nv := normalizeLang(v)
			if IsSupported(nv) {
				globalDefaultMu.Lock()
				globalDefaultLang = nv
				globalDefaultMu.Unlock()
			}
		}

		bundle = i18n.NewBundle(language.English)
		bundle.RegisterUnmarshalFunc("yaml", yaml.Unmarshal)

		files := []string{"locales/zh-CN.yaml", "locales/zh-TW.yaml", "locales/en.yaml", "locales/ja.yaml"}
		for _, file := range files {
			_, err := bundle.LoadMessageFileFS(localeFS, file)
			if err != nil {
				initErr = err
				return
			}
		}

		localizers[LangZhCN] = i18n.NewLocalizer(bundle, LangZhCN)
		localizers[LangZhTW] = i18n.NewLocalizer(bundle, LangZhTW)
		localizers[LangEn] = i18n.NewLocalizer(bundle, LangEn)
		localizers[LangJa] = i18n.NewLocalizer(bundle, LangJa)

		TranslateMessage = T
	})
	return initErr
}

// GlobalDefaultLang returns the configured process default (LINGVOICE_DEFAULT_LANG or built-in default).
func GlobalDefaultLang() string {
	globalDefaultMu.RLock()
	defer globalDefaultMu.RUnlock()
	return globalDefaultLang
}

// GetLocalizer returns a localizer for the specified language
func GetLocalizer(lang string) *i18n.Localizer {
	lang = normalizeLang(lang)

	mu.RLock()
	loc, ok := localizers[lang]
	mu.RUnlock()

	if ok {
		return loc
	}

	// Create new localizer for unknown language (fallback to default)
	mu.Lock()
	defer mu.Unlock()

	// Double-check after acquiring write lock
	if loc, ok = localizers[lang]; ok {
		return loc
	}

	loc = i18n.NewLocalizer(bundle, lang, DefaultLang)
	localizers[lang] = loc
	return loc
}

// T translates a message key using the language from gin context
func T(c *gin.Context, key string, args ...map[string]any) string {
	lang := GetLangFromContext(c)
	return Translate(lang, key, args...)
}

// Translate translates a message key for the specified language
func Translate(lang, key string, args ...map[string]any) string {
	loc := GetLocalizer(lang)

	config := &i18n.LocalizeConfig{
		MessageID: key,
	}

	if len(args) > 0 && args[0] != nil {
		config.TemplateData = args[0]
	}

	msg, err := loc.Localize(config)
	if err != nil {
		// Return key as fallback if translation not found
		return key
	}
	return msg
}

// userLangLoaderFunc resolves language from the active request (e.g. user profile in DB).
var userLangLoaderFunc func(c *gin.Context) string

// SetUserLangLoader registers how to load a signed-in user's preferred locale from DB/cache.
// Return "" to fall through to Accept-Language / global default.
func SetUserLangLoader(loader func(c *gin.Context) string) {
	userLangLoaderFunc = loader
}

// GetLangFromContext extracts the language setting from gin context
// It checks multiple sources in priority order:
// 1. User settings (ContextKeyUserSetting) - if already loaded (e.g., by TokenAuth)
// 2. Lazy load user language from cache/DB using user ID
// 3. Language set by middleware (ContextKeyLanguage) - from Accept-Language header
// 4. Default language (English)
func GetLangFromContext(c *gin.Context) string {
	if c == nil {
		return GlobalDefaultLang()
	}

	// 1. Try to get language from user settings (if already loaded by TokenAuth or other middleware)
	if userSetting, ok := GetContextKeyType[UserSetting](c, "user_setting"); ok {
		if userSetting.Language != "" {
			normalized := normalizeLang(userSetting.Language)
			if IsSupported(normalized) {
				return normalized
			}
		}
	}

	// 2. Lazy load from signed-in user (e.g. user_profiles.locale), via SetUserLangLoader.
	if userLangLoaderFunc != nil {
		if lang := userLangLoaderFunc(c); lang != "" {
			normalized := normalizeLang(lang)
			if IsSupported(normalized) {
				return normalized
			}
		}
	}

	// 3. Normalized tag from AcceptLanguage middleware
	if lang := c.GetString(GinContextKeyLanguage); lang != "" {
		normalized := normalizeLang(lang)
		if IsSupported(normalized) {
			return normalized
		}
	}

	// 4. Accept-Language header if middleware did not run
	if acceptLang := c.GetHeader("Accept-Language"); acceptLang != "" {
		lang := ParseAcceptLanguage(acceptLang)
		if IsSupported(lang) {
			return lang
		}
	}

	return GlobalDefaultLang()
}

// ParseAcceptLanguage parses the Accept-Language header and returns the preferred language
func ParseAcceptLanguage(header string) string {
	if header == "" {
		return GlobalDefaultLang()
	}

	parts := strings.Split(header, ",")
	if len(parts) == 0 {
		return GlobalDefaultLang()
	}

	firstLang := strings.TrimSpace(parts[0])
	if idx := strings.Index(firstLang, ";"); idx > 0 {
		firstLang = firstLang[:idx]
	}

	return normalizeLang(firstLang)
}

// normalizeLang normalizes language code to supported format
func normalizeLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return LangEn
	}

	switch {
	case strings.HasPrefix(lang, "zh-tw") || lang == "zh_hant":
		return LangZhTW
	case strings.HasPrefix(lang, "zh"):
		return LangZhCN
	case strings.HasPrefix(lang, "ja"):
		return LangJa
	case strings.HasPrefix(lang, "en"):
		return LangEn
	default:
		// Unknown BCP47 tags: use English catalog; final fallback to process default is GetLangFromContext.
		return LangEn
	}
}

// SupportedLanguages returns a list of supported language codes
func SupportedLanguages() []string {
	return []string{LangZhCN, LangZhTW, LangEn, LangJa}
}

// IsSupported checks if a language code is supported
func IsSupported(lang string) bool {
	lang = normalizeLang(lang)
	for _, supported := range SupportedLanguages() {
		if lang == supported {
			return true
		}
	}
	return false
}
