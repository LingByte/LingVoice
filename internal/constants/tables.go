// Copyright (c) 2026 heathcetide. All rights reserved.

// Package constants defines application-wide constant values.
package constants

// Database table names.
const (
	TableUsers = "users"
	TableRoles       = "roles"
	TablePermissions = "permissions"
	TableUserRoles   = "user_roles"
	TableRolePerms   = "role_permissions"
	TableTenants = "tenants"
)

// RBAC role constants.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// RBAC permission action constants.
const (
	PermActionRead   = "read"
	PermActionWrite  = "write"
	PermActionDelete = "delete"
)

// RBAC permission resource constants.
const (
	PermResourceUser   = "user"
	PermResourceRole   = "role"
	PermResourceFile   = "file"
)

// Multi-tenant constants.
const (
	// TenantStatusActive = active tenant
	TenantStatusActive = 1
	// TenantStatusDisabled = disabled tenant
	TenantStatusDisabled = 0

	// TenantPlanFree = free plan
	TenantPlanFree = "free"
	// TenantPlanPro = pro plan
	TenantPlanPro = "pro"
	// TenantPlanEnterprise = enterprise plan
	TenantPlanEnterprise = "enterprise"

	// ContextKeyTenantID is the gin.Context key for the current tenant ID.
	ContextKeyTenantID = "tenant_id"
	// HeaderTenantID is the HTTP header for tenant identification (when not using JWT).
	HeaderTenantID = "X-Tenant-ID"
)
