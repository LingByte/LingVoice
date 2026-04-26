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
import { Mic2 } from 'lucide-react'
import { type SpeechUsageRow, getSpeechUsage, listSpeechUsage } from '@/api/speechUsage'

const { Title, Paragraph, Text } = Typography

type DetailKV = { key: string; label: string; value: string }

function fmtTime(s?: string): string {
  if (!s || !String(s).trim()) return '—'
  return String(s)
}

function fmtBool(v: boolean): string {
  return v ? '是' : '否'
}

function previewText(s: string, max = 80): string {
  const t = String(s ?? '').replace(/\s+/g, ' ').trim()
  if (!t) return '—'
  return t.length > max ? `${t.slice(0, max)}…` : t
}

function fmtBytes(n: number): string {
  if (n == null || n <= 0) return '—'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`
}

export function SpeechUsagePage() {
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<SpeechUsageRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)

  const [qUser, setQUser] = useState('')
  const [qKind, setQKind] = useState('')
  const [qCred, setQCred] = useState('')
  const [qChannel, setQChannel] = useState('')
  const [qRequest, setQRequest] = useState('')
  const [qProvider, setQProvider] = useState('')
  const [qSuccess, setQSuccess] = useState<string>('')

  const [aUser, setAUser] = useState('')
  const [aKind, setAKind] = useState('')
  const [aCred, setACred] = useState('')
  const [aChannel, setAChannel] = useState('')
  const [aRequest, setARequest] = useState('')
  const [aProvider, setAProvider] = useState('')
  const [aSuccess, setASuccess] = useState<string>('')

  const [detailOpen, setDetailOpen] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailRow, setDetailRow] = useState<SpeechUsageRow | null>(null)

  const listParams = useMemo(() => {
    let success: boolean | undefined
    if (aSuccess === 'true') success = true
    else if (aSuccess === 'false') success = false
    const ch = Number.parseInt(aChannel.trim(), 10)
    const cred = Number.parseInt(aCred.trim(), 10)
    return {
      page,
      pageSize,
      ...(aUser.trim() ? { user_id: aUser.trim() } : {}),
      ...(aKind.trim() ? { kind: aKind.trim() } : {}),
      ...(Number.isFinite(cred) && cred > 0 ? { credential_id: cred } : {}),
      ...(Number.isFinite(ch) && ch > 0 ? { channel_id: ch } : {}),
      ...(aRequest.trim() ? { request_id: aRequest.trim() } : {}),
      ...(aProvider.trim() ? { provider: aProvider.trim() } : {}),
      ...(success !== undefined ? { success } : {}),
    }
  }, [page, pageSize, aUser, aKind, aCred, aChannel, aRequest, aProvider, aSuccess])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listSpeechUsage(listParams)
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [listParams])

  useEffect(() => {
    void load()
  }, [load])

  const applyFilters = () => {
    setAUser(qUser)
    setAKind(qKind)
    setACred(qCred)
    setAChannel(qChannel)
    setARequest(qRequest)
    setAProvider(qProvider)
    setASuccess(qSuccess)
    setPage(1)
  }

  const resetFilters = () => {
    setQUser('')
    setQKind('')
    setQCred('')
    setQChannel('')
    setQRequest('')
    setQProvider('')
    setQSuccess('')
    setAUser('')
    setAKind('')
    setACred('')
    setAChannel('')
    setARequest('')
    setAProvider('')
    setASuccess('')
    setPage(1)
  }

  const detailMetaRows = useMemo((): DetailKV[] => {
    if (!detailRow) return []
    const r = detailRow
    return [
      { key: 'id', label: 'ID', value: r.id },
      { key: 'request_id', label: 'request_id', value: r.request_id },
      { key: 'credential_id', label: 'credential_id', value: r.credential_id ? String(r.credential_id) : '—' },
      { key: 'user_id', label: 'user_id', value: r.user_id || '—' },
      { key: 'kind', label: 'kind', value: r.kind },
      { key: 'provider', label: 'provider', value: r.provider || '—' },
      { key: 'channel_id', label: 'channel_id', value: r.channel_id ? String(r.channel_id) : '—' },
      { key: 'group', label: 'group', value: r.group || '—' },
      { key: 'request_type', label: 'request_type', value: r.request_type },
      { key: 'latency_ms', label: 'latency_ms', value: String(r.latency_ms) },
      { key: 'status_code', label: 'status_code', value: String(r.status_code) },
      { key: 'success', label: 'success', value: fmtBool(r.success) },
      { key: 'audio_input_bytes', label: 'audio_input_bytes', value: String(r.audio_input_bytes ?? 0) },
      { key: 'audio_output_bytes', label: 'audio_output_bytes', value: String(r.audio_output_bytes ?? 0) },
      { key: 'text_input_chars', label: 'text_input_chars', value: String(r.text_input_chars ?? 0) },
      { key: 'user_agent', label: 'user_agent', value: r.user_agent || '—' },
      { key: 'ip_address', label: 'ip_address', value: r.ip_address || '—' },
      { key: 'requested_at', label: 'requested_at', value: fmtTime(r.requested_at) },
      { key: 'completed_at', label: 'completed_at', value: fmtTime(r.completed_at) },
      { key: 'created_at', label: 'created_at', value: fmtTime(r.created_at) },
      { key: 'updated_at', label: 'updated_at', value: fmtTime(r.updated_at) },
    ]
  }, [detailRow])

  const openDetail = (row: SpeechUsageRow) => {
    setDetailOpen(true)
    setDetailRow(null)
    setDetailLoading(true)
    void (async () => {
      try {
        const full = await getSpeechUsage(row.id)
        setDetailRow(full)
      } catch (e) {
        Message.error(e instanceof Error ? e.message : '加载详情失败')
        setDetailOpen(false)
      } finally {
        setDetailLoading(false)
      }
    })()
  }

  const columns = [
    {
      title: '请求 ID',
      dataIndex: 'request_id',
      width: 180,
      ellipsis: true,
      render: (v: string) => <span title={v}>{previewText(v, 28)}</span>,
    },
    {
      title: '类型',
      dataIndex: 'kind',
      width: 56,
      render: (v: string) => <span className="uppercase">{v || '—'}</span>,
    },
    {
      title: '提供商',
      dataIndex: 'provider',
      width: 100,
      render: (v: string) => v || '—',
    },
    {
      title: '凭证',
      dataIndex: 'credential_id',
      width: 72,
      render: (v: number) => <span className="tabular-nums">{v ? String(v) : '—'}</span>,
    },
    {
      title: '渠道',
      dataIndex: 'channel_id',
      width: 64,
      render: (v: number) => <span className="tabular-nums">{v ? String(v) : '—'}</span>,
    },
    {
      title: '音频',
      width: 120,
      render: (_: unknown, r: SpeechUsageRow) => (
        <span className="text-[12px] tabular-nums text-[var(--color-text-2)]">
          入 {fmtBytes(r.audio_input_bytes)}
          <br />
          出 {fmtBytes(r.audio_output_bytes)}
        </span>
      ),
    },
    {
      title: '文本字数',
      dataIndex: 'text_input_chars',
      width: 84,
      render: (v: number) => <span className="tabular-nums">{v > 0 ? v : '—'}</span>,
    },
    {
      title: '延迟(ms)',
      dataIndex: 'latency_ms',
      width: 88,
      render: (v: number) => <span className="tabular-nums">{v ?? 0}</span>,
    },
    {
      title: '成功',
      dataIndex: 'success',
      width: 64,
      render: (v: boolean) => (v ? <Text type="success">是</Text> : <Text type="error">否</Text>),
    },
    {
      title: '完成时间',
      dataIndex: 'completed_at',
      width: 176,
      render: (v: string) => fmtTime(v),
    },
    {
      title: '操作',
      width: 80,
      fixed: 'right' as const,
      render: (_: unknown, r: SpeechUsageRow) => (
        <Button type="text" size="mini" onClick={() => openDetail(r)}>
          详情
        </Button>
      ),
    },
  ]

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <div className="mb-3 flex shrink-0 items-center gap-2">
        <Mic2 size={20} strokeWidth={1.85} className="text-[var(--color-text-2)]" />
        <Title heading={5} className="!mb-0 !mt-0">
          语音用量
        </Title>
      </div>
      <Paragraph type="secondary" className="!mb-4 !mt-0 max-w-3xl text-[13px]">
        管理员可见：OpenAPI ASR/TTS 调用记录（不含原始音频 base64）；支持按用户、类型、凭证与渠道筛选，详情含请求/响应摘要 JSON。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <Input
          addBefore="用户"
          style={{ width: 160 }}
          value={qUser}
          onChange={setQUser}
          placeholder="user_id"
        />
        <div className="flex items-center gap-1">
          <Text type="secondary" className="shrink-0 whitespace-nowrap text-[12px]">
            类型
          </Text>
          <Select
            style={{ width: 100 }}
            value={qKind || undefined}
            onChange={(v) => setQKind(v == null ? '' : String(v))}
            placeholder="全部"
            allowClear
            options={[
              { label: 'ASR', value: 'asr' },
              { label: 'TTS', value: 'tts' },
            ]}
          />
        </div>
        <Input
          addBefore="凭证ID"
          style={{ width: 120 }}
          value={qCred}
          onChange={setQCred}
          placeholder="credential.id"
        />
        <Input
          addBefore="渠道ID"
          style={{ width: 120 }}
          value={qChannel}
          onChange={setQChannel}
          placeholder="asr/tts_channels.id"
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
          placeholder="厂商"
        />
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
            scroll={{ x: 1100 }}
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
        title="语音用量详情"
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
                  width: 160,
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
            {detailRow.error_message ? (
              <div className="mt-3">
                <Text className="mb-1 block text-[13px] font-medium text-[var(--color-danger-6)]">错误信息</Text>
                <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-2 font-mono text-[12px]">
                  {detailRow.error_message}
                </pre>
              </div>
            ) : null}
            <div className="mt-3">
              <Text className="mb-1 block text-[13px] font-medium">请求摘要 request_content</Text>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-2 font-mono text-[12px]">
                {detailRow.request_content?.trim() ? detailRow.request_content : '—'}
              </pre>
            </div>
            <div className="mt-3">
              <Text className="mb-1 block text-[13px] font-medium">响应摘要 response_content</Text>
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
