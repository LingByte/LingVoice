import type { ApiResponse } from '@/utils/request'
import { get } from '@/utils/request'
import type { LLMModelMetaRow } from '@/api/llmModelMetas'

function assertOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

export type LLMModelPlazaCatalogItem = LLMModelMetaRow & {
  routable_channel_count: number
  ability_groups?: string[]
}

export type PlazaVendorCount = { vendor: string; count: number }
export type PlazaGroupCount = { group: string; count: number }
export type PlazaBillingCount = { billing: string; count: number }

export type LLMModelPlazaData = {
  catalog: LLMModelPlazaCatalogItem[]
  models_without_meta: string[]
  total_filtered: number
  total_meta_enabled: number
  vendor_counts: PlazaVendorCount[]
  group_counts: PlazaGroupCount[]
  billing_counts: PlazaBillingCount[]
  usd_per_quota_unit: number
}

export type PlazaQuery = {
  q?: string
  vendor?: string
  billing?: string
  group?: string
}

function toQuery(params?: PlazaQuery): string {
  if (!params) return ''
  const q = new URLSearchParams()
  const t = (s?: string) => (s != null && String(s).trim() ? String(s).trim() : '')
  const qq = t(params.q)
  const v = t(params.vendor)
  const b = t(params.billing)
  const g = t(params.group)
  if (qq) q.set('q', qq)
  if (v) q.set('vendor', v)
  if (b) q.set('billing', b)
  if (g) q.set('group', g)
  const s = q.toString()
  return s ? `?${s}` : ''
}

export async function getLLMModelPlaza(params?: PlazaQuery) {
  const r = await get<LLMModelPlazaData>(`/api/llm-model-plaza${toQuery(params)}`)
  return assertOk(r)
}
