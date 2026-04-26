import { getApiBaseURL } from '@/config/apiConfig'

export type OpenAIModelRow = {
  id: string
  object?: string
  created?: number
  owned_by?: string
}

export async function fetchV1Models(apiKey: string): Promise<OpenAIModelRow[]> {
  const base = getApiBaseURL().replace(/\/$/, '')
  const r = await fetch(`${base}/v1/models`, {
    headers: { Authorization: `Bearer ${apiKey.trim()}` },
  })
  if (!r.ok) {
    const t = await r.text().catch(() => '')
    throw new Error(t || `${r.status} ${r.statusText}`)
  }
  const j = (await r.json()) as { data?: OpenAIModelRow[] }
  return j.data ?? []
}

export type AgentStreamEvent = { event: string; data: Record<string, unknown> }

export async function streamV1AgentChat(params: {
  apiKey: string
  model: string
  input: string
  max_tasks?: number
  /** 可选：写入 llm_usage 的 session_id（如云端 chat session id） */
  sessionId?: string
  signal?: AbortSignal
  onEvent: (ev: AgentStreamEvent) => void
}): Promise<void> {
  const base = getApiBaseURL().replace(/\/$/, '')
  const r = await fetch(`${base}/v1/agent/chat/stream`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${params.apiKey.trim()}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      model: params.model,
      input: params.input,
      max_tasks: params.max_tasks ?? 6,
      session_id: params.sessionId?.trim() ?? '',
    }),
    signal: params.signal,
  })
  if (!r.ok) {
    const t = await r.text().catch(() => '')
    throw new Error(t || `${r.status} ${r.statusText}`)
  }
  const reader = r.body?.getReader()
  if (!reader) throw new Error('响应无 body')
  const dec = new TextDecoder()
  let carry = ''
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    carry += dec.decode(value, { stream: true })
    for (;;) {
      const nl = carry.indexOf('\n')
      if (nl < 0) break
      const line = carry.slice(0, nl)
      carry = carry.slice(nl + 1)
      const s = line.trim()
      if (!s) continue
      try {
        const j = JSON.parse(s) as AgentStreamEvent
        if (j && typeof j.event === 'string') params.onEvent(j)
      } catch {
        /* 忽略非 JSON 行 */
      }
    }
  }
}

export async function streamV1ChatCompletion(params: {
  apiKey: string
  model: string
  messages: { role: 'user' | 'assistant' | 'system'; content: string }[]
  signal?: AbortSignal
  onDelta: (chunk: string) => void
}): Promise<void> {
  const base = getApiBaseURL().replace(/\/$/, '')
  const r = await fetch(`${base}/v1/chat/completions`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${params.apiKey.trim()}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      model: params.model,
      messages: params.messages,
      stream: true,
    }),
    signal: params.signal,
  })
  if (!r.ok) {
    const t = await r.text().catch(() => '')
    throw new Error(t || `${r.status} ${r.statusText}`)
  }
  const reader = r.body?.getReader()
  if (!reader) throw new Error('响应无 body')
  const dec = new TextDecoder()
  let carry = ''
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    carry += dec.decode(value, { stream: true })
    for (;;) {
      const nl = carry.indexOf('\n')
      if (nl < 0) break
      const line = carry.slice(0, nl)
      carry = carry.slice(nl + 1)
      const s = line.trim()
      if (!s.startsWith('data:')) continue
      const data = s.slice(5).trim()
      if (data === '' || data === '[DONE]') continue
      try {
        const j = JSON.parse(data) as { choices?: { delta?: { content?: string } }[] }
        const c = j.choices?.[0]?.delta?.content
        if (c) params.onDelta(c)
      } catch {
        /* 非 JSON 行忽略 */
      }
    }
  }
}
