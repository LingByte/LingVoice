import { assertOk, type Paginated } from '@/api/mailAdmin'
import { get } from '@/utils/request'

export interface SpeechUsageRow {
  id: string
  request_id: string
  credential_id: number
  user_id: string
  kind: string
  provider: string
  channel_id: number
  group: string
  request_type: string
  request_content: string
  response_content: string
  latency_ms: number
  status_code: number
  success: boolean
  error_message: string
  audio_input_bytes: number
  audio_output_bytes: number
  text_input_chars: number
  user_agent: string
  ip_address: string
  requested_at: string
  completed_at: string
  created_at: string
  updated_at: string
}

export type SpeechUsageListParams = {
  page?: number
  pageSize?: number
  user_id?: string
  kind?: string
  credential_id?: number
  channel_id?: number
  request_id?: string
  provider?: string
  request_type?: string
  success?: boolean
  from?: string
  to?: string
}

function toQuery(p: SpeechUsageListParams): string {
  const q = new URLSearchParams()
  if (p.page != null) q.set('page', String(p.page))
  if (p.pageSize != null) q.set('pageSize', String(p.pageSize))
  if (p.user_id) q.set('user_id', p.user_id)
  if (p.kind) q.set('kind', p.kind)
  if (p.credential_id != null && p.credential_id > 0) q.set('credential_id', String(p.credential_id))
  if (p.channel_id != null && p.channel_id > 0) q.set('channel_id', String(p.channel_id))
  if (p.request_id) q.set('request_id', p.request_id)
  if (p.provider) q.set('provider', p.provider)
  if (p.request_type) q.set('request_type', p.request_type)
  if (p.success === true) q.set('success', 'true')
  if (p.success === false) q.set('success', 'false')
  if (p.from) q.set('from', p.from)
  if (p.to) q.set('to', p.to)
  const s = q.toString()
  return s ? `?${s}` : ''
}

export async function listSpeechUsage(params: SpeechUsageListParams): Promise<Paginated<SpeechUsageRow>> {
  const r = await get<Paginated<SpeechUsageRow>>(`/api/speech-usage${toQuery(params)}`)
  return assertOk(r)
}

export async function getSpeechUsage(id: string): Promise<SpeechUsageRow> {
  const enc = encodeURIComponent(id)
  const r = await get<{ usage: SpeechUsageRow }>(`/api/speech-usage/${enc}`)
  const d = assertOk(r)
  return d.usage
}
