import { assertOk, type Paginated } from '@/api/mailAdmin'
import { get, patch } from '@/utils/request'

/** 与后端 models.User JSON 对齐（camelCase） */
export interface AdminUserRow {
  id: number
  email: string
  displayName?: string
  status: string
  role?: string
  locale?: string
  source?: string
  emailVerified?: boolean
  loginCount?: number
  createdAt?: string
  updatedAt?: string
  lastLogin?: string
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

export async function getAdminUser(id: number): Promise<AdminUserRow> {
  const r = await get<{ user: AdminUserRow }>(`/api/admin/users/${id}`)
  const d = assertOk(r)
  return d.user
}

export type AdminPatchUserBody = {
  status?: string
  role?: string
  display_name?: string
  locale?: string
}

export async function patchAdminUser(id: number, body: AdminPatchUserBody): Promise<AdminUserRow> {
  const r = await patch<{ user: AdminUserRow }>(`/api/admin/users/${id}`, body)
  const d = assertOk(r)
  return d.user
}
