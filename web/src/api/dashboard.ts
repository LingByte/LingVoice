import type { ApiResponse } from '@/utils/request'
import { get } from '@/utils/request'

function ensureOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

export type DashboardModelRow = {
  model: string
  count: number
  tokens: number
  quota: number
}

/** 与后端 llm_usage_user_daily 对齐（UTC 日） */
export type DashboardDailyRow = {
  stat_date: string
  request_count: number
  success_count: number
  token_sum: number
  quota_sum: number
}

export type DashboardUserRankRow = {
  user_id: string
  email?: string
  quota_sum: number
  success_count: number
  token_sum: number
}

export type DashboardOverview = {
  greeting_name: string
  email: string
  is_admin: boolean
  period: {
    days: number
    from_rfc3339: string
    to_rfc3339: string
  }
  account: {
    remain_quota: number
    used_quota: number
    unlimited_quota: boolean
    balance_usd: number
    history_spend_usd: number
    usd_per_quota_unit: number
  }
  usage: {
    request_count: number
    stat_count: number
  }
  resource: {
    stat_quota_units: number
    stat_quota_usd: number
    stat_tokens: number
  }
  performance: {
    avg_rpm: number
    avg_tpm: number
  }
  models: DashboardModelRow[]
  daily_series: DashboardDailyRow[]
  users_rank: DashboardUserRankRow[]
}

export async function getDashboardOverview(days?: number): Promise<DashboardOverview> {
  const r = await get<DashboardOverview>('/api/dashboard/overview', {
    params: days != null && days > 0 ? { days: String(days) } : undefined,
  })
  return ensureOk(r)
}
