package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/joblet"
	"github.com/LingByte/LingVoice/pkg/utils/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *Handlers) registerJobletRoutes(api *gin.RouterGroup) {
	j := api.Group("/joblet-tasks")
	j.Use(models.AuthRequired, models.AdminRequired)
	{
		j.GET("/health", h.jobletTasksHealthHandler)
		j.GET("", h.jobletTasksListHandler)
		j.GET("/:id", h.jobletTaskDetailHandler)
		j.DELETE("/:id", h.jobletTaskDeleteHandler)
	}
}

func (h *Handlers) jobletTasksHealthHandler(c *gin.Context) {
	p := joblet.DefaultPool()
	var stats any
	if p != nil {
		stats = p.Stats()
	}
	dbHealth, ok := joblet.GlobalDBTaskLoggerHealth()
	response.Success(c, "success", gin.H{
		"default_pool": stats,
		"db_logger": func() any {
			if !ok {
				return nil
			}
			return dbHealth
		}(),
	})
}

func (h *Handlers) jobletTasksListHandler(c *gin.Context) {
	page := models.ParseQueryInt(c, "page", 1)
	pageSize := models.ClampPageSize(models.ParseQueryInt(c, "pageSize", 20))

	var f joblet.TaskRecordFilters
	f.Page = page
	f.PageSize = pageSize

	// Default scope: current organization.
	// Allow explicit orgId for superadmin use-cases, but keep default safe.
	orgID := models.CurrentOrgID(c)
	if orgID > 0 {
		f.OrgID = &orgID
	}

	if s := strings.TrimSpace(c.Query("orgId")); s != "" {
		if n, err := strconv.ParseUint(s, 10, 64); err == nil && n > 0 {
			v := uint(n)
			f.OrgID = &v
		}
	}
	if s := strings.TrimSpace(c.Query("docId")); s != "" {
		if n, err := strconv.ParseUint(s, 10, 64); err == nil && n > 0 {
			v := uint(n)
			f.DocID = &v
		}
	}
	f.Namespace = strings.TrimSpace(c.Query("namespace"))
	f.Status = strings.TrimSpace(c.Query("status"))
	f.Stage = strings.TrimSpace(c.Query("stage"))
	f.NameLike = strings.TrimSpace(c.Query("name"))

	if s := strings.TrimSpace(c.Query("from")); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.From = &t
		}
	}
	if s := strings.TrimSpace(c.Query("to")); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.To = &t
		}
	}

	out, err := joblet.ListTaskRecords(h.db, f)
	if err != nil {
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "success", gin.H{
		"list":      out.List,
		"total":     out.Total,
		"page":      out.Page,
		"pageSize":  out.PageSize,
		"totalPage": out.TotalPage,
	})
}

func (h *Handlers) jobletTaskDetailHandler(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	row, err := joblet.GetTaskRecord(h.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.FailWithCode(c, http.StatusNotFound, "任务不存在", nil)
			return
		}
		response.Fail(c, "查询失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "success", gin.H{"task": row})
}

func (h *Handlers) jobletTaskDeleteHandler(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if err := joblet.DeleteTaskRecord(h.db, id); err != nil {
		response.Fail(c, "删除失败", gin.H{"error": err.Error()})
		return
	}
	response.Success(c, "删除成功", gin.H{"id": id})
}
