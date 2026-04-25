import { assertOk, type Paginated } from '@/api/mailAdmin'
import { get } from '@/utils/request'

export interface AgentRunRow {
  id: string
  session_id: string
  user_id: string
  goal: string
  status: string
  phase: string
  total_steps: number
  total_tokens: number
  max_steps: number
  max_cost_tokens: number
  max_duration_ms: number
  started_at: string
  completed_at: string
  created_at: string
  updated_at: string
}

export interface AgentRunDetail extends AgentRunRow {
  plan_json: string
  result_text: string
  error_message: string
}

export interface AgentStepRow {
  id: string
  run_id: string
  step_id: string
  task_id: string
  title: string
  instruction: string
  status: string
  model: string
  input_json: string
  output_text: string
  error_message: string
  feedback: string
  attempts: number
  input_tokens: number
  output_tokens: number
  total_tokens: number
  latency_ms: number
  started_at: string
  completed_at: string
  created_at: string
  updated_at: string
}

export type AgentRunListParams = {
  page?: number
  pageSize?: number
  user_id?: string
  session_id?: string
  status?: string
  phase?: string
  from?: string
  to?: string
}

function toQuery(p: AgentRunListParams): string {
  const q = new URLSearchParams()
  if (p.page != null) q.set('page', String(p.page))
  if (p.pageSize != null) q.set('pageSize', String(p.pageSize))
  if (p.user_id) q.set('user_id', p.user_id)
  if (p.session_id) q.set('session_id', p.session_id)
  if (p.status) q.set('status', p.status)
  if (p.phase) q.set('phase', p.phase)
  if (p.from) q.set('from', p.from)
  if (p.to) q.set('to', p.to)
  const s = q.toString()
  return s ? `?${s}` : ''
}

export async function listAgentRuns(params: AgentRunListParams): Promise<Paginated<AgentRunRow>> {
  const r = await get<Paginated<AgentRunRow>>(`/api/agent/runs${toQuery(params)}`)
  return assertOk(r)
}

export async function getAgentRun(id: string): Promise<AgentRunDetail> {
  const enc = encodeURIComponent(id)
  const r = await get<{ run: AgentRunDetail }>(`/api/agent/runs/${enc}`)
  const d = assertOk(r)
  return d.run
}

export async function listAgentRunSteps(runId: string): Promise<AgentStepRow[]> {
  const enc = encodeURIComponent(runId)
  const r = await get<{ run_id: string; list: AgentStepRow[] }>(`/api/agent/runs/${enc}/steps`)
  const d = assertOk(r)
  return d.list ?? []
}
