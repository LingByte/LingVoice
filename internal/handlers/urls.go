// Copyright (c) 2026 heathcetide. All rights reserved.

package handlers

import (
	"time"

	"github.com/LingByte/LingVoice/internal/configs"
	"github.com/LingByte/LingVoice/pkg/common/jwtutil"
	jwtingin "github.com/LingByte/LingVoice/pkg/common/jwtutil/gin"
	"github.com/LingByte/LingVoice/internal/constants"
	"github.com/LingByte/LingVoice/pkg/apidocs/humax"
	"github.com/danielgtaylor/huma/v2"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AppInfo holds application metadata.
type AppInfo struct {
	Name      string
	Version   string
	BuildTime string
	GitCommit string
	JWTAuth *jwtutil.Auth
}

// Handlers is the HTTP handler collection.
// Storage/cache/lock/retry are accessed via pkg/ package-level singletons.
type Handlers struct {
	cfg       *configs.Config
	info      AppInfo
	db        *gorm.DB
	startTime time.Time
	jwtAuth *jwtutil.Auth
}

// New creates a new Handlers instance.
func New(cfg *configs.Config, info AppInfo, db *gorm.DB) *Handlers {
	return &Handlers{
		cfg:       cfg,
		info:      info,
		db:        db,
		startTime: time.Now(),
		jwtAuth: info.JWTAuth,
	}
}

// Register registers all routes via humax.Group (Gin + OpenAPI docs).
func (h *Handlers) Register(engine *gin.Engine, api huma.API) {
	r := humax.NewGroup(api, engine, "")
	h.registerRoutes(r)
}

// registerRoutes registers all route groups.
func (h *Handlers) registerRoutes(r *humax.Group) {
	h.registerSystemRoutes(r)
	h.registerAuthRoutes(r)
	h.registerUserRoutes(r)
	h.registerFileRoutes(r)
	h.registerRoleRoutes(r)
	h.registerPermissionRoutes(r)
	h.registerUserRoleRoutes(r)
	h.registerTenantRoutes(r)
}

// registerSystemRoutes registers public system endpoints (health, version).
func (h *Handlers) registerSystemRoutes(r *humax.Group) {
	r.GET("/health", h.Health)
	r.GET("/live", h.Liveness)
	r.GET("/ready", h.Readiness)
	r.GET("/api/v1/version", h.Version)
}

// registerAuthRoutes registers public auth endpoints (login, refresh).
func (h *Handlers) registerAuthRoutes(r *humax.Group) {
	auth := r.Group("/api/v1/auth")
	{
		auth.POST("/login", h.Login)
		auth.POST("/refresh", h.Refresh)
	}
}

// registerUserRoutes registers user registration (public) + user CRUD endpoints.
func (h *Handlers) registerUserRoutes(r *humax.Group) {
	// Public registration
	r.POST("/api/v1/register", h.RegisterUser)

	// User CRUD
	users := r.Group("/api/v1/users")
	{
		users.GET("", h.ListUsers)
		users.GET("/:id", h.GetUser)
		users.POST("", h.CreateUser)
		users.PUT("/:id", h.UpdateUser)
		users.PUT("/:id/password", h.ChangePassword)
		users.DELETE("/:id", h.DeleteUser)
	}
}

// registerFileRoutes registers file upload/download/delete endpoints.
func (h *Handlers) registerFileRoutes(r *humax.Group) {
	files := r.Group("/api/v1/files")
	{
		files.POST("/upload", h.UploadFile)
		files.GET("/:key", h.DownloadFile)
		files.DELETE("/:key", h.DeleteFile)
	}
}

// registerRoleRoutes registers role management endpoints (admin only).
func (h *Handlers) registerRoleRoutes(r *humax.Group) {
	roles := r.Group("/api/v1/roles")
	// Admin-only management endpoints
	adminMW := jwtingin.RequireRole(h.jwtAuth, constants.RoleAdmin)
	roles.Use(adminMW)
	{
		roles.GET("", h.ListRoles)
		roles.POST("", h.CreateRole)
		roles.GET("/:id", h.GetRole)
		roles.PUT("/:id", h.UpdateRole)
		roles.DELETE("/:id", h.DeleteRole)
		roles.GET("/:id/permissions", h.GetRolePermissions)
		roles.POST("/permissions/assign", h.AssignPermissionsToRole)
	}
}

// registerPermissionRoutes registers permission management endpoints (admin only).
func (h *Handlers) registerPermissionRoutes(r *humax.Group) {
	perms := r.Group("/api/v1/permissions")
	adminMW := jwtingin.RequireRole(h.jwtAuth, constants.RoleAdmin)
	perms.Use(adminMW)
	{
		perms.GET("", h.ListPermissions)
		perms.POST("", h.CreatePermission)
		perms.PUT("/:id", h.UpdatePermission)
		perms.DELETE("/:id", h.DeletePermission)
	}
}

// registerUserRoleRoutes registers user-role assignment endpoints (admin only).
func (h *Handlers) registerUserRoleRoutes(r *humax.Group) {
	userRoles := r.Group("/api/v1/user-roles")
	adminMW := jwtingin.RequireRole(h.jwtAuth, constants.RoleAdmin)
	userRoles.Use(adminMW)
	{
		userRoles.POST("/assign", h.AssignRoleToUser)
		userRoles.POST("/revoke", h.RevokeRoleFromUser)
		userRoles.GET("/:id/roles", h.GetUserRoles)
		userRoles.GET("/:id/permissions", h.GetUserPermissions)
	}
}

// registerTenantRoutes registers tenant management endpoints.
func (h *Handlers) registerTenantRoutes(r *humax.Group) {
	tenants := r.Group("/api/v1/tenants")
	{
		tenants.GET("", h.ListTenants)
		tenants.POST("", h.CreateTenant)
		tenants.GET("/:id", h.GetTenant)
		tenants.PUT("/:id", h.UpdateTenant)
		tenants.DELETE("/:id", h.DeleteTenant)
	}
}
