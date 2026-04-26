import type { ApiResponse } from '@/utils/request'
import { del, get, patch, post } from '@/utils/request'

function ensureOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

export type ChatSessionRow = {
  id: string
  title: string
  model: string
  provider: string
  system_prompt?: string
  status?: string
  created_at: number
  updated_at: number
}

export type ChatMessageRow = {
  id: string
  session_id: string
  role: string
  content: string
  token_count?: number
  model: string
  provider: string
  request_id?: string
  created_at: number
}

export async function listChatSessions(): Promise<ChatSessionRow[]> {
  const r = await get<{ list: ChatSessionRow[] }>('/api/chat/sessions')
  const d = ensureOk(r)
  return d.list ?? []
}

export async function createChatSession(body: {
  title?: string
  model: string
  provider?: string
  system_prompt?: string
}): Promise<ChatSessionRow> {
  const r = await post<{ session: ChatSessionRow }>('/api/chat/sessions', body)
  const d = ensureOk(r)
  if (!d.session) throw new Error('未返回 session')
  return d.session
}

export async function patchChatSessionTitle(sessionId: string, title: string): Promise<void> {
  const r = await patch<{ id: string }>(`/api/chat/sessions/${encodeURIComponent(sessionId)}`, { title })
  ensureOk(r)
}

export async function deleteChatSession(sessionId: string): Promise<void> {
  const r = await del<{ id: string }>(`/api/chat/sessions/${encodeURIComponent(sessionId)}`)
  ensureOk(r)
}

export async function listChatMessages(sessionId: string): Promise<{
  list: ChatMessageRow[]
  session: { id: string; title: string; model: string; provider: string }
}> {
  const r = await get<{
    list: ChatMessageRow[]
    session: { id: string; title: string; model: string; provider: string }
  }>(`/api/chat/sessions/${encodeURIComponent(sessionId)}/messages`)
  return ensureOk(r)
}

export async function appendChatMessage(
  sessionId: string,
  body: {
    role: 'user' | 'assistant' | 'system'
    content: string
    token_count?: number
    model?: string
    provider?: string
    request_id?: string
  },
): Promise<ChatMessageRow> {
  const r = await post<{ message: ChatMessageRow }>(
    `/api/chat/sessions/${encodeURIComponent(sessionId)}/messages`,
    body,
  )
  const d = ensureOk(r)
  if (!d.message) throw new Error('未返回 message')
  return d.message
}
