package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/LingByte/LingVoice/cmd/bootstrap"
	"github.com/LingByte/LingVoice/internal/handlers"
	"github.com/LingByte/LingVoice/internal/listeners"
	"github.com/LingByte/LingVoice/internal/migrations"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/config"
	"github.com/LingByte/LingVoice/pkg/constants"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/LingByte/LingVoice/pkg/notification"
	"github.com/LingByte/LingVoice/pkg/utils"
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
				utils.Config{},
				notification.MailLog{},
				models.MailTemplate{},
				models.InternalNotification{},
				models.NotificationChannel{},
				models.LLMChannel{},
				models.ASRChannel{},
				models.TTSChannel{},
				models.Credential{},
				models.User{},
				&models.ChatSession{},
				&models.ChatMessage{},
				&models.LLMUsage{},
				&models.AgentRun{},
				&models.AgentStep{},
			}
		},
	})

	if err != nil {
		logger.Error("database setup failed", zap.Error(err))
		return
	}
	listeners.InitApplicationListeners(db, zap.L())
	migrations.DropMailTemplateSubjectTplColumn(db)
	migrations.DropLLMUsageSessionIDColumn(db)

	// 8. Load Base Configs
	var addr = config.GlobalConfig.Server.Addr
	if addr == "" {
		addr = ":8082"
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

	// Cookie Register
	secret := utils.GetEnv(constants.ENV_SESSION_SECRET)
	if secret != "" {
		expireDays := utils.GetIntEnv(constants.ENV_SESSION_EXPIRE_DAYS)
		if expireDays <= 0 {
			expireDays = 7
		}
		r.Use(middleware.WithCookieSession(secret, int(expireDays)*24*3600))
	} else {
		r.Use(middleware.WithMemSession(utils.RandText(32)))
	}

	// Cors Handle Middleware
	r.Use(middleware.CorsMiddleware())

	// Logger Handle Middleware
	r.Use(middleware.LoggerMiddleware(zap.L()))

	// 18. Register Routes
	app.RegisterRoutes(r)

	// 21. Emit system initialization signal
	utils.Sig().Emit(constants.SigInitSystemConfig, nil)

	httpServer := &http.Server{
		Addr:           addr,
		Handler:        r,
		ReadTimeout:    120 * time.Second,
		WriteTimeout:   300 * time.Second, // LLM / OpenAPI 流式响应需较长写窗口
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
		logger.Info("user-service HTTPS", zap.String("addr", addr))
		if err := httpServer.ListenAndServeTLS("", ""); err != nil {
			logger.Error("user-service failed", zap.Error(err))
		}
		return
	}

	logger.Info("user-service HTTP", zap.String("addr", addr))
	if err := httpServer.ListenAndServe(); err != nil {
		logger.Error("user-service failed", zap.Error(err))
	}

}
