import { Message, Select, Table, Typography } from '@arco-design/web-react'
import { type ReactNode, useCallback, useEffect, useMemo, useState } from 'react'
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { Activity, BarChart3, Coins, Gauge, LayoutGrid, Sparkles, TrendingUp, Users } from 'lucide-react'
import { getDashboardOverview, type DashboardOverview } from '@/api/dashboard'
import { fetchAuthMe } from '@/api/auth'
import { useAuthStore } from '@/stores/authStore'
import { cn } from '@/lib/cn'

const { Title, Text } = Typography

const usdFmt = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', minimumFractionDigits: 2 })
const numFmt = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 })
const CHART_COLORS = ['#165DFF', '#722ED1', '#0FC6C2', '#F77234', '#F5319D', '#3491FA', '#9FDB1D', '#14C9C9', '#F7BA1E', '#9E53F0']

export type DashboardChartKind =
  | 'trend'
  | 'quota_dist'
  | 'calls_dist'
  | 'model_rank'
  | 'users_rank'

function greetingCN(): string {
  const h = new Date().getHours()
  if (h >= 5 && h < 12) return '早上好'
  if (h >= 12 && h < 18) return '下午好'
  return '晚上好'
}

function StatCard(props: {
  title: string
  subtitle?: string
  children: ReactNode
  className?: string
  icon?: React.ReactNode
}) {
  return (
    <div
      className={cn(
        'relative flex min-h-[108px] flex-col overflow-hidden rounded-2xl border border-[var(--color-border-2)] bg-gradient-to-br from-[var(--color-bg-2)] to-[var(--color-fill-1)] p-4 shadow-sm',
        props.className,
      )}
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <Text className="text-[13px] font-medium text-[var(--color-text-2)]">{props.title}</Text>
        {props.icon ? <span className="text-[var(--color-text-3)] opacity-90">{props.icon}</span> : null}
      </div>
      <div className="flex flex-1 flex-col justify-center">{props.children}</div>
      {props.subtitle ? (
        <Text type="secondary" className="mt-1 block text-[11px] leading-snug">
          {props.subtitle}
        </Text>
      ) : null}
    </div>
  )
}

function shortDate(s: string): string {
  const p = (s || '').slice(5, 10)
  return p || s
}

export function DashboardPage() {
  const setUser = useAuthStore((s) => s.setUser)
  const authUser = useAuthStore((s) => s.user)
  const [days, setDays] = useState(30)
  const [loading, setLoading] = useState(true)
  const [data, setData] = useState<DashboardOverview | null>(null)
  const [chartKind, setChartKind] = useState<DashboardChartKind>('trend')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const d = await getDashboardOverview(days)
      setData(d)
      const me = await fetchAuthMe()
      if (me.kind === 'ok') setUser(me.user)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
      setData(null)
    } finally {
      setLoading(false)
    }
  }, [days, setUser])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (!data?.is_admin && chartKind === 'users_rank') {
      setChartKind('trend')
    }
  }, [data?.is_admin, chartKind])

  const displayName = useMemo(() => {
    if (data?.greeting_name) return data.greeting_name
    const dn = authUser?.displayName?.trim()
    if (dn) return dn
    const em = authUser?.email?.trim()
    if (em) {
      const at = em.indexOf('@')
      return at > 0 ? em.slice(0, at) : em
    }
    return '用户'
  }, [data, authUser])

  const trendData = useMemo(() => {
    const rows = data?.daily_series ?? []
    return rows.map((r) => ({
      label: shortDate(r.stat_date),
      stat_date: r.stat_date,
      requests: r.request_count,
      success: r.success_count,
      tokens: r.token_sum,
      quota: r.quota_sum,
    }))
  }, [data?.daily_series])

  const modelPieData = useMemo(() => {
    const m = data?.models ?? []
    return m
      .filter((x) => (x.count ?? 0) > 0 || (x.quota ?? 0) > 0)
      .map((x) => ({ name: x.model || '—', value: x.count, quota: x.quota, tokens: x.tokens }))
  }, [data?.models])

  const quotaPieData = useMemo(() => {
    const m = data?.models ?? []
    return m
      .filter((x) => (x.quota ?? 0) > 0)
      .map((x) => ({ name: x.model || '—', value: x.quota, tokens: x.tokens }))
  }, [data?.models])

  const modelRankBars = useMemo(() => {
    const m = [...(data?.models ?? [])].sort((a, b) => (b.count ?? 0) - (a.count ?? 0))
    return m.slice(0, 8).map((x) => ({ name: x.model.length > 24 ? `${x.model.slice(0, 22)}…` : x.model, calls: x.count }))
  }, [data?.models])

  const userRankBars = useMemo(() => {
    const r = data?.users_rank ?? []
    return r.map((x) => ({
      name: (x.email || x.user_id || '—').length > 28 ? `${(x.email || x.user_id).slice(0, 26)}…` : x.email || x.user_id,
      quota: x.quota_sum,
      tokens: x.token_sum,
      calls: x.success_count,
    }))
  }, [data?.users_rank])

  const modelColumns = useMemo(
    () => [
      { title: '模型', dataIndex: 'model', ellipsis: true },
      { title: '请求', dataIndex: 'count', width: 88, render: (v: number) => numFmt.format(v ?? 0) },
      { title: 'Tokens', dataIndex: 'tokens', width: 110, render: (v: number) => numFmt.format(v ?? 0) },
      { title: '额度', dataIndex: 'quota', width: 88, render: (v: number) => numFmt.format(v ?? 0) },
    ],
    [],
  )

  const chartOptions = useMemo((): { label: string; value: DashboardChartKind }[] => {
    const base: { label: string; value: DashboardChartKind }[] = [
      { label: '消耗与调用趋势', value: 'trend' },
      { label: '额度消耗分布（按模型）', value: 'quota_dist' },
      { label: '调用次数分布（按模型）', value: 'calls_dist' },
      { label: '调用次数排行', value: 'model_rank' },
    ]
    if (data?.is_admin) {
      base.push({ label: '用户消耗排行（全站）', value: 'users_rank' })
    }
    return base
  }, [data?.is_admin])

  const ac = data?.account
  const us = data?.usage
  const rs = data?.resource
  const pf = data?.performance

  const chartTitle = useMemo(() => {
    switch (chartKind) {
      case 'trend':
        return '周期内每日趋势'
      case 'quota_dist':
        return '额度消耗分布（按模型）'
      case 'calls_dist':
        return '调用次数分布（按模型）'
      case 'model_rank':
        return '模型调用次数排行'
      case 'users_rank':
        return '用户消耗排行（额度单位，全站）'
      default:
        return ''
    }
  }, [chartKind])

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-6">
      <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
        <div>
          <div className="mb-1 flex items-center gap-2">
            <Sparkles size={20} strokeWidth={1.75} className="text-[var(--color-text-2)]" />
            <Text className="text-[13px] text-[var(--color-text-2)]">数据面板</Text>
          </div>
          <Title heading={4} className="!mb-1 !mt-0">
            {greetingCN()}，{displayName}
          </Title>
        </div>
        <div className="flex items-center gap-2">
          <Text type="secondary" className="text-[12px]">
            统计周期
          </Text>
          <Select
            size="small"
            style={{ width: 120 }}
            value={String(days)}
            onChange={(v) => setDays(Number(v) || 30)}
            options={[
              { label: '近 7 天', value: '7' },
              { label: '近 30 天', value: '30' },
              { label: '近 90 天', value: '90' },
            ]}
          />
        </div>
      </div>

      {loading && !data ? (
        <Text type="secondary">加载中…</Text>
      ) : (
        <>
          <div className="mb-5 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <StatCard
              title="当前余额（≈USD）"
              subtitle={ac?.unlimited_quota ? '无限额度' : `换算 ${ac?.usd_per_quota_unit ?? 0.01} USD / 额度单位`}
              icon={<Coins size={18} strokeWidth={1.75} />}
            >
              <div className="font-mono text-[24px] font-semibold leading-tight tabular-nums text-[var(--color-text-1)]">
                {ac?.unlimited_quota ? '∞' : usdFmt.format(ac?.balance_usd ?? 0)}
              </div>
              {!ac?.unlimited_quota ? (
                <Text type="secondary" className="mt-1 block text-[11px] tabular-nums">
                  剩余 {numFmt.format(ac?.remain_quota ?? 0)} 单位
                </Text>
              ) : null}
            </StatCard>

            <StatCard title="历史消耗（≈USD）" subtitle="累计已用额度换算" icon={<TrendingUp size={18} strokeWidth={1.75} />}>
              <div className="font-mono text-[24px] font-semibold leading-tight tabular-nums text-[var(--color-text-1)]">
                {usdFmt.format(ac?.history_spend_usd ?? 0)}
              </div>
              <Text type="secondary" className="mt-1 block text-[11px] tabular-nums">
                已用 {numFmt.format(ac?.used_quota ?? 0)} 单位
              </Text>
            </StatCard>

            <StatCard title="请求与 Token" subtitle="周期内汇总" icon={<Activity size={18} strokeWidth={1.75} />}>
              <div className="flex flex-wrap gap-x-6 gap-y-1">
                <div>
                  <Text type="secondary" className="block text-[11px]">
                    请求
                  </Text>
                  <span className="font-mono text-[20px] font-semibold tabular-nums">{numFmt.format(us?.request_count ?? 0)}</span>
                </div>
                <div>
                  <Text type="secondary" className="block text-[11px]">
                    成功
                  </Text>
                  <span className="font-mono text-[20px] font-semibold tabular-nums">{numFmt.format(us?.stat_count ?? 0)}</span>
                </div>
                <div>
                  <Text type="secondary" className="block text-[11px]">
                    Tokens
                  </Text>
                  <span className="font-mono text-[20px] font-semibold tabular-nums">{numFmt.format(rs?.stat_tokens ?? 0)}</span>
                </div>
              </div>
            </StatCard>

            <StatCard title="RPM / TPM" subtitle="按周期时长推算的平均吞吐" icon={<Gauge size={18} strokeWidth={1.75} />}>
              <div className="flex flex-wrap gap-x-6 gap-y-1">
                <div>
                  <Text type="secondary" className="block text-[11px]">
                    平均 RPM
                  </Text>
                  <span className="font-mono text-[20px] font-semibold tabular-nums">
                    {(pf?.avg_rpm ?? 0).toLocaleString('en-US', { maximumFractionDigits: 2 })}
                  </span>
                </div>
                <div>
                  <Text type="secondary" className="block text-[11px]">
                    平均 TPM
                  </Text>
                  <span className="font-mono text-[20px] font-semibold tabular-nums">{numFmt.format(Math.round(pf?.avg_tpm ?? 0))}</span>
                </div>
              </div>
              <Text type="secondary" className="mt-1 block text-[11px]">
                周期内统计额度 {usdFmt.format(rs?.stat_quota_usd ?? 0)}（{numFmt.format(rs?.stat_quota_units ?? 0)} 单位）
              </Text>
            </StatCard>
          </div>

          <div className="mb-5 rounded-2xl border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-4 shadow-sm">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-2">
                <LayoutGrid size={18} strokeWidth={1.75} className="text-[var(--color-text-2)]" />
                <Text className="text-[14px] font-semibold">图表</Text>
                <Text type="secondary" className="text-[12px]">
                  {chartTitle}
                </Text>
              </div>
              <Select
                size="small"
                style={{ minWidth: 220 }}
                value={chartKind}
                onChange={(v) => setChartKind(v as DashboardChartKind)}
                options={chartOptions}
              />
            </div>

            <div className="h-[300px] w-full min-w-0">
              {chartKind === 'trend' && (
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={trendData} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
                    <defs>
                      <linearGradient id="fillReq" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="#165DFF" stopOpacity={0.35} />
                        <stop offset="95%" stopColor="#165DFF" stopOpacity={0} />
                      </linearGradient>
                      <linearGradient id="fillTok" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="#0FC6C2" stopOpacity={0.3} />
                        <stop offset="95%" stopColor="#0FC6C2" stopOpacity={0} />
                      </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border-3)" vertical={false} />
                    <XAxis dataKey="label" tick={{ fontSize: 11 }} stroke="var(--color-text-3)" />
                    <YAxis yAxisId="l" tick={{ fontSize: 11 }} stroke="var(--color-text-3)" width={44} />
                    <YAxis yAxisId="r" orientation="right" tick={{ fontSize: 11 }} stroke="var(--color-text-3)" width={52} />
                    <Tooltip
                      contentStyle={{
                        borderRadius: 8,
                        border: '1px solid var(--color-border-2)',
                        fontSize: 12,
                      }}
                    />
                    <Legend wrapperStyle={{ fontSize: 12 }} />
                    <Area yAxisId="l" type="monotone" dataKey="success" name="成功请求" stroke="#165DFF" fill="url(#fillReq)" strokeWidth={2} />
                    <Area yAxisId="r" type="monotone" dataKey="tokens" name="Tokens" stroke="#0FC6C2" fill="url(#fillTok)" strokeWidth={2} />
                  </AreaChart>
                </ResponsiveContainer>
              )}

              {(chartKind === 'quota_dist' || chartKind === 'calls_dist') && (
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Tooltip formatter={(v: number) => numFmt.format(v)} />
                    <Legend />
                    <Pie
                      data={chartKind === 'quota_dist' ? quotaPieData : modelPieData}
                      dataKey="value"
                      nameKey="name"
                      cx="50%"
                      cy="50%"
                      innerRadius={52}
                      outerRadius={96}
                      paddingAngle={2}
                      label={({ name, percent }) => `${name} ${(percent * 100).toFixed(0)}%`}
                    >
                      {(chartKind === 'quota_dist' ? quotaPieData : modelPieData).map((_, i) => (
                        <Cell key={i} fill={CHART_COLORS[i % CHART_COLORS.length]} />
                      ))}
                    </Pie>
                  </PieChart>
                </ResponsiveContainer>
              )}

              {chartKind === 'model_rank' && (
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={modelRankBars} layout="vertical" margin={{ left: 8, right: 16, top: 8, bottom: 8 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border-3)" horizontal={false} />
                    <XAxis type="number" tick={{ fontSize: 11 }} stroke="var(--color-text-3)" />
                    <YAxis type="category" dataKey="name" width={120} tick={{ fontSize: 11 }} stroke="var(--color-text-3)" />
                    <Tooltip formatter={(v: number) => numFmt.format(v)} />
                    <Bar dataKey="calls" name="调用次数" fill="#165DFF" radius={[0, 6, 6, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              )}

              {chartKind === 'users_rank' &&
                (data?.is_admin && userRankBars.length > 0 ? (
                  <ResponsiveContainer width="100%" height="100%">
                    <BarChart data={userRankBars} layout="vertical" margin={{ left: 8, right: 16, top: 8, bottom: 8 }}>
                      <CartesianGrid strokeDasharray="3 3" stroke="var(--color-border-3)" horizontal={false} />
                      <XAxis type="number" tick={{ fontSize: 11 }} stroke="var(--color-text-3)" />
                      <YAxis type="category" dataKey="name" width={140} tick={{ fontSize: 11 }} stroke="var(--color-text-3)" />
                      <Tooltip formatter={(v: number) => numFmt.format(v)} />
                      <Legend />
                      <Bar dataKey="quota" name="额度" fill="#722ED1" radius={[0, 4, 4, 0]} />
                      <Bar dataKey="calls" name="成功请求" fill="#165DFF" radius={[0, 4, 4, 0]} />
                    </BarChart>
                  </ResponsiveContainer>
                ) : (
                  <div className="flex h-full flex-col items-center justify-center gap-2 text-[var(--color-text-3)]">
                    <Users size={28} strokeWidth={1.5} />
                    <Text type="secondary" className="text-[13px]">
                      {data?.is_admin ? '本周期暂无多用户汇总数据' : '仅管理员可查看全站用户排行'}
                    </Text>
                  </div>
                ))}
            </div>
            {(chartKind === 'quota_dist' || chartKind === 'calls_dist') && (quotaPieData.length === 0 && modelPieData.length === 0) ? (
              <Text type="secondary" className="mt-2 block text-center text-[12px]">
                暂无模型分布数据
              </Text>
            ) : null}
          </div>

          <div className="rounded-2xl border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-4 shadow-sm">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2">
                <BarChart3 size={18} strokeWidth={1.75} className="text-[var(--color-text-2)]" />
                <Text className="text-[14px] font-semibold">模型排行</Text>
              </div>
              <Text type="secondary" className="text-[12px]">
                Top 模型（按成功请求数）
              </Text>
            </div>
            <Table
              size="small"
              rowKey="model"
              pagination={false}
              columns={modelColumns}
              data={(data?.models ?? []).map((m) => ({
                model: m.model,
                count: m.count,
                tokens: m.tokens,
                quota: m.quota,
              }))}
              noDataElement={<Text type="secondary">暂无数据</Text>}
            />
          </div>
        </>
      )}
    </div>
  )
}
