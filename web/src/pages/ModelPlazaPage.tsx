import {
  Button,
  Grid,
  Input,
  Message,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from '@arco-design/web-react'
import type { ColumnProps } from '@arco-design/web-react/es/Table'
import { Copy, LayoutGrid, LayoutList, Search } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  getLLMModelPlaza,
  type LLMModelPlazaCatalogItem,
  type LLMModelPlazaData,
  type PlazaBillingCount,
  type PlazaGroupCount,
  type PlazaVendorCount,
} from '@/api/llmModelPlaza'
import { ModelPlazaDetailModal } from '@/components/modelPlaza/ModelPlazaDetailModal'
import { isAdminRole } from '@/utils/authz'
import { resolveModelCardIcon } from '@/utils/modelVendorIcon'
import { useAuthStore } from '@/stores/authStore'

const { Title, Paragraph, Text } = Typography
const { Row, Col } = Grid

const VENDOR_PREVIEW = 14

function vendorLabel(v: string): string {
  return v === '__empty__' ? '未填写供应商' : v
}

function billingLabel(mode?: string | null): string {
  const m = (mode || '').toLowerCase().trim()
  return m === 'times' ? '按次计费' : '按量计费'
}

function fmtCompact(n?: number | null): string {
  if (n == null) return '—'
  if (n >= 1_000_000) return `${(n / 1e6).toFixed(1)}M`
  if (n >= 10_000) return `${(n / 1e3).toFixed(1)}K`
  if (n >= 1000) return `${Math.round(n / 100) / 10}K`
  return String(n)
}

function fmtUsd4(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—'
  return `$${n.toFixed(4)}`
}

function billingCountRows(rows: PlazaBillingCount[], key: string): number {
  const k = key.toLowerCase()
  const hit = rows.find((x) => String(x.billing || '').toLowerCase() === k)
  return hit ? Number(hit.count) || 0 : 0
}

function priceTokenPer1M(
  item: LLMModelPlazaCatalogItem,
  usdPerQuota: number,
  kind: 'in' | 'out',
): number {
  const mr = item.quota_model_ratio ?? 1
  if (kind === 'in') {
    const pr = item.quota_prompt_ratio ?? 1
    return usdPerQuota * 1e6 * mr * pr
  }
  const cr = item.quota_completion_ratio ?? 1
  return usdPerQuota * 1e6 * mr * cr
}

function priceTimesPerCall(item: LLMModelPlazaCatalogItem, usdPerQuota: number): number {
  return usdPerQuota * (item.quota_model_ratio ?? 1)
}

function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message
  const o = e as { msg?: string }
  if (o && typeof o.msg === 'string') return o.msg
  return '加载失败'
}

type FilterChipProps = {
  active: boolean
  label: string
  onClick: () => void
}

function FilterChip(props: FilterChipProps) {
  return (
    <button
      type="button"
      onClick={props.onClick}
      className={`mb-1.5 mr-1.5 inline-flex max-w-full items-center rounded-md border px-2 py-1 text-left text-[12px] transition-colors ${
        props.active
          ? 'border-[rgb(59,130,246)] bg-[rgb(239,246,255)] text-[rgb(30,64,175)]'
          : 'border-[var(--color-border-2)] bg-[var(--color-bg-2)] text-[var(--color-text-2)] hover:border-[var(--color-border-3)]'
      }`}
    >
      <span className="truncate">{props.label}</span>
    </button>
  )
}

export function ModelPlazaPage() {
  const user = useAuthStore((s) => s.user)
  const admin = isAdminRole(user?.role)

  const [loading, setLoading] = useState(false)
  const [payload, setPayload] = useState<LLMModelPlazaData | null>(null)
  const [vendor, setVendor] = useState('')
  const [group, setGroup] = useState('')
  const [billing, setBilling] = useState('')
  const [qInput, setQInput] = useState('')
  const [q, setQ] = useState('')
  const [vendorExpanded, setVendorExpanded] = useState(false)

  const [showRechargeUsd, setShowRechargeUsd] = useState(true)
  const [showRatios, setShowRatios] = useState(false)
  const [tableView, setTableView] = useState(false)
  const [detailItem, setDetailItem] = useState<LLMModelPlazaCatalogItem | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await getLLMModelPlaza({
        ...(q.trim() ? { q: q.trim() } : {}),
        ...(vendor ? { vendor } : {}),
        ...(billing ? { billing } : {}),
        ...(group ? { group } : {}),
      })
      setPayload(data)
    } catch (e) {
      Message.error(errMsg(e))
    } finally {
      setLoading(false)
    }
  }, [q, vendor, billing, group])

  useEffect(() => {
    void load()
  }, [load])

  const catalog = payload?.catalog ?? []
  const orphans = payload?.models_without_meta ?? []
  const usdPerQuota = payload?.usd_per_quota_unit ?? 0.01
  const vendorCounts = payload?.vendor_counts ?? []
  const groupCounts = payload?.group_counts ?? []
  const billingCounts = payload?.billing_counts ?? []
  const totalMeta = payload?.total_meta_enabled ?? 0
  const totalFiltered = payload?.total_filtered ?? 0

  const vendorsShown = useMemo(() => {
    const list = [...vendorCounts]
    if (!vendorExpanded && list.length > VENDOR_PREVIEW) {
      return list.slice(0, VENDOR_PREVIEW)
    }
    return list
  }, [vendorCounts, vendorExpanded])

  const showAllSuppliersHero = !vendor && !group && !billing

  const bannerTitle = useMemo(() => {
    if (showAllSuppliersHero) return '全部供应商'
    if (vendor) return vendorLabel(vendor)
    if (group) return `令牌分组 · ${group}`
    if (billing === 'token') return '按量计费模型'
    if (billing === 'times') return '按次计费模型'
    return '全部供应商'
  }, [showAllSuppliersHero, vendor, group, billing])

  const copyNames = async () => {
    const text = catalog.map((c) => c.model_name).join('\n')
    if (!text) {
      Message.info('当前列表为空')
      return
    }
    try {
      await navigator.clipboard.writeText(text)
      Message.success('已复制模型名列表')
    } catch {
      Message.error('复制失败')
    }
  }

  const copyMarkdown = async () => {
    const head = '| 模型 | 供应商 | 计费 | 输入$/1M | 补全$/1M |\n| --- | --- | --- | --- | --- |\n'
    const rows = catalog
      .map((c) => {
        const mode = (c.quota_billing_mode || '').toLowerCase() === 'times' ? '按次' : '按量'
        const pi =
          mode === '按次'
            ? fmtUsd4(priceTimesPerCall(c, usdPerQuota))
            : fmtUsd4(priceTokenPer1M(c, usdPerQuota, 'in'))
        const po =
          mode === '按次' ? '—' : fmtUsd4(priceTokenPer1M(c, usdPerQuota, 'out'))
        return `| ${c.model_name} | ${c.vendor || '—'} | ${mode} | ${pi} | ${po} |`
      })
      .join('\n')
    try {
      await navigator.clipboard.writeText(head + rows)
      Message.success('已复制 Markdown 表格')
    } catch {
      Message.error('复制失败')
    }
  }

  const resetFilters = () => {
    setVendor('')
    setGroup('')
    setBilling('')
    setQInput('')
    setQ('')
    setVendorExpanded(false)
  }

  const tableColumns: ColumnProps<LLMModelPlazaCatalogItem>[] = useMemo(
    () => [
      {
        title: '模型',
        dataIndex: 'model_name',
        width: 200,
        render: (_v: unknown, row: LLMModelPlazaCatalogItem) => (
          <span className="font-medium text-[var(--color-text-1)]">{row.model_name}</span>
        ),
      },
      {
        title: '供应商',
        dataIndex: 'vendor',
        width: 120,
        render: (v: unknown) => (typeof v === 'string' ? v : '') || '—',
      },
      {
        title: '计费',
        width: 96,
        render: (_v: unknown, row: LLMModelPlazaCatalogItem) => (
          <Tag size="small">{billingLabel(row.quota_billing_mode)}</Tag>
        ),
      },
      {
        title: showRechargeUsd ? '输入（估算）' : '路由渠道',
        width: 140,
        render: (_v: unknown, row: LLMModelPlazaCatalogItem) => {
          if (!showRechargeUsd) return <Text type="secondary">{row.routable_channel_count}</Text>
          const times = (row.quota_billing_mode || '').toLowerCase() === 'times'
          return (
            <Text type="secondary" className="text-[12px]">
              {times
                ? `${fmtUsd4(priceTimesPerCall(row, usdPerQuota))} / 次`
                : `${fmtUsd4(priceTokenPer1M(row, usdPerQuota, 'in'))} / 1M`}
            </Text>
          )
        },
      },
      {
        title: showRechargeUsd ? '补全（估算）' : '分组',
        width: 160,
        render: (_v: unknown, row: LLMModelPlazaCatalogItem) => {
          if (!showRechargeUsd) {
            const g = row.ability_groups?.slice(0, 3).join(', ') || '—'
            return (
              <Text type="secondary" className="text-[12px]">
                {g}
              </Text>
            )
          }
          const times = (row.quota_billing_mode || '').toLowerCase() === 'times'
          return (
            <Text type="secondary" className="text-[12px]">
              {times ? '—' : `${fmtUsd4(priceTokenPer1M(row, usdPerQuota, 'out'))} / 1M`}
            </Text>
          )
        },
      },
      {
        title: '上下文',
        width: 88,
        render: (_v: unknown, row: LLMModelPlazaCatalogItem) => (
          <Text type="secondary" className="text-[12px]">
            {fmtCompact(row.context_length ?? null)}
          </Text>
        ),
      },
      {
        title: '说明',
        dataIndex: 'description',
        ellipsis: true,
        render: (v: unknown) => (
          <span className="text-[12px] text-[var(--color-text-3)]">
            {typeof v === 'string' && v.trim() ? v : '—'}
          </span>
        ),
      },
    ],
    [showRechargeUsd, usdPerQuota],
  )

  const tokenTotal = billingCountRows(billingCounts, 'token')
  const timesTotal = billingCountRows(billingCounts, 'times')

  return (
    <div className="flex h-full min-h-0 w-full flex-1 overflow-hidden bg-[var(--color-fill-1)]">
      <ModelPlazaDetailModal
        visible={detailItem != null}
        item={detailItem}
        usdPerQuota={usdPerQuota}
        onClose={() => setDetailItem(null)}
      />
      {/* 左侧筛选 */}
      <aside className="hidden w-[260px] shrink-0 flex-col border-r border-[var(--color-border-2)] bg-[var(--color-bg-2)] md:flex">
        <div className="flex items-center justify-between border-b border-[var(--color-border-2)] px-3 py-2.5">
          <Text bold className="text-[13px]">
            筛选
          </Text>
          <Button type="text" size="mini" onClick={resetFilters}>
            重置
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
          <div className="mb-4">
            <Text className="mb-2 block text-[12px] text-[var(--color-text-3)]">供应商</Text>
            <div className="flex flex-wrap">
              <FilterChip
                active={!vendor}
                label={`全部供应商 (${totalMeta})`}
                onClick={() => setVendor('')}
              />
              {vendorsShown.map((vc: PlazaVendorCount) => (
                <FilterChip
                  key={vc.vendor}
                  active={vendor === vc.vendor}
                  label={`${vendorLabel(vc.vendor)} (${vc.count})`}
                  onClick={() => setVendor(vendor === vc.vendor ? '' : vc.vendor)}
                />
              ))}
            </div>
            {vendorCounts.length > VENDOR_PREVIEW ? (
              <Button type="text" size="mini" className="!px-0" onClick={() => setVendorExpanded((x) => !x)}>
                {vendorExpanded ? '收起' : '展开更多'}
              </Button>
            ) : null}
          </div>

          <div className="mb-4">
            <Text className="mb-2 block text-[12px] text-[var(--color-text-3)]">可用令牌分组</Text>
            <div className="flex flex-wrap">
              <FilterChip active={!group} label={`全部 (${totalMeta})`} onClick={() => setGroup('')} />
              {groupCounts.map((gc: PlazaGroupCount) => (
                <FilterChip
                  key={gc.group}
                  active={group === gc.group}
                  label={`${gc.group || '—'} (${gc.count})`}
                  onClick={() => setGroup(group === gc.group ? '' : gc.group)}
                />
              ))}
            </div>
          </div>

          <div>
            <Text className="mb-2 block text-[12px] text-[var(--color-text-3)]">计费类型</Text>
            <div className="flex flex-wrap">
              <FilterChip active={!billing} label={`全部类型 (${totalMeta})`} onClick={() => setBilling('')} />
              <FilterChip
                active={billing === 'token'}
                label={`按量计费 (${tokenTotal})`}
                onClick={() => setBilling(billing === 'token' ? '' : 'token')}
              />
              <FilterChip
                active={billing === 'times'}
                label={`按次计费 (${timesTotal})`}
                onClick={() => setBilling(billing === 'times' ? '' : 'times')}
              />
            </div>
          </div>
        </div>
      </aside>

      <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        <div className="shrink-0 border-b border-[var(--color-border-2)] bg-[var(--color-bg-1)] px-6 py-5">
          <div className="min-w-0 max-w-[900px]">
            <Title heading={5} className="!mb-2 !mt-0 !text-[20px] !font-semibold !tracking-tight text-[var(--color-text-1)]">
              {bannerTitle}
            </Title>
            {showAllSuppliersHero ? (
              <Paragraph className="!mb-4 !mt-0 max-w-[640px] !text-[13px] !leading-relaxed !text-[var(--color-text-2)]">
                查看所有可用的 AI 模型供应商，覆盖多家知名厂商与聚合渠道的可用模型目录；支持按供应商、令牌分组与计费类型筛选。
              </Paragraph>
            ) : null}
            <Space size={10} wrap className="!items-center">
              {showAllSuppliersHero ? (
                <Tag size="medium" color="arcoblue" className="!m-0 font-medium">
                  全部模型
                </Tag>
              ) : null}
              <Tag size="medium" className="!m-0 border-[var(--color-border-2)] bg-[var(--color-fill-2)] text-[var(--color-text-2)]">
                共 {totalFiltered} 个模型
              </Tag>
            </Space>
          </div>
        </div>

        <div className="shrink-0 border-b border-[var(--color-border-2)] bg-[var(--color-bg-1)] px-4 py-3">
          <div className="flex flex-wrap items-center gap-3">
            <Input
              allowClear
              prefix={<Search size={14} className="text-[var(--color-text-3)]" />}
              placeholder="模糊搜索模型名称"
              value={qInput}
              onChange={setQInput}
              onPressEnter={() => setQ(qInput)}
              className="max-w-[320px]"
            />
            <Button type="primary" size="small" onClick={() => setQ(qInput)}>
              搜索
            </Button>
            <Button size="small" icon={<Copy size={14} />} onClick={() => void copyNames()}>
              复制
            </Button>
            <Tooltip content="复制当前结果为 Markdown 表格">
              <Button size="small" onClick={() => void copyMarkdown()}>
                M
              </Button>
            </Tooltip>
            <div className="mx-1 h-4 w-px bg-[var(--color-border-2)]" />
            <Space size={4} className="items-center">
              <Text className="text-[12px] text-[var(--color-text-2)]">充值价格显示</Text>
              <Switch size="small" checked={showRechargeUsd} onChange={setShowRechargeUsd} />
            </Space>
            <Space size={4} className="items-center">
              <Text className="text-[12px] text-[var(--color-text-2)]">倍率</Text>
              <Switch size="small" checked={showRatios} onChange={setShowRatios} />
            </Space>
            <Space size={4} className="items-center">
              <Text className="text-[12px] text-[var(--color-text-2)]">表格视图</Text>
              <Switch
                size="small"
                checked={tableView}
                onChange={setTableView}
                checkedText={<LayoutList size={12} />}
                uncheckedText={<LayoutGrid size={12} />}
              />
            </Space>
            <Button size="small" loading={loading} onClick={() => void load()}>
              刷新
            </Button>
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-auto px-4 py-4">
          {tableView ? (
            <Table
              rowKey="id"
              loading={loading}
              data={catalog}
              columns={tableColumns}
              pagination={false}
              borderCell
              size="small"
              onRow={(record) => ({
                onClick: () => setDetailItem(record),
                style: { cursor: 'pointer' },
              })}
            />
          ) : (
            <Row gutter={[14, 14]}>
              {catalog.map((item) => {
                const icon = resolveModelCardIcon(item.model_name, item.vendor, item.icon_url)
                const times = (item.quota_billing_mode || '').toLowerCase() === 'times'
                return (
                  <Col key={item.id} xs={24} sm={12} lg={8} xl={6}>
                    <div
                      role="button"
                      tabIndex={0}
                      className="relative flex h-full flex-col rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-1)] p-4 shadow-sm outline-none transition-shadow hover:border-[rgb(var(--primary-4))] hover:shadow-md focus-visible:ring-2 focus-visible:ring-[rgb(var(--primary-5))]"
                      onClick={() => setDetailItem(item)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault()
                          setDetailItem(item)
                        }
                      }}
                    >
                      <Tooltip content="复制模型名">
                        <Button
                          type="text"
                          size="mini"
                          className="!absolute right-2 top-2 !z-[1] !h-7 !w-7 !min-w-0 !p-0"
                          icon={<Copy size={14} />}
                          onClick={async (e) => {
                            e.stopPropagation()
                            try {
                              await navigator.clipboard.writeText(item.model_name)
                              Message.success('已复制')
                            } catch {
                              Message.error('复制失败')
                            }
                          }}
                        />
                      </Tooltip>
                      <div className="mb-3 flex items-center gap-3 pr-8">
                        <div className="flex h-11 w-11 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-[var(--color-fill-2)]">
                          {icon ? (
                            <img src={icon} alt="" className="max-h-8 max-w-[90%] object-contain" />
                          ) : (
                            <Text type="secondary" className="text-[11px]">
                              AI
                            </Text>
                          )}
                        </div>
                        <div className="min-w-0 flex-1">
                          <div className="truncate font-semibold text-[var(--color-text-1)]">
                            {item.model_name}
                          </div>
                          {item.vendor ? (
                            <Text type="secondary" className="text-[12px]">
                              {item.vendor}
                            </Text>
                          ) : null}
                        </div>
                      </div>

                      {showRechargeUsd ? (
                        <div className="mb-2 space-y-0.5 text-[12px] text-[var(--color-text-2)]">
                          {times ? (
                            <div>
                              单次估算 {fmtUsd4(priceTimesPerCall(item, usdPerQuota))}{' '}
                              <Text type="secondary">（按次）</Text>
                            </div>
                          ) : (
                            <>
                              <div>
                                输入价格 {fmtUsd4(priceTokenPer1M(item, usdPerQuota, 'in'))} / 1M Tokens
                              </div>
                              <div>
                                补全价格 {fmtUsd4(priceTokenPer1M(item, usdPerQuota, 'out'))} / 1M Tokens
                              </div>
                            </>
                          )}
                        </div>
                      ) : (
                        <div className="mb-2 text-[12px] text-[var(--color-text-2)]">
                          可路由渠道 {item.routable_channel_count}
                        </div>
                      )}

                      {showRatios ? (
                        <div className="mb-2 text-[11px] text-[var(--color-text-3)]">
                          倍率 model {item.quota_model_ratio ?? 1} · prompt {item.quota_prompt_ratio ?? 1} ·
                          completion {item.quota_completion_ratio ?? 1}
                        </div>
                      ) : null}

                      {item.description ? (
                        <Paragraph
                          className="!mb-0 line-clamp-3 flex-1 !text-[12px] !leading-snug"
                          type="secondary"
                        >
                          {item.description}
                        </Paragraph>
                      ) : (
                        <div className="min-h-[40px] flex-1 text-[12px] text-[var(--color-text-4)]">暂无描述</div>
                      )}

                      <div className="mt-3 flex items-end justify-between gap-2 border-t border-[var(--color-border-1)] pt-2">
                        <Tag size="small" color="purple">
                          {billingLabel(item.quota_billing_mode)}
                        </Tag>
                        <Text type="secondary" className="text-[12px]">
                          {fmtCompact(item.context_length ?? null)}
                        </Text>
                      </div>
                    </div>
                  </Col>
                )
              })}
            </Row>
          )}

          {!loading && catalog.length === 0 ? (
            <div className="py-16 text-center text-[var(--color-text-3)]">当前筛选下暂无模型</div>
          ) : null}

          {admin && orphans.length > 0 ? (
            <div className="mt-10 rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-1)] p-4">
              <Title heading={6} className="!mb-2">
                仅有能力、未建元数据的模型
              </Title>
              <Paragraph type="secondary" className="!mb-3 !text-[13px]">
                下列名称出现在能力表中，但尚无元数据记录。建议在「模型元数据」中补全。
              </Paragraph>
              <Space wrap>
                {orphans.map((m) => (
                  <Tag key={m} size="large">
                    {m}
                  </Tag>
                ))}
              </Space>
            </div>
          ) : null}
        </div>
      </main>
    </div>
  )
}
