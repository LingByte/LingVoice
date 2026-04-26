import type { ApiResponse } from '@/utils/request'
import { get, patch } from '@/utils/request'
import type { Paginated } from '@/api/mailAdmin'

function assertOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

/** 与后端 `InternalNotification` JSON 对齐 */
export type InternalNotificationRow = {
  id: number
  userId: number
  title: string
  content: string
  read: boolean
  createdAt: string
  updatedAt?: string
  createBy?: string
  updateBy?: string
  remark?: string
  deletedAt?: string | null
}

export async function listMyInternalNotifications(page = 1, pageSize = 50) {
  const q = new URLSearchParams({ page: String(page), pageSize: String(pageSize) })
  const r = await get<Paginated<InternalNotificationRow>>(`/api/internal-notifications?${q.toString()}`)
  return assertOk(r)
}

export async function markInternalNotificationRead(id: number, read = true) {
  const r = await patch<InternalNotificationRow>(`/api/internal-notifications/${encodeURIComponent(String(id))}/read`, {
    read,
  })
  return assertOk(r)
}
