import { Button, Message, Modal, Table, Tag, Tooltip, Typography } from '@arco-design/web-react'
import type { ColumnProps } from '@arco-design/web-react/es/Table'
import { Coins, Copy, Info, Link2 } from 'lucide-react'
import { Fragment, useMemo } from 'react'
import type { ReactNode } from 'react'
import type { LLMModelPlazaCatalogItem } from '@/api/llmModelPlaza'
import { resolveModelCardIcon } from '@/utils/modelVendorIcon'

const { Paragraph, Text } = Typography

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

function billingLabel(mode?: string | null): string {
  const m = (mode || '').toLowerCase().trim()
  return m === 'times' ? '按次计费' : '按量计费'
}

function priceTokenPer1M(item: LLMModelPlazaCatalogItem, usdPerQuota: number, kind: 'in' | 'out'): number {
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

type GroupPriceRow = { key: string; group: string; billing: string }

function PriceSummary(props: {
  item: LLMModelPlazaCatalogItem
  usdPerQuota: number
  times: boolean
}) {
  const { item, usdPerQuota, times } = props
  if (times) {
    return (
      <div className="text-[13px] text-[var(--color-text-2)]">
        单次估算 {fmtUsd4(priceTimesPerCall(item, usdPerQuota))}{' '}
        <span className="text-[var(--color-text-3)]">（按次）</span>
      </div>
    )
  }
  return (
    <div className="space-y-0.5 text-[13px] text-[var(--color-text-2)]">
      <div>输入价格 {fmtUsd4(priceTokenPer1M(item, usdPerQuota, 'in'))} / 1M Tokens</div>
      <div>补全价格 {fmtUsd4(priceTokenPer1M(item, usdPerQuota, 'out'))} / 1M Tokens</div>
    </div>
  )
}

function Section(props: {
  icon: ReactNode
  iconClass: string
  title: string
  subtitle: string
  children: ReactNode
}) {
  return (
    <section className="border-b border-[var(--color-border-2)] pb-5 last:mb-0 last:border-0 last:pb-0">
      <div className="mb-3 flex gap-3">
        <div
          className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-white ${props.iconClass}`}
        >
          {props.icon}
        </div>
        <div className="min-w-0 flex-1">
          <Text bold className="block text-[15px] text-[var(--color-text-1)]">
            {props.title}
          </Text>
          <Text type="secondary" className="mt-0.5 block text-[12px]">
            {props.subtitle}
          </Text>
        </div>
      </div>
      <div className="pl-0 sm:pl-12">{props.children}</div>
    </section>
  )
}

type Props = {
  visible: boolean
  item: LLMModelPlazaCatalogItem | null
  usdPerQuota: number
  onClose: () => void
}

/** 模型卡片详情 */
export function ModelPlazaDetailModal(props: Props) {
  const { visible, item, usdPerQuota, onClose } = props
  const times = (item?.quota_billing_mode || '').toLowerCase() === 'times'

  const groups = item?.ability_groups?.filter(Boolean) ?? []

  const tableData: GroupPriceRow[] = useMemo(() => {
    if (!item) return []
    const billing = billingLabel(item.quota_billing_mode)
    if (groups.length === 0) {
      return [{ key: 'default', group: '默认（路由分组见能力表）', billing }]
    }
    if (groups.length > 1) {
      return [{ key: 'all-groups', group: `${groups.join('、')} 等分组`, billing }]
    }
    return [{ key: groups[0], group: `${groups[0]} 分组`, billing }]
  }, [item, groups])

  const columns: ColumnProps<GroupPriceRow>[] = useMemo(
    () => [
      {
        title: '分组',
        dataIndex: 'group',
        width: 200,
        render: (v: string) => (
          <span className="inline-flex rounded-md border border-[var(--color-border-2)] bg-[var(--color-fill-1)] px-2 py-1 text-[12px]">
            {v}
          </span>
        ),
      },
      {
        title: '计费类型',
        dataIndex: 'billing',
        width: 120,
        render: (v: string) => (
          <Tag size="small" color="purple" className="!m-0">
            {v}
          </Tag>
        ),
      },
      {
        title: '价格摘要',
        render: () =>
          item ? <PriceSummary item={item} usdPerQuota={usdPerQuota} times={times} /> : null,
      },
    ],
    [item, times, usdPerQuota],
  )

  if (!item) return null

  const icon = resolveModelCardIcon(item.model_name, item.vendor, item.icon_url)

  const titleNode = (
    <div className="flex min-w-0 w-full items-center gap-3">
      <div className="flex h-10 w-10 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-[var(--color-fill-2)]">
        {icon ? (
          <img src={icon} alt="" className="max-h-7 max-w-[90%] object-contain" />
        ) : (
          <Text type="secondary" className="text-[11px]">
            AI
          </Text>
        )}
      </div>
      <div className="min-w-0 flex-1 pr-2">
        <div className="truncate text-[16px] font-semibold text-[var(--color-text-1)]">{item.model_name}</div>
      </div>
      <div className="ml-auto shrink-0 pr-14 sm:pr-16">
        <Tooltip content="复制模型名" mini position="bottom">
          <Button
            type="text"
            size="small"
            className="!h-9 !min-w-9 !rounded-lg !text-[rgb(var(--primary-6))] hover:!bg-[var(--color-fill-2)]"
            icon={<Copy size={18} strokeWidth={2} />}
            aria-label="复制模型名"
            onClick={async () => {
              try {
                await navigator.clipboard.writeText(item.model_name)
                Message.success('已复制模型名')
              } catch {
                Message.error('复制失败')
              }
            }}
          />
        </Tooltip>
      </div>
    </div>
  )

  return (
    <Modal
      title={titleNode}
      visible={visible}
      onCancel={onClose}
      footer={null}
      maskClosable
      style={{ width: 'min(720px, 94vw)' }}
      unmountOnExit
      className="model-plaza-detail-modal"
    >
      <div className="max-h-[min(72vh,780px)] overflow-y-auto pr-1">
        <Section
          icon={<Info size={18} strokeWidth={2} />}
          iconClass="bg-[rgb(59,130,246)]"
          title="基本信息"
          subtitle="模型的详细描述和基本特性"
        >
          <Paragraph className="!mb-3 !mt-0 !text-[13px] !leading-relaxed text-[var(--color-text-2)]">
            {item.description?.trim() || '暂无描述（可在模型元数据中补充）。'}
          </Paragraph>
          <Tag color="arcoblue" size="small" className="!m-0">
            {fmtCompact(item.context_length ?? null)} 上下文
          </Tag>
        </Section>

        <Section
          icon={<Link2 size={18} strokeWidth={2} />}
          iconClass="bg-[rgb(139,92,246)]"
          title="API 端点"
          subtitle="模型支持的接口端点信息（OpenAI 兼容网关）"
        >
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-[var(--color-border-2)] bg-[var(--color-fill-1)] px-4 py-3">
            <div className="flex min-w-0 flex-1 items-center gap-2 text-[13px]">
              <span className="inline-block h-2 w-2 shrink-0 rounded-full bg-[rgb(var(--success-6))]" title="可用" />
              <span className="truncate font-mono text-[var(--color-text-2)]">openai: /v1/chat/completions</span>
            </div>
            <Tag size="small" color="green" className="!m-0 shrink-0">
              POST
            </Tag>
          </div>
        </Section>

        <Section
          icon={<Coins size={18} strokeWidth={2} />}
          iconClass="bg-[rgb(249,115,22)]"
          title="分组价格"
          subtitle="不同令牌分组下的路由与价格摘要（广场为估算）"
        >
          <div className="mb-4 flex flex-wrap items-center gap-1.5 text-[12px] text-[var(--color-text-2)]">
            <span className="text-[var(--color-text-3)]">调用链路</span>
            {groups.length === 0 ? (
              <span className="rounded-md border border-[var(--color-border-2)] bg-[var(--color-bg-2)] px-2 py-1 text-[var(--color-text-3)]">
                未配置 LLM 能力分组
              </span>
            ) : (
              <>
                <span className="rounded-md border border-[var(--color-border-2)] bg-[var(--color-fill-1)] px-2 py-1 font-medium">
                  {item.model_name}
                </span>
                {groups.map((g) => (
                  <Fragment key={g}>
                    <span className="text-[var(--color-text-4)]">→</span>
                    <span className="rounded-md border border-[var(--color-border-2)] bg-[var(--color-fill-1)] px-2 py-1">
                      {g}
                    </span>
                  </Fragment>
                ))}
              </>
            )}
          </div>
          <Table rowKey="key" columns={columns} data={tableData} pagination={false} size="small" borderCell />
          {groups.length > 1 ? (
            <Paragraph type="secondary" className="!mb-0 !mt-2 !text-[12px] !leading-relaxed">
              路由因分组而异，上表价格为按模型元数据统一估算；网关实际扣费另含凭证分组倍率（group_ratio）与用量舍入。
            </Paragraph>
          ) : null}
        </Section>
      </div>
    </Modal>
  )
}
