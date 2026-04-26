import type { ApiResponse } from '@/utils/request'
import { del, get, post, put } from '@/utils/request'
import type { Paginated } from '@/api/mailAdmin'

function assertOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

export type SpeechKind = 'asr' | 'tts'

/** 与后端 models.LLMChannelProtocols 一致 */
export const LLM_CHANNEL_PROTOCOL_OPTIONS = [
  'openai',
  'anthropic',
  'coze',
  'ollama',
  'lmstudio',
] as const

const llm = '/api/llm-channels'
const asr = '/api/asr-channels'
const tts = '/api/tts-channels'

export type LLMChannelInfo = {
  is_multi_key: boolean
  multi_key_size: number
  multi_key_status_list: Record<string, number>
  multi_key_disabled_reason?: Record<string, string>
  multi_key_disabled_time?: Record<string, number>
  multi_key_polling_index: number
  multi_key_mode: 'random' | 'polling' | string
}

/** GET /api/llm-channels/catalog 脱敏项（无 key） */
export type LLMChannelCatalogRow = {
  id: number
  name: string
  group: string
  protocol?: string
  models: string
  status: number
}

export type LLMChannelRow = {
  id: number
  protocol?: string
  type: number
  key: string
  openai_organization?: string | null
  test_model?: string | null
  status: number
  name: string
  weight?: number | null
  created_time: number
  test_time: number
  response_time: number
  base_url?: string | null
  balance: number
  balance_updated_time: number
  models: string
  group: string
  used_quota: number
  model_mapping?: string | null
  status_code_mapping?: string | null
  priority?: number | null
  auto_ban?: number | null
  tag?: string | null
  channel_info: LLMChannelInfo
}

export type LLMChannelDetail = { channel: LLMChannelRow }

export type LLMChannelUpsert = {
  protocol?: string
  type?: number
  key: string
  name: string
  status?: number
  openai_organization?: string | null
  test_model?: string | null
  base_url?: string | null
  models?: string
  group?: string
  model_mapping?: string | null
  status_code_mapping?: string | null
  priority?: number
  weight?: number
  auto_ban?: number | null
  tag?: string | null
  channel_info: LLMChannelInfo
}

export async function listLLMChannels(page: number, pageSize: number, group?: string) {
  const r = await get<Paginated<LLMChannelRow>>(llm, {
    params: { page, pageSize, ...(group ? { group } : {}) },
  })
  return assertOk(r)
}

/** 已登录任意用户：不含 API Key，供凭证配置等 */
export async function listLLMChannelCatalog(page: number, pageSize: number, group?: string) {
  const r = await get<Paginated<LLMChannelCatalogRow>>(`${llm}/catalog`, {
    params: { page, pageSize, ...(group ? { group } : {}) },
  })
  return assertOk(r)
}

export async function getLLMChannel(id: number) {
  const r = await get<LLMChannelDetail>(`${llm}/${id}`)
  return assertOk(r)
}

export async function createLLMChannel(body: LLMChannelUpsert) {
  const r = await post<LLMChannelRow>(llm, body)
  return assertOk(r)
}

export async function updateLLMChannel(id: number, body: LLMChannelUpsert) {
  const r = await put<LLMChannelRow>(`${llm}/${id}`, body)
  return assertOk(r)
}

export async function deleteLLMChannel(id: number) {
  const r = await del<{ id: number }>(`${llm}/${id}`)
  assertOk(r)
}

export type SpeechChannelRow = {
  id: number
  createAt?: string
  updateAt?: string
  createBy?: string
  updateBy?: string
  remark?: string
  provider: string
  name: string
  enabled: boolean
  group: string
  sortOrder: number
  configJson?: string
}

export type SpeechChannelDetail = { channel: SpeechChannelRow }

export type SpeechChannelUpsert = {
  provider: string
  name: string
  enabled?: boolean
  group?: string
  sortOrder?: number
  configJson?: string
}

export async function listASRChannels(page: number, pageSize: number, filters?: { group?: string; provider?: string }) {
  const r = await get<Paginated<SpeechChannelRow>>(asr, {
    params: { page, pageSize, ...filters },
  })
  return assertOk(r)
}

export async function getASRChannel(id: number) {
  const r = await get<SpeechChannelDetail>(`${asr}/${id}`)
  return assertOk(r)
}

export async function createASRChannel(body: SpeechChannelUpsert) {
  const r = await post<SpeechChannelRow>(asr, body)
  return assertOk(r)
}

export async function updateASRChannel(id: number, body: SpeechChannelUpsert) {
  const r = await put<SpeechChannelRow>(`${asr}/${id}`, body)
  return assertOk(r)
}

export async function deleteASRChannel(id: number) {
  const r = await del<{ id: number }>(`${asr}/${id}`)
  assertOk(r)
}

export async function listTTSChannels(page: number, pageSize: number, filters?: { group?: string; provider?: string }) {
  const r = await get<Paginated<SpeechChannelRow>>(tts, {
    params: { page, pageSize, ...filters },
  })
  return assertOk(r)
}

export async function getTTSChannel(id: number) {
  const r = await get<SpeechChannelDetail>(`${tts}/${id}`)
  return assertOk(r)
}

export async function createTTSChannel(body: SpeechChannelUpsert) {
  const r = await post<SpeechChannelRow>(tts, body)
  return assertOk(r)
}

export async function updateTTSChannel(id: number, body: SpeechChannelUpsert) {
  const r = await put<SpeechChannelRow>(`${tts}/${id}`, body)
  return assertOk(r)
}

export async function deleteTTSChannel(id: number) {
  const r = await del<{ id: number }>(`${tts}/${id}`)
  assertOk(r)
}
