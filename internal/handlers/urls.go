// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handlers struct {
	db *gorm.DB
}

func NewHandlers(db *gorm.DB) *Handlers {
	return &Handlers{
		db: db,
	}
}

func (h *Handlers) Register(engine *gin.Engine) {
	engine.Use(middleware.InjectDB(h.db))

	api := engine.Group("/api")

	nc := api.Group("/notification-channels")
	{
		nc.GET("", h.listNotificationChannels)
		nc.POST("", h.createNotificationChannel)
		nc.GET("/:id", h.getNotificationChannel)
		nc.PUT("/:id", h.updateNotificationChannel)
		nc.DELETE("/:id", h.deleteNotificationChannel)
	}

	mt := api.Group("/mail-templates")
	{
		mt.POST("/translate", h.translateMailTemplate)
		mt.GET("/presets", h.listMailTemplatePresets)
		mt.GET("", h.listMailTemplates)
		mt.POST("", h.createMailTemplate)
		mt.GET("/:id", h.getMailTemplate)
		mt.PUT("/:id", h.updateMailTemplate)
		mt.DELETE("/:id", h.deleteMailTemplate)
	}

	ml := api.Group("/mail-logs")
	{
		ml.GET("", h.listMailLogs)
		ml.POST("", h.createMailLog)
		ml.GET("/:id", h.getMailLog)
		ml.PUT("/:id", h.updateMailLog)
		ml.DELETE("/:id", h.deleteMailLog)
	}

	in := api.Group("/internal-notifications")
	in.Use(models.AuthRequired)
	{
		in.GET("", h.listInternalNotifications)
		in.POST("", h.createInternalNotification)
		in.GET("/:id", h.getInternalNotification)
		in.PUT("/:id", h.updateInternalNotification)
		in.PATCH("/:id/read", h.markInternalNotificationRead)
		in.DELETE("/:id", h.deleteInternalNotification)
	}
	h.registerAuthRoutes(api)
}

func (h *Handlers) registerAuthRoutes(api *gin.RouterGroup) {
	auth := api.Group("/auth")
	{
		auth.GET("/me", models.AuthRequired, h.getAuthMe)
		auth.POST("/send-verify-email", h.postSendVerifyEmail)
		auth.POST("/verify-email-login", h.postVerifyEmailLogin)
		auth.POST("/login", h.postLogin)
		auth.POST("/register", h.postRegister)
		auth.POST("/refresh", h.postRefresh)
		auth.POST("/logout", models.AuthRequired, h.postLogout)
	}
}
