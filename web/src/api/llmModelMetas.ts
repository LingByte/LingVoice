import type { ApiResponse } from '@/utils/request'
import { del, get, post, put } from '@/utils/request'
import type { Paginated } from '@/api/mailAdmin'

function assertOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

const base = '/api/llm-model-metas'

export type LLMModelMetaRow = {
  id: number
  model_name: string
  description?: string
  tags?: string
  status: number
  icon_url?: string
  vendor?: string
  sort_order?: number
  context_length?: number | null
  max_output_tokens?: number | null
  quota_billing_mode?: string
  quota_model_ratio?: number
  quota_prompt_ratio?: number
  quota_completion_ratio?: number
  quota_cache_read_ratio?: number
  created_time: number
  updated_time: number
}

export type LLMModelMetaDetail = { meta: LLMModelMetaRow }

export type LLMModelMetaWrite = {
  model_name: string
  description?: string
  tags?: string
  status?: number
  icon_url?: string
  vendor?: string
  sort_order?: number
  context_length?: number | null
  max_output_tokens?: number | null
  quota_billing_mode?: string
  quota_model_ratio?: number
  quota_prompt_ratio?: number
  quota_completion_ratio?: number
  quota_cache_read_ratio?: number
}

export async function listLLMModelMetas(
  page: number,
  pageSize: number,
  filters?: { q?: string; status?: number },
) {
  const r = await get<Paginated<LLMModelMetaRow>>(base, {
    params: {
      page,
      pageSize,
      ...(filters?.q ? { q: filters.q } : {}),
      ...(filters?.status !== undefined ? { status: filters.status } : {}),
    },
  })
  return assertOk(r)
}

export async function getLLMModelMeta(id: number) {
  const r = await get<LLMModelMetaDetail>(`${base}/${id}`)
  return assertOk(r)
}

export async function createLLMModelMeta(body: LLMModelMetaWrite) {
  const r = await post<LLMModelMetaDetail>(base, body)
  return assertOk(r)
}

export async function updateLLMModelMeta(id: number, body: LLMModelMetaWrite) {
  const r = await put<LLMModelMetaDetail>(`${base}/${id}`, body)
  return assertOk(r)
}

export async function deleteLLMModelMeta(id: number) {
  const r = await del(`${base}/${id}`)
  assertOk(r)
}
