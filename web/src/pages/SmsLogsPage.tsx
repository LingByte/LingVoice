import { Button, Input, Message, Modal, Pagination, Space, Table, Typography } from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'
import { getSMSLog, listSMSLogs, type SMSLogRow } from '@/api/mailAdmin'

const { Title, Paragraph } = Typography

function safeStr(v: unknown): string {
  return typeof v === 'string' ? v : v == null ? '' : String(v)
}

export function SmsLogsPage() {
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<SMSLogRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)

  const [detailOpen, setDetailOpen] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detail, setDetail] = useState<SMSLogRow | null>(null)

  const [draftProvider, setDraftProvider] = useState('')
  const [appliedProvider, setAppliedProvider] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listSMSLogs(page, pageSize, appliedProvider ? { provider: appliedProvider } : undefined)
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [page, pageSize, appliedProvider])

  useEffect(() => {
    void load()
  }, [load])

  const openDetail = async (id: string | number) => {
    setDetailOpen(true)
    setDetailLoading(true)
    try {
      const row = await getSMSLog(id)
      setDetail(row)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载详情失败')
      setDetail(null)
    } finally {
      setDetailLoading(false)
    }
  }

  const columns = useMemo(
    () => [
      { title: 'ID', dataIndex: 'id', width: 110, render: (v: string) => <EllipsisCopyText text={safeStr(v)} maxWidth={90} copiedTip="ID 已复制" /> },
      { title: 'Provider', dataIndex: 'provider', width: 110, render: (v: string) => <EllipsisCopyText text={safeStr(v)} maxWidth={94} copiedTip="Provider 已复制" /> },
      { title: '渠道名', dataIndex: 'channel_name', width: 160, render: (v?: string) => <EllipsisCopyText text={safeStr(v)} maxWidth={140} copiedTip="渠道名已复制" /> },
      { title: '收件人', dataIndex: 'to_phone', width: 160, render: (v: string) => <EllipsisCopyText text={safeStr(v)} maxWidth={140} copiedTip="手机号已复制" /> },
      { title: '模板', dataIndex: 'template', width: 140, render: (v?: string) => <EllipsisCopyText text={safeStr(v)} maxWidth={120} copiedTip="模板已复制" /> },
      { title: '状态', dataIndex: 'status', width: 110, render: (v: string) => <EllipsisCopyText text={safeStr(v)} maxWidth={94} copiedTip="状态已复制" /> },
      { title: 'MessageID', dataIndex: 'message_id', width: 160, render: (v?: string) => <EllipsisCopyText text={safeStr(v)} maxWidth={140} copiedTip="MessageID 已复制" /> },
      { title: '创建时间', dataIndex: 'created_at', width: 180, render: (v?: string) => <EllipsisCopyText text={safeStr(v)} maxWidth={164} copyable={false} /> },
      {
        title: '操作',
        width: 110,
        fixed: 'right' as const,
        render: (_: unknown, row: SMSLogRow) => (
          <Space>
            <Button type="text" size="mini" onClick={() => void openDetail(row.id)}>
              详情
            </Button>
          </Space>
        ),
      },
    ],
    [],
  )

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        短信日志
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
        仅展示当前组织（个人空间）下的短信发送记录。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Input allowClear placeholder="筛选 provider，如 yunpian" value={draftProvider} onChange={setDraftProvider} style={{ width: 220 }} />
        <Button
          type="primary"
          onClick={() => {
            setAppliedProvider(draftProvider.trim())
            setPage(1)
          }}
        >
          查询
        </Button>
      </div>

      <Table loading={loading} rowKey="id" data={list} pagination={false} scroll={{ x: 1120 }} columns={columns as any} />

      <div className="mt-4 flex justify-end">
        <Pagination
          current={page}
          pageSize={pageSize}
          total={total}
          showTotal
          onChange={(p, ps) => {
            setPage(p)
            setPageSize(ps)
          }}
        />
      </div>

      <Modal
        title="短信日志详情"
        visible={detailOpen}
        onCancel={() => setDetailOpen(false)}
        footer={null}
        style={{ width: 920 }}
      >
        {detailLoading || !detail ? (
          <div className="py-6 text-center text-[13px] text-[var(--color-text-2)]">{detailLoading ? '加载中…' : '无数据'}</div>
        ) : (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-3 text-[13px]">
              <div>
                <div className="text-[var(--color-text-3)]">ID</div>
                <EllipsisCopyText text={safeStr(detail.id)} maxWidth={360} copiedTip="ID 已复制" />
              </div>
              <div>
                <div className="text-[var(--color-text-3)]">Provider</div>
                <EllipsisCopyText text={safeStr(detail.provider)} maxWidth={360} copiedTip="Provider 已复制" />
              </div>
              <div>
                <div className="text-[var(--color-text-3)]">To</div>
                <EllipsisCopyText text={safeStr(detail.to_phone)} maxWidth={360} copiedTip="手机号已复制" />
              </div>
              <div>
                <div className="text-[var(--color-text-3)]">Status</div>
                <EllipsisCopyText text={safeStr(detail.status)} maxWidth={360} copiedTip="状态已复制" />
              </div>
              <div className="col-span-2">
                <div className="text-[var(--color-text-3)]">Content</div>
                <pre className="whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-2 text-[12px] leading-5">
                  {safeStr(detail.content)}
                </pre>
              </div>
              <div className="col-span-2">
                <div className="text-[var(--color-text-3)]">Raw</div>
                <pre className="max-h-[260px] overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-2 text-[12px] leading-5">
                  {safeStr(detail.raw)}
                </pre>
              </div>
              {detail.error_msg ? (
                <div className="col-span-2">
                  <div className="text-[var(--color-text-3)]">Error</div>
                  <pre className="whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-2 text-[12px] leading-5 text-[var(--color-danger-light-4)]">
                    {safeStr(detail.error_msg)}
                  </pre>
                </div>
              ) : null}
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}

