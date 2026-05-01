// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package handlers

import (
	"errors"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Handlers) registerSitePublicRoutes(api *gin.RouterGroup) {
	site := api.Group("/site")
	{
		site.GET("/announcements", h.announcementsListHandler)
	}
}

func (h *Handlers) registerAdminRoutes(api *gin.RouterGroup) {
	admin := api.Group("/admin")
	admin.Use(models.AuthRequired, models.AdminRequired)
	{
		admin.GET("/users", h.adminUsersListHandler)
		admin.GET("/users/:id", h.adminUserDetailHandler)
		admin.PATCH("/users/:id", h.adminUserPatchHandler)
		admin.DELETE("/users/:id", h.adminUserDeleteHandler)
		admin.GET("/announcements", h.adminAnnouncementsListHandler)
		admin.POST("/announcements", h.adminAnnouncementCreateHandler)
		admin.PUT("/announcements/:id", h.adminAnnouncementUpdateHandler)
		admin.DELETE("/announcements/:id", h.adminAnnouncementDeleteHandler)
	}
}

type SiteAnnouncementPatch struct {
	Title     *string `json:"title" binding:"omitempty,max=255"`
	Body      *string `json:"body"`
	Pinned    *bool   `json:"pinned"`
	Enabled   *bool   `json:"enabled"`
	SortOrder *int    `json:"sort_order"`
}

type SiteAnnouncementWrite struct {
	Title     string  `json:"title" binding:"required,max=255"`
	Body      *string `json:"body"`
	Pinned    *bool   `json:"pinned"`
	Enabled   *bool   `json:"enabled"`
	SortOrder *int    `json:"sort_order"`
}

// announcementsListHandler GET /api/site/announcements 已启用公告列表（无需登录）。
func (h *Handlers) announcementsListHandler(c *gin.Context) {
	var rows []models.Announcement
	if err := h.db.Where("enabled = ?", true).
		Order("pinned DESC, sort_order ASC, id DESC").
		Find(&rows).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"list": rows})
}

// adminAnnouncementsListHandler GET /api/admin/announcements
func (h *Handlers) adminAnnouncementsListHandler(c *gin.Context) {
	var rows []models.Announcement
	if err := h.db.Order("pinned DESC, sort_order ASC, id DESC").Find(&rows).Error; err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "ok", gin.H{"list": rows})
}

// adminAnnouncementCreateHandler POST /api/admin/announcements
func (h *Handlers) adminAnnouncementCreateHandler(c *gin.Context) {
	var body SiteAnnouncementWrite
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	b := ""
	if body.Body != nil {
		b = strings.TrimSpace(*body.Body)
	}
	row := models.Announcement{
		Title:     strings.TrimSpace(body.Title),
		Body:      b,
		Pinned:    body.Pinned != nil && *body.Pinned,
		Enabled:   body.Enabled == nil || *body.Enabled,
		SortOrder: 0,
	}
	if body.SortOrder != nil {
		row.SortOrder = *body.SortOrder
	}
	if err := h.db.Create(&row).Error; err != nil {
		response.Fail(c, "创建失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "已创建", gin.H{"announcement": row})
}

// adminAnnouncementUpdateHandler PUT /api/admin/announcements/:id
func (h *Handlers) adminAnnouncementUpdateHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	var body SiteAnnouncementPatch
	if err := c.ShouldBindJSON(&body); err != nil {
		response.FailWithCode(c, 400, "参数错误", gin.H{"error": err.Error()})
		return
	}
	var row models.Announcement
	if err := h.db.Where("id = ?", id).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, 404, "不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	vals := map[string]any{}
	if body.Title != nil {
		if t := strings.TrimSpace(*body.Title); t != "" {
			vals["title"] = t
		}
	}
	if body.Body != nil {
		vals["body"] = strings.TrimSpace(*body.Body)
	}
	if body.Pinned != nil {
		vals["pinned"] = *body.Pinned
	}
	if body.Enabled != nil {
		vals["enabled"] = *body.Enabled
	}
	if body.SortOrder != nil {
		vals["sort_order"] = *body.SortOrder
	}
	if len(vals) == 0 {
		response.FailWithCode(c, 400, "无可更新字段", nil)
		return
	}
	if err := h.db.Model(&row).Updates(vals).Error; err != nil {
		response.Fail(c, "更新失败", gin.H{"error": err.Error()})
		return
	}
	_ = h.db.Where("id = ?", id).First(&row).Error
	response.Success(c, "已更新", gin.H{"announcement": row})
}

// adminAnnouncementDeleteHandler DELETE /api/admin/announcements/:id
func (h *Handlers) adminAnnouncementDeleteHandler(c *gin.Context) {
	id, ok := models.ParseUintParam(c, "id")
	if !ok {
		response.FailWithCode(c, 400, "无效的 id", nil)
		return
	}
	if err := h.db.Delete(&models.Announcement{}, id).Error; err != nil {
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "已删除", gin.H{"id": id})
}
