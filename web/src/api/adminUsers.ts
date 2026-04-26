import { assertOk, type Paginated } from '@/api/mailAdmin'
import { del, get, patch } from '@/utils/request'

/** 与后端管理端用户 JSON 对齐；id 为十进制字符串，避免大整数精度丢失 */
export interface AdminUserRow {
  id: string
  email: string
  displayName?: string
  phone?: string
  firstName?: string
  lastName?: string
  avatar?: string
  gender?: string
  city?: string
  region?: string
  timezone?: string
  status: string
  role?: string
  locale?: string
  source?: string
  emailVerified?: boolean
  phoneVerified?: boolean
  emailNotifications?: boolean
  twoFactorEnabled?: boolean
  loginCount?: number
  profileComplete?: number
  githubLogin?: string
  wechatOpenId?: string
  remainQuota?: number
  usedQuota?: number
  unlimitedQuota?: boolean
  createdAt?: string
  updatedAt?: string
  lastLogin?: string
  lastPasswordChange?: string
  accountDeletionRequestedAt?: string
  accountDeletionEffectiveAt?: string
}

export type AdminUserListParams = {
  page?: number
  pageSize?: number
  email?: string
  status?: string
  role?: string
}

function toQuery(p: AdminUserListParams): string {
  const q = new URLSearchParams()
  if (p.page != null) q.set('page', String(p.page))
  if (p.pageSize != null) q.set('pageSize', String(p.pageSize))
  if (p.email) q.set('email', p.email)
  if (p.status) q.set('status', p.status)
  if (p.role) q.set('role', p.role)
  const s = q.toString()
  return s ? `?${s}` : ''
}

export async function listAdminUsers(params: AdminUserListParams): Promise<Paginated<AdminUserRow>> {
  const r = await get<Paginated<AdminUserRow>>(`/api/admin/users${toQuery(params)}`)
  return assertOk(r)
}

export async function getAdminUser(id: string): Promise<AdminUserRow> {
  const r = await get<{ user: AdminUserRow }>(`/api/admin/users/${encodeURIComponent(id)}`)
  const d = assertOk(r)
  return d.user
}

export type AdminPatchUserBody = {
  status?: string
  role?: string
  display_name?: string
  locale?: string
  phone?: string
  first_name?: string
  last_name?: string
  avatar?: string
  timezone?: string
  gender?: string
  city?: string
  region?: string
  email_notifications?: boolean
  phone_verified?: boolean
  email_verified?: boolean
  remain_quota?: number
  used_quota?: number
  unlimited_quota?: boolean
}

export async function patchAdminUser(id: string, body: AdminPatchUserBody): Promise<AdminUserRow> {
  const r = await patch<{ user: AdminUserRow }>(`/api/admin/users/${encodeURIComponent(id)}`, body)
  const d = assertOk(r)
  return d.user
}

export async function deleteAdminUser(id: string): Promise<void> {
  const r = await del<{ id: string }>(`/api/admin/users/${encodeURIComponent(id)}`)
  assertOk(r)
}
