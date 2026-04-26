import type { ApiResponse } from '@/utils/request'
import { del, get, post, put } from '@/utils/request'

function assertOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

export type SiteAnnouncement = {
  id: number
  title: string
  body: string
  pinned: boolean
  enabled: boolean
  sort_order: number
  created_at: string
  updated_at: string
}

export async function listPublicAnnouncements() {
  const r = await get<{ list: SiteAnnouncement[] }>('/api/site/announcements')
  return assertOk(r).list
}

export async function listAdminAnnouncements() {
  const r = await get<{ list: SiteAnnouncement[] }>('/api/admin/announcements')
  return assertOk(r).list
}

export type SiteAnnouncementCreate = {
  title: string
  body?: string
  pinned?: boolean
  enabled?: boolean
  sort_order?: number
}

export async function createAdminAnnouncement(body: SiteAnnouncementCreate) {
  const r = await post<{ announcement: SiteAnnouncement }>('/api/admin/announcements', body)
  return assertOk(r).announcement
}

export type SiteAnnouncementPatch = {
  title?: string
  body?: string
  pinned?: boolean
  enabled?: boolean
  sort_order?: number
}

export async function updateAdminAnnouncement(id: number, body: SiteAnnouncementPatch) {
  const r = await put<{ announcement: SiteAnnouncement }>(
    `/api/admin/announcements/${encodeURIComponent(String(id))}`,
    body,
  )
  return assertOk(r).announcement
}

export async function deleteAdminAnnouncement(id: number) {
  const r = await del<{ id: number }>(`/api/admin/announcements/${encodeURIComponent(String(id))}`)
  return assertOk(r)
}
