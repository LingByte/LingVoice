package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice"
	"github.com/LingByte/LingVoice/cmd/bootstrap"
	"github.com/LingByte/LingVoice/i18n"
	"github.com/LingByte/LingVoice/internal/config"
	"github.com/LingByte/LingVoice/internal/handlers"
	"github.com/LingByte/LingVoice/internal/listeners"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/constants"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/LingByte/LingVoice/pkg/notification/mail"
	"github.com/LingByte/LingVoice/pkg/notification/sms"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Copyright (c) 2026 LingByte
// SPDX-License-Identifier: MIT

type LingEchoApp struct {
	db       *gorm.DB
	handlers *handlers.Handlers
}

func NewLingEchoApp(db *gorm.DB) *LingEchoApp {
	return &LingEchoApp{
		db:       db,
		handlers: handlers.NewHandlers(db),
	}
}

func (app *LingEchoApp) RegisterRoutes(r *gin.Engine) {
	// Register system routes (with /api prefix)
	app.handlers.Register(r)
}

func main() {
	// 1. Parse Command Line Parameters
	init := flag.Bool("init", false, "deprecated: ignored; schema migration always runs at startup")
	seed := flag.Bool("seed", false, "seed database")
	mode := flag.String("mode", "", "running environment (development, test, production)")
	initSQL := flag.String("init-sql", "", "path to database init .sql script (optional)")
	flag.Parse()
	// 2. Set Environment Variables
	if *mode != "" {
		os.Setenv("MODE", *mode)
	}

	// 4. Load Log Configuration
	err := logger.Init(&config.GlobalConfig.Log, config.GlobalConfig.Server.Mode)
	if err != nil {
		panic(err)
	}

	// 5. Print Banner
	if err := bootstrap.PrintBannerFromFile("banner.txt", config.GlobalConfig.Server.Name); err != nil {
		log.Fatalf("unload banner: %v", err)
	}

	// 6. Print Configuration
	bootstrap.LogConfigInfo()

	// 7. Load Data Source
	db, err := bootstrap.SetupDatabase(os.Stdout, &bootstrap.Options{
		InitSQLPath: *initSQL,
		AutoMigrate: *init,
		SeedNonProd: *seed,
		MigrateModels: func() []any {
			return []any{
				&base.Config{},
				&mail.MailLog{},
				&sms.SMSLog{},
				&models.Organization{},
				&models.OrgMember{},
				&models.MailTemplate{},
				&models.InternalNotification{},
				&models.NotificationChannel{},
				&models.LLMChannel{},
				&models.LLMAbility{},
				&models.LLMModelMeta{},
				&models.Announcement{},
				&models.ASRChannel{},
				&models.TTSChannel{},
				&models.Credential{},
				&models.User{},
				&models.UserProfile{},
				&models.ChatSession{},
				&models.ChatMessage{},
				&models.LLMUsage{},
				&models.LLMUsageUserDaily{},
				&models.LLMUsageUserModelDaily{},
				&models.SpeechUsage{},
				&models.AgentRun{},
				&models.AgentStep{},
				&models.KnowledgeNamespace{},
				&models.KnowledgeDocument{},
			}
		},
	})
	if err != nil {
		logger.Error("database setup failed", zap.Error(err))
		return
	}
	listeners.InitApplicationListeners(db, zap.L())

	if err := i18n.Init(); err != nil {
		logger.Error("i18n init failed", zap.Error(err))
		return
	}
	i18n.SetUserLangLoader(func(c *gin.Context) string {
		if c == nil {
			return ""
		}
		u := models.CurrentUser(c)
		if u == nil {
			return ""
		}
		var p models.UserProfile
		if err := db.Select("locale").Where("user_id = ?", u.ID).First(&p).Error; err != nil {
			return ""
		}
		return strings.TrimSpace(p.Locale)
	})

	if err := bootstrap.InitializeKeyManager(); err != nil {
		logger.Error("key manager initialization failed", zap.Error(err))
		return
	}

	// 8. Load Base Configs
	var addr = config.GlobalConfig.Server.Addr
	if addr == "" {
		addr = ":7070"
	}

	var DBDriver = config.GlobalConfig.Database.Driver
	if DBDriver == "" {
		DBDriver = "sqlite"
	}

	var DSN = config.GlobalConfig.Database.DSN
	if DSN == "" {
		DSN = "file::memory:?cache=shared"
	}
	flag.StringVar(&addr, "addr", addr, "HTTP Serve address")
	flag.StringVar(&DBDriver, "db-driver", DBDriver, "database driver")
	flag.StringVar(&DSN, "dsn", DSN, "database source name")
	//// 11. New App
	app := NewLingEchoApp(db)
	// 15. Initialize Gin Routing
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()        // Use gin.New() instead of gin.Default() to avoid automatic redirects
	r.Use(gin.Recovery()) // Manually add Recovery middleware
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.MaxMultipartMemory = 32 << 20 // 32 MB
	secret := strings.TrimSpace(config.GlobalConfig.Auth.SessionSecret)
	expireDays, _ := strconv.Atoi(strings.TrimSpace(config.GlobalConfig.Auth.SecretExpireDays))
	if expireDays <= 0 {
		expireDays = 7
	}
	r.Use(middleware.WithCookieSession(secret, expireDays*24*3600))

	// Cors Handle Middleware
	r.Use(middleware.CorsMiddleware())

	// Logger Handle Middleware
	r.Use(middleware.LoggerMiddleware(zap.L()))

	r.Use(middleware.AcceptLanguage())

	// 18. Register Routes
	app.RegisterRoutes(r)

	webAssets := LingVoice.NewCombineEmbedFS(
		LingVoice.HintAssetsRoot("web/dist"),
		LingVoice.EmbedFS{EmbedRoot: "web/dist", Embedfs: LingVoice.EmbedWebAssets},
	)
	LingVoice.Mount(r, webAssets)
	r.NoRoute(LingVoice.WebFallback(webAssets, func(c *gin.Context) {
		LingVoice.RenderNotFoundPage(c, c.Request.URL.Path, c.Request.Method)
	}))
	logger.Info("already embed static resource for CombineEmbedFS")

	// 19. Emit system initialization signal
	base.Sig().Emit(constants.SigInitSystemConfig, nil)

	httpServer := &http.Server{
		Addr:           addr,
		Handler:        r,
		ReadTimeout:    120 * time.Second,
		WriteTimeout:   300 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	if config.GlobalConfig.Server.SSLEnabled && listeners.IsSSLEnabled() {
		tlsConfig, err := listeners.GetTLSConfig()
		if err != nil {
			logger.Error("failed to get TLS config", zap.Error(err))
			return
		}
		if tlsConfig != nil {
			httpServer.TLSConfig = tlsConfig
		} else {
			logger.Warn("SSL enabled but TLS config is nil, falling back to HTTP")
		}
	}

	if httpServer.TLSConfig != nil {
		logger.Info("LingVoice HTTPS", zap.String("addr", addr))
		if err := httpServer.ListenAndServeTLS(config.GlobalConfig.Server.SSLKeyFile, config.GlobalConfig.Server.SSLCertFile); err != nil {
			logger.Error("user-service failed", zap.Error(err))
		}
		return
	}

	logger.Info("LingVoice HTTP", zap.String("addr", addr))
	if err := httpServer.ListenAndServe(); err != nil {
		logger.Error("LingVoice failed", zap.Error(err))
	}

}
