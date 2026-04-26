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

	// OpenAI 兼容网关：根路径 /v1（与 new-api 一致），不经 /api 前缀
	h.registerV1RelayRoutes(engine)

	api := engine.Group("/api")

	llmCat := api.Group("/llm-channels")
	llmCat.Use(models.AuthRequired)
	{
		llmCat.GET("/catalog", h.listLLMChannelsCatalog)
	}
	llm := api.Group("/llm-channels")
	llm.Use(models.AuthRequired, models.AdminRequired)
	{
		llm.GET("", h.listLLMChannels)
		llm.POST("", h.createLLMChannel)
		llm.GET("/:id", h.getLLMChannel)
		llm.PUT("/:id", h.updateLLMChannel)
		llm.DELETE("/:id", h.deleteLLMChannel)
	}
	la := api.Group("/llm-abilities")
	la.Use(models.AuthRequired, models.AdminRequired)
	{
		la.GET("", h.listLLMAbilities)
		la.POST("", h.createLLMAbility)
		la.PATCH("", h.patchLLMAbility)
		la.DELETE("", h.deleteLLMAbility)
		la.POST("/sync-channel/:id", h.postLLMAbilitiesSyncChannel)
	}
	lm := api.Group("/llm-model-metas")
	lm.Use(models.AuthRequired, models.AdminRequired)
	{
		lm.GET("", h.listLLMModelMetas)
		lm.POST("", h.createLLMModelMeta)
		lm.GET("/:id", h.getLLMModelMeta)
		lm.PUT("/:id", h.updateLLMModelMeta)
		lm.DELETE("/:id", h.deleteLLMModelMeta)
	}
	ladm := api.Group("/llm-admin")
	ladm.Use(models.AuthRequired, models.AdminRequired)
	{
		ladm.GET("/form-options", h.getLLMAdminFormOptions)
	}
	lp := api.Group("/llm-model-plaza")
	lp.Use(models.AuthRequired)
	{
		lp.GET("", h.listLLMModelPlaza)
	}

	site := api.Group("/site")
	{
		site.GET("/announcements", h.listPublicAnnouncements)
	}
	asr := api.Group("/asr-channels")
	{
		asr.GET("", h.listASRChannels)
		asr.POST("", h.createASRChannel)
		asr.GET("/:id", h.getASRChannel)
		asr.PUT("/:id", h.updateASRChannel)
		asr.DELETE("/:id", h.deleteASRChannel)
	}
	tts := api.Group("/tts-channels")
	{
		tts.GET("", h.listTTSChannels)
		tts.POST("", h.createTTSChannel)
		tts.GET("/:id", h.getTTSChannel)
		tts.PUT("/:id", h.updateTTSChannel)
		tts.DELETE("/:id", h.deleteTTSChannel)
	}

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

	lu := api.Group("/llm-usage")
	lu.Use(models.AuthRequired)
	{
		lu.GET("", h.listLLMUsage)
		lu.GET("/:id", h.getLLMUsage)
	}

	su := api.Group("/speech-usage")
	su.Use(models.AuthRequired)
	{
		su.GET("", h.listSpeechUsage)
		su.GET("/:id", h.getSpeechUsage)
	}

	agent := api.Group("/agent")
	agent.Use(models.AuthRequired)
	{
		ar := agent.Group("/runs")
		ar.GET("", h.listAgentRuns)
		ar.GET("/:id/steps", h.listAgentRunSteps)
		ar.GET("/:id", h.getAgentRun)
	}

	admin := api.Group("/admin")
	admin.Use(models.AuthRequired)
	{
		admin.GET("/users", h.listAdminUsers)
		admin.GET("/users/:id", h.getAdminUser)
		admin.PATCH("/users/:id", h.patchAdminUser)
		admin.GET("/announcements", h.listAdminAnnouncements)
		admin.POST("/announcements", h.createSiteAnnouncement)
		admin.PUT("/announcements/:id", h.updateSiteAnnouncement)
		admin.DELETE("/announcements/:id", h.deleteSiteAnnouncement)
	}

	ml := api.Group("/mail-logs")
	{
		ml.GET("", h.listMailLogs)
		ml.POST("", h.createMailLog)
		ml.GET("/:id", h.getMailLog)
		ml.PUT("/:id", h.updateMailLog)
		ml.DELETE("/:id", h.deleteMailLog)
	}

	dash := api.Group("/dashboard")
	dash.Use(models.AuthRequired)
	{
		dash.GET("/overview", h.getDashboardOverview)
	}

	chat := api.Group("/chat")
	chat.Use(models.AuthRequired)
	{
		chat.GET("/sessions", h.listChatSessions)
		chat.POST("/sessions", h.createChatSession)
		chat.GET("/sessions/:id/messages", h.listChatMessages)
		chat.POST("/sessions/:id/messages", h.appendChatMessage)
		chat.GET("/sessions/:id", h.getChatSession)
		chat.PATCH("/sessions/:id", h.patchChatSession)
		chat.DELETE("/sessions/:id", h.deleteChatSession)
	}

	cr := api.Group("/credentials")
	cr.Use(models.AuthRequired)
	{
		cr.GET("", h.listCredentials)
		cr.GET("/groups", h.listCredentialGroups)
		cr.GET("/llm-available-models", h.listLLMAvailableModelsForCredentialGroup)
		cr.POST("", h.createCredential)
		cr.GET("/:id", h.getCredential)
		cr.PUT("/:id", h.updateCredential)
		cr.DELETE("/:id", h.deleteCredential)
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

	user := api.Group("/user")
	user.Use(models.AuthRequired)
	{
		user.PATCH("/profile", h.patchUserProfile)
		user.POST("/avatar", h.postUserAvatar)
	}
}
