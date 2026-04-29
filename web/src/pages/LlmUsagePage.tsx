import {
  Button,
  Input,
  Message,
  Modal,
  Pagination,
  Select,
  Spin,
  Table,
  Typography,
} from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { BarChart3 } from 'lucide-react'
import {
  type LLMUsageRow,
  getLLMUsage,
  getMyLLMUsage,
  listLLMUsage,
  listMyLLMUsage,
} from '@/api/llmUsage'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'

const { Title, Paragraph, Text } = Typography

type DetailKV = { key: string; label: string; value: string }

function fmtTime(s?: string): string {
  if (!s || !String(s).trim()) return '—'
  return String(s)
}

function fmtBool(v: boolean): string {
  return v ? '是' : '否'
}

export type LlmUsagePageProps = {
  /** admin：全量查询（可按 user_id 筛选）；user：仅当前登录用户自己的记录 */
  variant?: 'admin' | 'user'
}

export function LlmUsagePage({ variant = 'admin' }: LlmUsagePageProps) {
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<LLMUsageRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)

  const [qUser, setQUser] = useState('')
  const [qChannel, setQChannel] = useState('')
  const [qRequest, setQRequest] = useState('')
  const [qProvider, setQProvider] = useState('')
  const [qModel, setQModel] = useState('')
  const [qSuccess, setQSuccess] = useState<string>('')

  const [aUser, setAUser] = useState('')
  const [aChannel, setAChannel] = useState('')
  const [aRequest, setARequest] = useState('')
  const [aProvider, setAProvider] = useState('')
  const [aModel, setAModel] = useState('')
  const [aSuccess, setASuccess] = useState<string>('')

  const [detailOpen, setDetailOpen] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailRow, setDetailRow] = useState<LLMUsageRow | null>(null)

  const listParams = useMemo(() => {
    let success: boolean | undefined
    if (aSuccess === 'true') success = true
    else if (aSuccess === 'false') success = false
    const ch = Number.parseInt(aChannel.trim(), 10)
    return {
      page,
      pageSize,
      ...(variant === 'admin' && aUser.trim() ? { user_id: aUser.trim() } : {}),
      ...(Number.isFinite(ch) && ch > 0 ? { channel_id: ch } : {}),
      ...(aRequest.trim() ? { request_id: aRequest.trim() } : {}),
      ...(aProvider.trim() ? { provider: aProvider.trim() } : {}),
      ...(aModel.trim() ? { model: aModel.trim() } : {}),
      ...(success !== undefined ? { success } : {}),
    }
  }, [variant, page, pageSize, aUser, aChannel, aRequest, aProvider, aModel, aSuccess])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data =
        variant === 'user' ? await listMyLLMUsage(listParams) : await listLLMUsage(listParams)
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [listParams, variant])

  useEffect(() => {
    void load()
  }, [load])

  const applyFilters = () => {
    setAUser(qUser)
    setAChannel(qChannel)
    setARequest(qRequest)
    setAProvider(qProvider)
    setAModel(qModel)
    setASuccess(qSuccess)
    setPage(1)
  }

  const resetFilters = () => {
    setQUser('')
    setQChannel('')
    setQRequest('')
    setQProvider('')
    setQModel('')
    setQSuccess('')
    setAUser('')
    setAChannel('')
    setARequest('')
    setAProvider('')
    setAModel('')
    setASuccess('')
    setPage(1)
  }

  const detailMetaRows = useMemo((): DetailKV[] => {
    if (!detailRow) return []
    const r = detailRow
    return [
      { key: 'id', label: 'ID', value: r.id },
      { key: 'request_id', label: 'request_id', value: r.request_id },
      { key: 'user_id', label: 'user_id', value: r.user_id || '—' },
      { key: 'channel_id', label: 'channel_id', value: r.channel_id ? String(r.channel_id) : '—' },
      { key: 'provider', label: 'provider', value: r.provider },
      { key: 'model', label: 'model', value: r.model },
      { key: 'request_type', label: 'request_type', value: r.request_type },
      { key: 'base_url', label: 'base_url', value: r.base_url || '—' },
      { key: 'input_tokens', label: 'input_tokens', value: String(r.input_tokens) },
      { key: 'output_tokens', label: 'output_tokens', value: String(r.output_tokens) },
      { key: 'total_tokens', label: 'total_tokens', value: String(r.total_tokens) },
      { key: 'quota_delta', label: 'quota_delta', value: String(r.quota_delta ?? 0) },
      { key: 'latency_ms', label: 'latency_ms', value: String(r.latency_ms) },
      { key: 'ttft_ms', label: 'ttft_ms', value: String(r.ttft_ms) },
      { key: 'tps', label: 'tps', value: String(r.tps) },
      { key: 'queue_time_ms', label: 'queue_time_ms', value: String(r.queue_time_ms) },
      { key: 'status_code', label: 'status_code', value: String(r.status_code) },
      { key: 'success', label: 'success', value: fmtBool(r.success) },
      { key: 'error_code', label: 'error_code', value: r.error_code || '—' },
      { key: 'user_agent', label: 'user_agent', value: r.user_agent || '—' },
      { key: 'ip_address', label: 'ip_address', value: r.ip_address || '—' },
      { key: 'requested_at', label: 'requested_at', value: fmtTime(r.requested_at) },
      { key: 'started_at', label: 'started_at', value: fmtTime(r.started_at) },
      { key: 'first_token_at', label: 'first_token_at', value: fmtTime(r.first_token_at) },
      { key: 'completed_at', label: 'completed_at', value: fmtTime(r.completed_at) },
      { key: 'created_at', label: 'created_at', value: fmtTime(r.created_at) },
      { key: 'updated_at', label: 'updated_at', value: fmtTime(r.updated_at) },
    ]
  }, [detailRow])

  const openDetail = (row: LLMUsageRow) => {
    setDetailOpen(true)
    setDetailRow(null)
    setDetailLoading(true)
    void (async () => {
      try {
        const full = variant === 'user' ? await getMyLLMUsage(row.id) : await getLLMUsage(row.id)
        setDetailRow(full)
      } catch (e) {
        Message.error(e instanceof Error ? e.message : '加载详情失败')
        setDetailOpen(false)
      } finally {
        setDetailLoading(false)
      }
    })()
  }

  const columns = useMemo(() => {
    const userCol =
      variant === 'admin'
        ? [
            {
              title: '用户',
              dataIndex: 'user_id',
              width: 112,
              render: (v: string) => (
                <EllipsisCopyText
                  className="tabular-nums"
                  text={v || '—'}
                  maxWidth={96}
                  copiedTip="user_id 已复制"
                  tooltipMaxLen={80}
                />
              ),
            },
          ]
        : []
    return [
    {
      title: '请求 ID',
      dataIndex: 'request_id',
      width: 200,
      render: (v: string) => (
        <EllipsisCopyText text={v} maxWidth={184} copiedTip="请求 ID 已复制" tooltipMaxLen={200} />
      ),
    },
    ...userCol,
    {
      title: '提供商',
      dataIndex: 'provider',
      width: 100,
      render: (v: string) => <EllipsisCopyText text={v || '—'} maxWidth={88} copiedTip="已复制" />,
    },
    {
      title: '模型',
      dataIndex: 'model',
      width: 160,
      render: (v: string) => <EllipsisCopyText text={v ?? ''} maxWidth={144} copiedTip="模型已复制" />,
    },
    {
      title: '渠道',
      dataIndex: 'channel_id',
      width: 72,
      render: (_: unknown, r: LLMUsageRow) => (
        <EllipsisCopyText
          className="tabular-nums"
          text={r.channel_id ? String(r.channel_id) : '—'}
          maxWidth={56}
          copiedTip="渠道 ID 已复制"
          tooltipMaxLen={48}
        />
      ),
    },
    {
      title: 'Token',
      width: 110,
      render: (_: unknown, r: LLMUsageRow) => (
        <span className="text-[12px] tabular-nums">
          {r.input_tokens}/{r.output_tokens}
        </span>
      ),
    },
    {
      title: '额度Δ',
      width: 72,
      render: (_: unknown, r: LLMUsageRow) => (
        <span className="tabular-nums text-[12px]">{r.quota_delta ?? 0}</span>
      ),
    },
    {
      title: '延迟(ms)',
      dataIndex: 'latency_ms',
      width: 96,
      render: (v: number) => <span className="tabular-nums">{v ?? 0}</span>,
    },
    {
      title: '成功',
      dataIndex: 'success',
      width: 72,
      render: (v: boolean) => (v ? <Text type="success">是</Text> : <Text type="error">否</Text>),
    },
    {
      title: '完成时间',
      dataIndex: 'completed_at',
      width: 180,
      render: (v: string) => <EllipsisCopyText text={fmtTime(v)} maxWidth={164} copyable={false} />,
    },
    {
      title: '操作',
      width: 88,
      fixed: 'right' as const,
      render: (_: unknown, r: LLMUsageRow) => (
        <Button type="text" size="mini" onClick={() => openDetail(r)}>
          详情
        </Button>
      ),
    },
  ]
  }, [variant])

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <div className="mb-3 flex shrink-0 items-center gap-2">
        <BarChart3 size={20} strokeWidth={1.85} className="text-[var(--color-text-2)]" />
        <Title heading={5} className="!mb-0 !mt-0">
          {variant === 'user' ? '使用日志' : 'LLM 用量'}
        </Title>
      </div>
      <Paragraph type="secondary" className="!mb-4 !mt-0 max-w-3xl text-[13px]">
        {variant === 'user'
          ? '本页展示当前登录账号的 LLM 调用记录，支持按渠道、模型、请求标识与时间范围筛选；详情含请求与响应正文。数据仅供查阅。'
          : '管理员可分页查询全部用户的 LLM 用量，并按用户、渠道、模型等条件筛选；详情含渠道 id、多渠道路由尝试 channel_attempts、请求与响应正文。不提供编辑与删除。'}
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-end gap-3">
        {variant === 'admin' ? (
          <Input
            addBefore="用户"
            style={{ width: 160 }}
            value={qUser}
            onChange={setQUser}
            placeholder="user_id"
          />
        ) : null}
        <Input
          addBefore="渠道ID"
          style={{ width: 120 }}
          value={qChannel}
          onChange={setQChannel}
          placeholder="llm_channels.id"
        />
        <Input
          addBefore="请求"
          style={{ width: 200 }}
          value={qRequest}
          onChange={setQRequest}
          placeholder="request_id"
        />
        <Input
          addBefore="提供商"
          style={{ width: 120 }}
          value={qProvider}
          onChange={setQProvider}
          placeholder="openai"
        />
        <Input addBefore="模型" style={{ width: 140 }} value={qModel} onChange={setQModel} placeholder="gpt-4o" />
        <div className="flex items-center gap-1">
          <Text type="secondary" className="shrink-0 whitespace-nowrap text-[12px]">
            成功
          </Text>
          <Select
            style={{ width: 100 }}
            value={qSuccess || undefined}
            onChange={(v) => setQSuccess(v == null ? '' : String(v))}
            placeholder="全部"
            allowClear
            options={[
              { label: '是', value: 'true' },
              { label: '否', value: 'false' },
            ]}
          />
        </div>
        <Button type="primary" onClick={applyFilters}>
          查询
        </Button>
        <Button onClick={resetFilters}>重置</Button>
      </div>

      <div className="min-h-0 flex-1 min-w-0">
        <Spin loading={loading} className="block w-full">
          <Table
            rowKey="id"
            columns={columns}
            data={list}
            pagination={false}
            borderCell
            scroll={{ x: variant === 'admin' ? 1320 : 1200 }}
          />
        </Spin>
      </div>

      <div className="mt-4 flex shrink-0 justify-end">
        <Pagination
          current={page}
          pageSize={pageSize}
          total={total}
          showTotal
          sizeCanChange
          pageSizeChangeResetCurrent
          onChange={(p, ps) => {
            setPage(p)
            setPageSize(ps)
          }}
        />
      </div>

      <Modal
        title="用量详情"
        visible={detailOpen}
        onCancel={() => setDetailOpen(false)}
        footer={
          <Button type="primary" onClick={() => setDetailOpen(false)}>
            关闭
          </Button>
        }
        style={{ width: 'min(920px, 96vw)' }}
        unmountOnExit
      >
        {detailLoading ? (
          <div className="flex justify-center py-10">
            <Spin />
          </div>
        ) : detailRow ? (
          <div className="max-h-[min(72vh,720px)] overflow-y-auto pr-1">
            <Table<DetailKV>
              size="small"
              borderCell
              pagination={false}
              rowKey="key"
              scroll={{ y: 260 }}
              columns={[
                {
                  title: '字段',
                  dataIndex: 'label',
                  width: 140,
                  className: '!bg-[var(--color-fill-2)] !font-medium !text-[var(--color-text-2)]',
                },
                {
                  title: '值',
                  dataIndex: 'value',
                  render: (v: string) => (
                    <span className="break-all font-mono text-[12px] text-[var(--color-text-1)]">{v}</span>
                  ),
                },
              ]}
              data={detailMetaRows}
            />
            {detailRow.channel_attempts && detailRow.channel_attempts.length > 0 ? (
              <div className="mt-3">
                <Text className="mb-1 block text-[13px] font-medium">渠道路由尝试 channel_attempts</Text>
                <pre className="max-h-56 overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-2 font-mono text-[12px]">
                  {JSON.stringify(detailRow.channel_attempts, null, 2)}
                </pre>
              </div>
            ) : null}
            {detailRow.error_message ? (
              <div className="mt-3">
                <Text className="mb-1 block text-[13px] font-medium text-[var(--color-danger-6)]">错误信息</Text>
                <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-2 font-mono text-[12px]">
                  {detailRow.error_message}
                </pre>
              </div>
            ) : null}
            <div className="mt-3">
              <Text className="mb-1 block text-[13px] font-medium">请求内容 request_content</Text>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-2 font-mono text-[12px]">
                {detailRow.request_content?.trim() ? detailRow.request_content : '—'}
              </pre>
            </div>
            <div className="mt-3">
              <Text className="mb-1 block text-[13px] font-medium">响应内容 response_content</Text>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-2 font-mono text-[12px]">
                {detailRow.response_content?.trim() ? detailRow.response_content : '—'}
              </pre>
            </div>
          </div>
        ) : null}
      </Modal>
    </div>
  )
}
