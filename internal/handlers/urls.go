// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"github.com/LingByte/LingVoice/cmd/bootstrap"
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
	// JWKS endpoint for public key discovery
	engine.GET("/.well-known/jwks.json", h.JWKSHandler)
	// SendCloud webhook callback (updates mail_logs status)
	engine.POST("/webhooks/sendcloud", h.sendCloudWebhookHandler)
	// OpenAI 兼容网关：根路径 /v1（与 new-api 一致），不经 /api 前缀
	h.registerV1RelayRoutes(engine)
	api := engine.Group("/api")
	h.registerMailLogRoutes(api)
	h.registerSMSLogRoutes(api)
	h.registerMailTemplatesRoutes(api)
	h.registerNotificationChannelRoutes(api)
	h.registerInnerNotificationRoutes(api)
	h.registerKnowledgeRoutes(api)
	h.registerLLMChannelRoutes(api)
	h.registerLLMAbilityRoutes(api)
	h.registerLLMModelMetaRoutes(api)
	h.registerLLMAdminRoutes(api)
	h.registerLLMModelPlazaRoutes(api)
	h.registerSitePublicRoutes(api)
	h.registerASRChannelRoutes(api)
	h.registerTTSChannelRoutes(api)
	h.registerLLMUsageRoutes(api)
	h.registerSpeechUsageRoutes(api)
	h.registerAgentRunRoutes(api)
	h.registerAdminRoutes(api)
	h.registerDashboardRoutes(api)
	h.registerChatRoutes(api)
	h.registerCredentialRoutes(api)
	h.registerAuthRoutes(api)
}

func (h *Handlers) registerLLMChannelRoutes(api *gin.RouterGroup) {
	llmCat := api.Group("/llm-channels")
	llmCat.Use(models.AuthRequired)
	{
		llmCat.GET("/catalog", h.llmChannelsCatalogHandler)
	}
	llm := api.Group("/llm-channels")
	llm.Use(models.AuthRequired, models.AdminRequired)
	{
		llm.GET("", h.llmChannelsListHandler)
		llm.POST("", h.llmChannelCreateHandler)
		llm.GET("/:id", h.llmChannelDetailHandler)
		llm.PUT("/:id", h.llmChannelUpdateHandler)
		llm.DELETE("/:id", h.llmChannelDeleteHandler)
	}
}

func (h *Handlers) registerLLMAbilityRoutes(api *gin.RouterGroup) {
	la := api.Group("/llm-abilities")
	la.Use(models.AuthRequired, models.AdminRequired)
	{
		la.GET("", h.llmAbilitiesListHandler)
		la.POST("", h.llmAbilityCreateHandler)
		la.PATCH("", h.llmAbilityPatchHandler)
		la.DELETE("", h.llmAbilityDeleteHandler)
		la.POST("/sync-channel/:id", h.llmAbilitiesSyncChannelHandler)
	}
}

func (h *Handlers) registerLLMModelMetaRoutes(api *gin.RouterGroup) {
	lm := api.Group("/llm-model-metas")
	lm.Use(models.AuthRequired, models.AdminRequired)
	{
		lm.GET("", h.llmModelMetasListHandler)
		lm.POST("", h.llmModelMetaCreateHandler)
		lm.GET("/:id", h.llmModelMetaDetailHandler)
		lm.PUT("/:id", h.llmModelMetaUpdateHandler)
		lm.DELETE("/:id", h.llmModelMetaDeleteHandler)
	}
}

func (h *Handlers) registerLLMAdminRoutes(api *gin.RouterGroup) {
	ladm := api.Group("/llm-admin")
	ladm.Use(models.AuthRequired, models.AdminRequired)
	{
		ladm.GET("/form-options", h.llmAdminFormOptionsHandler)
	}
}

func (h *Handlers) registerLLMModelPlazaRoutes(api *gin.RouterGroup) {
	lp := api.Group("/llm-model-plaza")
	lp.Use(models.AuthRequired)
	{
		lp.GET("", h.llmModelPlazaListHandler)
	}
}

func (h *Handlers) registerSitePublicRoutes(api *gin.RouterGroup) {
	site := api.Group("/site")
	{
		site.GET("/announcements", h.siteAnnouncementsListHandler)
	}
}

func (h *Handlers) registerASRChannelRoutes(api *gin.RouterGroup) {
	asr := api.Group("/asr-channels")
	{
		asr.GET("", h.asrChannelsListHandler)
		asr.POST("", h.asrChannelCreateHandler)
		asr.GET("/:id", h.asrChannelDetailHandler)
		asr.PUT("/:id", h.asrChannelUpdateHandler)
		asr.DELETE("/:id", h.asrChannelDeleteHandler)
	}
}

func (h *Handlers) registerTTSChannelRoutes(api *gin.RouterGroup) {
	tts := api.Group("/tts-channels")
	{
		tts.GET("", h.ttsChannelsListHandler)
		tts.POST("", h.ttsChannelCreateHandler)
		tts.GET("/:id", h.ttsChannelDetailHandler)
		tts.PUT("/:id", h.ttsChannelUpdateHandler)
		tts.DELETE("/:id", h.ttsChannelDeleteHandler)
	}
}

func (h *Handlers) registerLLMUsageRoutes(api *gin.RouterGroup) {
	lu := api.Group("/llm-usage")
	lu.Use(models.AuthRequired)
	{
		lu.GET("", h.llmUsageListHandler)
		lu.GET("/:id", h.llmUsageDetailHandler)
	}
}

func (h *Handlers) registerSpeechUsageRoutes(api *gin.RouterGroup) {
	su := api.Group("/speech-usage")
	su.Use(models.AuthRequired)
	{
		su.GET("", h.speechUsageListHandler)
		su.GET("/:id", h.speechUsageDetailHandler)
	}
}

func (h *Handlers) registerAgentRunRoutes(api *gin.RouterGroup) {
	agent := api.Group("/agent")
	agent.Use(models.AuthRequired)
	{
		ar := agent.Group("/runs")
		ar.GET("", h.agentRunsListHandler)
		ar.GET("/:id/steps", h.agentRunStepsListHandler)
		ar.GET("/:id", h.agentRunDetailHandler)
	}
}

func (h *Handlers) registerAdminRoutes(api *gin.RouterGroup) {
	admin := api.Group("/admin")
	admin.Use(models.AuthRequired)
	{
		admin.GET("/users", h.adminUsersListHandler)
		admin.GET("/users/:id", h.adminUserDetailHandler)
		admin.PATCH("/users/:id", h.adminUserPatchHandler)
		admin.DELETE("/users/:id", h.adminUserDeleteHandler)
		admin.GET("/announcements", h.adminSiteAnnouncementsListHandler)
		admin.POST("/announcements", h.adminSiteAnnouncementCreateHandler)
		admin.PUT("/announcements/:id", h.adminSiteAnnouncementUpdateHandler)
		admin.DELETE("/announcements/:id", h.adminSiteAnnouncementDeleteHandler)
	}
}

func (h *Handlers) registerDashboardRoutes(api *gin.RouterGroup) {
	dash := api.Group("/dashboard")
	dash.Use(models.AuthRequired)
	{
		dash.GET("/overview", h.dashboardOverviewHandler)
	}
}

func (h *Handlers) registerChatRoutes(api *gin.RouterGroup) {
	chat := api.Group("/chat")
	chat.Use(models.AuthRequired)
	{
		chat.GET("/sessions", h.chatSessionsListHandler)
		chat.POST("/sessions", h.chatSessionCreateHandler)
		chat.GET("/sessions/:id/messages", h.chatSessionMessagesListHandler)
		chat.POST("/sessions/:id/messages", h.chatSessionMessageCreateHandler)
		chat.GET("/sessions/:id", h.chatSessionDetailHandler)
		chat.PATCH("/sessions/:id", h.chatSessionPatchHandler)
		chat.DELETE("/sessions/:id", h.chatSessionDeleteHandler)
	}
}

func (h *Handlers) registerCredentialRoutes(api *gin.RouterGroup) {
	cr := api.Group("/credentials")
	cr.Use(models.AuthRequired)
	{
		cr.GET("", h.credentialsListHandler)
		cr.GET("/groups", h.credentialGroupsListHandler)
		cr.GET("/llm-available-models", h.credentialsLLMAvailableModelsHandler)
		cr.POST("", h.credentialCreateHandler)
		cr.GET("/:id", h.credentialDetailHandler)
		cr.PUT("/:id", h.credentialUpdateHandler)
		cr.DELETE("/:id", h.credentialDeleteHandler)
	}
}

func (h *Handlers) registerAuthRoutes(api *gin.RouterGroup) {
	auth := api.Group("/auth")
	{
		auth.GET("/me", models.AuthRequired, h.authMeHandler)
		auth.POST("/send-verify-email", h.authSendVerifyEmailHandler)
		auth.POST("/verify-email-login", h.authVerifyEmailLoginHandler)
		auth.POST("/login", h.authLoginHandler)
		auth.POST("/register", h.authRegisterHandler)
		auth.POST("/refresh", h.authRefreshHandler)
		auth.POST("/logout", models.AuthRequired, h.authLogoutHandler)
	}

	user := api.Group("/user")
	user.Use(models.AuthRequired)
	{
		user.PATCH("/profile", h.userProfilePatchHandler)
		user.POST("/avatar", h.userAvatarUploadHandler)
		user.POST("/password/change", h.userChangePasswordHandler)
		user.POST("/password/send-code", h.userSendPasswordResetCodeHandler)
		user.POST("/password/reset-by-code", h.userResetPasswordByCodeHandler)
		user.GET("/llm-usage", h.userLLMUsageListHandler)
		user.GET("/llm-usage/:id", h.userLLMUsageDetailHandler)
	}
}

// JWKSHandler returns the JSON Web Key Set (JWKS) endpoint
func (h *Handlers) JWKSHandler(c *gin.Context) {
	c.Header("Content-Type", "application/json")
	c.Header("Cache-Control", "public, max-age=3600")
	if bootstrap.GlobalKeyManager == nil {
		c.JSON(500, gin.H{"error": "key manager not initialized"})
		return
	}
	jwksJSON, err := bootstrap.GlobalKeyManager.GetJWKSJSON()
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to generate JWKS"})
		return
	}
	c.String(200, jwksJSON)
}
