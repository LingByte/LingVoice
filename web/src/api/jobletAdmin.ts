import { del, get } from '@/utils/request'
import { assertOk, type Paginated } from '@/api/mailAdmin'

export interface JobletTaskRow {
  id: string
  org_id?: number
  doc_id?: number
  namespace?: string
  name?: string
  status: string
  stage: string
  priority: number
  attempt: number
  message?: string
  error?: string
  meta_json?: string
  submitted_at?: string
  enqueued_at?: string
  started_at?: string
  finished_at?: string
  last_event_at?: string
  created_at?: string
  updated_at?: string
}

const api = {
  tasks: '/api/joblet-tasks',
}

export async function listJobletTasks(
  page: number,
  pageSize: number,
  options?: {
    orgId?: string | number
    docId?: string | number
    namespace?: string
    status?: string
    stage?: string
    name?: string
    from?: string
    to?: string
  },
) {
  const r = await get<Paginated<JobletTaskRow>>(api.tasks, {
    params: { page, pageSize, ...(options || {}) },
  })
  return assertOk(r)
}

export async function getJobletTask(id: string) {
  const r = await get<{ task: JobletTaskRow }>(`${api.tasks}/${id}`)
  return assertOk(r)
}

export async function deleteJobletTask(id: string) {
  const r = await del<{ id: string }>(`${api.tasks}/${id}`)
  return assertOk(r)
}

