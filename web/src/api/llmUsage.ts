import { assertOk, type Paginated } from '@/api/mailAdmin'
import { get } from '@/utils/request'

export interface LLMUsageChannelAttempt {
  order: number
  channel_id: number
  base_url?: string
  success: boolean
  status_code?: number
  latency_ms?: number
  ttft_ms?: number
  error_code?: string
  error_message?: string
}

export interface LLMUsageRow {
  id: string
  request_id: string
  user_id: string
  channel_id: number
  channel_attempts?: LLMUsageChannelAttempt[]
  provider: string
  model: string
  base_url: string
  request_type: string
  input_tokens: number
  output_tokens: number
  total_tokens: number
  /** 本次从 OpenAPI 凭证扣除的额度单位 */
  quota_delta?: number
  latency_ms: number
  ttft_ms: number
  tps: number
  queue_time_ms: number
  request_content: string
  response_content: string
  user_agent: string
  ip_address: string
  status_code: number
  success: boolean
  error_code: string
  error_message: string
  requested_at: string
  started_at: string
  first_token_at: string
  completed_at: string
  created_at: string
  updated_at: string
}

export type LLMUsageListParams = {
  page?: number
  pageSize?: number
  user_id?: string
  channel_id?: number
  request_id?: string
  provider?: string
  model?: string
  request_type?: string
  success?: boolean
  /** RFC3339 */
  from?: string
  /** RFC3339 */
  to?: string
}

function toQuery(p: LLMUsageListParams): string {
  const q = new URLSearchParams()
  if (p.page != null) q.set('page', String(p.page))
  if (p.pageSize != null) q.set('pageSize', String(p.pageSize))
  if (p.user_id) q.set('user_id', p.user_id)
  if (p.channel_id != null && p.channel_id > 0) q.set('channel_id', String(p.channel_id))
  if (p.request_id) q.set('request_id', p.request_id)
  if (p.provider) q.set('provider', p.provider)
  if (p.model) q.set('model', p.model)
  if (p.request_type) q.set('request_type', p.request_type)
  if (p.success === true) q.set('success', 'true')
  if (p.success === false) q.set('success', 'false')
  if (p.from) q.set('from', p.from)
  if (p.to) q.set('to', p.to)
  const s = q.toString()
  return s ? `?${s}` : ''
}

export async function listLLMUsage(params: LLMUsageListParams): Promise<Paginated<LLMUsageRow>> {
  const r = await get<Paginated<LLMUsageRow>>(`/api/llm-usage${toQuery(params)}`)
  return assertOk(r)
}

export async function getLLMUsage(id: string): Promise<LLMUsageRow> {
  const enc = encodeURIComponent(id)
  const r = await get<{ usage: LLMUsageRow }>(`/api/llm-usage/${enc}`)
  const d = assertOk(r)
  return d.usage
}
