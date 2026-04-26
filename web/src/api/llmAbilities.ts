import type { ApiResponse } from '@/utils/request'
import { del, get, patch, post } from '@/utils/request'
import type { Paginated } from '@/api/mailAdmin'

function assertOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

const base = '/api/llm-abilities'

export type LLMAbilityRow = {
  group: string
  model: string
  channel_id: number
  channel_name?: string
  model_meta_id?: number | null
  enabled: boolean
  priority: number
  weight: number
  tag?: string | null
}

export type LLMAbilityDetail = { ability: LLMAbilityRow }

export type LLMAbilityCreate = {
  group: string
  /** 与 model_meta_id 二选一（或同时传，以元数据为准） */
  model?: string
  model_meta_id?: number
  channel_id: number
  enabled?: boolean
  priority?: number
  weight?: number
  tag?: string | null
}

export type LLMAbilityPatch = {
  enabled?: boolean
  priority?: number
  weight?: number
  tag?: string | null
  model_meta_id?: number
}

export async function listLLMAbilities(
  page: number,
  pageSize: number,
  filters?: { group?: string; model?: string; channel_id?: number },
) {
  const r = await get<Paginated<LLMAbilityRow>>(base, {
    params: { page, pageSize, ...filters },
  })
  return assertOk(r)
}

export async function createLLMAbility(body: LLMAbilityCreate) {
  const r = await post<LLMAbilityDetail>(base, body)
  return assertOk(r)
}

export async function patchLLMAbility(
  key: { group: string; model: string; channel_id: number },
  body: LLMAbilityPatch,
) {
  const r = await patch<LLMAbilityDetail>(base, body, {
    params: {
      group: key.group,
      model: key.model,
      channel_id: key.channel_id,
    },
  })
  return assertOk(r)
}

export async function deleteLLMAbility(key: { group: string; model: string; channel_id: number }) {
  const r = await del(base, {
    params: {
      group: key.group,
      model: key.model,
      channel_id: key.channel_id,
    },
  })
  assertOk(r)
}

export async function syncLLMAbilitiesFromChannel(channelId: number) {
  const r = await post<{ channel_id: number }>(`${base}/sync-channel/${channelId}`)
  return assertOk(r)
}
