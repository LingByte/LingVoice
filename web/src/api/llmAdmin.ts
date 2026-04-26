import type { ApiResponse } from '@/utils/request'
import { get } from '@/utils/request'
import type { LLMChannelRow } from '@/api/channelsAdmin'
import type { LLMModelMetaRow } from '@/api/llmModelMetas'

function assertOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

export type LLMAdminChannelOption = Pick<
  LLMChannelRow,
  'id' | 'name' | 'group' | 'protocol' | 'models' | 'status'
>

export type LLMAdminFormOptions = {
  model_metas: LLMModelMetaRow[]
  channels: LLMAdminChannelOption[]
  model_name_suggestions: string[]
}

export async function getLLMAdminFormOptions(group?: string) {
  const r = await get<LLMAdminFormOptions>('/api/llm-admin/form-options', {
    params: group ? { group } : {},
  })
  return assertOk(r)
}
