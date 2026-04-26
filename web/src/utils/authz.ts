/** 与后端 models.RoleAdmin / RoleSuperAdmin 一致（小写比较）。 */
export function isAdminRole(role?: string | null): boolean {
  const r = (role || '').toLowerCase()
  return r === 'admin' || r === 'superadmin'
}
