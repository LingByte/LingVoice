import type { ApiResponse } from '@/utils/request'
import { del, get, post, put } from '@/utils/request'

export type CredentialKind = 'llm' | 'asr' | 'tts' | 'email'

export type CredentialRow = {
  id: number
  user_id: number
  kind: CredentialKind | string
  key_masked: string
  status: number
  name: string
  extra?: Record<string, unknown> | string
  created_time: number
  accessed_time: number
  expired_time: number
  remain_quota: number
  unlimited_quota: boolean
  used_quota: number
  model_limits_enabled: boolean
  model_limits: string
  allow_ips?: string | null
  group: string
  cross_group_retry: boolean
}

export type CredentialCreateBody = {
  kind: CredentialKind | string
  name: string
  remain_quota?: number
  unlimited_quota?: boolean
  allow_ips?: string
  group?: string
  cross_group_retry?: boolean
  expired_time?: number
}

export type CredentialCreateResult = CredentialRow & {
  key: string
  key_hint: string
}

export type CredentialUpdateBody = {
  name: string
  status: number
  remain_quota: number
  unlimited_quota: boolean
  allow_ips: string
  group: string
  cross_group_retry: boolean
  expired_time: number
}

function ensureOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

export type ListCredentialsFilters = {
  kind?: string
  group?: string
}

export async function listCredentials(filters?: ListCredentialsFilters): Promise<CredentialRow[]> {
  const params: Record<string, string> = {}
  if (filters?.kind) params.kind = filters.kind
  if (filters?.group) params.group = filters.group
  const r = await get<{ list: CredentialRow[] }>('/api/credentials', {
    params: Object.keys(params).length ? params : undefined,
  })
  const d = ensureOk(r)
  return d.list ?? []
}

export async function listCredentialGroups(): Promise<string[]> {
  const r = await get<{ groups: string[] }>('/api/credentials/groups')
  const d = ensureOk(r)
  return d.groups ?? []
}

export async function createCredential(body: CredentialCreateBody): Promise<CredentialCreateResult> {
  const r = await post<CredentialCreateResult>('/api/credentials', body)
  return ensureOk(r)
}

export async function getCredential(id: number): Promise<CredentialRow> {
  const r = await get<CredentialRow>(`/api/credentials/${id}`)
  return ensureOk(r)
}

export async function updateCredential(
  id: number,
  body: CredentialUpdateBody,
): Promise<CredentialRow> {
  const r = await put<CredentialRow>(`/api/credentials/${id}`, body)
  return ensureOk(r)
}

export async function deleteCredential(id: number): Promise<void> {
  const r = await del<{ id: number }>(`/api/credentials/${id}`)
  ensureOk(r)
}
