
// Copyright (c) 2026 heathcetide. All rights reserved.

package handlers

import (
	"errors"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/internal/types"

	"github.com/LingByte/LingVoice/pkg/common/response"
	respgin "github.com/LingByte/LingVoice/pkg/common/response/gin"
	"github.com/LingByte/LingVoice/pkg/common/validate"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListPermissions returns all permissions.
func (h *Handlers) ListPermissions(c *gin.Context) {
	if h.db == nil {
		respgin.WriteError(c, response.Err(response.CodeServiceUnavail))
		return
	}
	perms, err := models.Permission{}.ListAll(h.db.WithContext(c.Request.Context()))
	if err != nil {
		respgin.WriteError(c, response.Err(response.CodeInternal))
		return
	}
	result := make([]types.PermissionResponse, len(perms))
	for i := range perms {
		result[i] = types.ToPermissionResponse(&perms[i])
	}
	respgin.Success(c, result)
}

// CreatePermission creates a new permission.
func (h *Handlers) CreatePermission(c *gin.Context) {
	var req types.PermissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respgin.WriteError(c, response.New(response.CodeBadRequest, err.Error()))
		return
	}
	if err := validate.Validate(&req); err != nil {
		respgin.WriteError(c, response.New(response.CodeBadRequest, err.Error()))
		return
	}

	if h.db == nil {
		respgin.WriteError(c, response.Err(response.CodeServiceUnavail))
		return
	}

	perm := models.Permission{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Resource:    req.Resource,
		Action:      req.Action,
		Description: req.Description,
	}

	if err := perm.Create(h.db.WithContext(c.Request.Context())); err != nil {
		respgin.WriteError(c, response.New(response.CodeConflict, "permission name already exists"))
		return
	}
	respgin.Created(c, types.ToPermissionResponse(&perm))
}

// UpdatePermission updates a permission by ID.
func (h *Handlers) UpdatePermission(c *gin.Context) {
	var idReq types.IDRequest
	if err := c.ShouldBindUri(&idReq); err != nil {
		respgin.WriteError(c, response.Err(response.CodeBadRequest))
		return
	}

	var req types.PermissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respgin.WriteError(c, response.New(response.CodeBadRequest, err.Error()))
		return
	}
	if err := validate.Validate(&req); err != nil {
		respgin.WriteError(c, response.New(response.CodeBadRequest, err.Error()))
		return
	}

	if h.db == nil {
		respgin.WriteError(c, response.Err(response.CodeServiceUnavail))
		return
	}

	perm, err := models.Permission{}.FindByID(h.db.WithContext(c.Request.Context()), idReq.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respgin.WriteError(c, response.Err(response.CodeNotFound))
			return
		}
		respgin.WriteError(c, response.Err(response.CodeInternal))
		return
	}

	updates := map[string]interface{}{
		"name":        req.Name,
		"displayName": req.DisplayName,
		"resource":    req.Resource,
		"action":      req.Action,
		"description": req.Description,
	}

	if err := perm.Updates(h.db.WithContext(c.Request.Context()), updates); err != nil {
		respgin.WriteError(c, response.Err(response.CodeInternal))
		return
	}
	respgin.Success(c, types.ToPermissionResponse(perm))
}

// DeletePermission deletes a permission by ID.
func (h *Handlers) DeletePermission(c *gin.Context) {
	var req types.IDRequest
	if err := c.ShouldBindUri(&req); err != nil {
		respgin.WriteError(c, response.Err(response.CodeBadRequest))
		return
	}

	if h.db == nil {
		respgin.WriteError(c, response.Err(response.CodeServiceUnavail))
		return
	}

	if err := (models.Permission{}).DeleteByID(h.db.WithContext(c.Request.Context()), req.ID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			respgin.WriteError(c, response.Err(response.CodeNotFound))
			return
		}
		respgin.WriteError(c, response.Err(response.CodeInternal))
		return
	}
	respgin.NoContent(c)
}
