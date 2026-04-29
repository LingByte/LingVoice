import {
  Button,
  Input,
  Message,
  Modal,
  Pagination,
  Space,
  Spin,
  Table,
  Typography,
} from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  getMailLog,
  listMailLogs,
  mailLogHtmlBody,
  type MailLogRow,
} from '@/api/mailAdmin'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'

const { Title, Paragraph, Text } = Typography

type DetailKV = { key: string; label: string; value: string }

/** 片段型邮件 HTML 包成完整文档，iframe srcDoc 才能稳定排版渲染。 */
function wrapMailLogHtmlForPreview(fragment: string): string {
  const body =
    fragment.trim() ||
    '<p style="color:#86909c;font-family:system-ui;padding:8px">暂无 HTML 正文。后端字段为 <code>html_body</code>（longtext）；真实发信成功后会写入。</p>'
  return `<!DOCTYPE html><html><head><meta charset="utf-8"/><meta name="viewport" content="width=device-width,initial-scale=1"/><base target="_blank"/>
<style>html,body{height:auto;margin:0;padding:12px;font-size:14px;line-height:1.55;word-break:break-word;font-family:system-ui,-apple-system,sans-serif;}img{max-width:100%;height:auto;}table{max-width:100%;}</style></head><body>${body}</body></html>`
}

export function MailLogsPage() {
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<MailLogRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)
  const [dUser, setDUser] = useState('')
  const [dStatus, setDStatus] = useState('')
  const [dProvider, setDProvider] = useState('')
  const [aUser, setAUser] = useState('')
  const [aStatus, setAStatus] = useState('')
  const [aProvider, setAProvider] = useState('')

  const [detailOpen, setDetailOpen] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailRow, setDetailRow] = useState<MailLogRow | null>(null)

  const filters = () => ({
    ...(aUser && !Number.isNaN(Number(aUser)) ? { user_id: Number(aUser) } : {}),
    ...(aStatus ? { status: aStatus } : {}),
    ...(aProvider ? { provider: aProvider } : {}),
  })

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listMailLogs(page, pageSize, filters())
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [page, pageSize, aUser, aStatus, aProvider])

  useEffect(() => {
    void load()
  }, [load])

  const openDetail = (row: MailLogRow) => {
    setDetailOpen(true)
    setDetailRow(null)
    setDetailLoading(true)
    void (async () => {
      try {
        const full = await getMailLog(row.id)
        setDetailRow(full)
      } catch (e) {
        Message.error(e instanceof Error ? e.message : '加载详情失败')
        setDetailOpen(false)
      } finally {
        setDetailLoading(false)
      }
    })()
  }

  const previewSrcDoc = detailRow
    ? wrapMailLogHtmlForPreview(mailLogHtmlBody(detailRow as unknown as Record<string, unknown>))
    : ''

  const detailTableData = useMemo((): DetailKV[] => {
    if (!detailRow) return []
    const html = mailLogHtmlBody(detailRow as unknown as Record<string, unknown>)
    const fmt = (s?: string) => (s && String(s).trim() ? String(s) : '—')
    return [
      { key: 'id', label: 'ID', value: String(detailRow.id) },
      { key: 'user_id', label: '用户 ID', value: String(detailRow.user_id) },
      { key: 'to', label: '收件人', value: detailRow.to_email },
      { key: 'subject', label: '主题', value: detailRow.subject },
      { key: 'status', label: '状态', value: detailRow.status },
      { key: 'provider', label: '渠道', value: [detailRow.provider, detailRow.channel_name].filter(Boolean).join(' / ') },
      { key: 'msgid', label: 'Message ID', value: fmt(detailRow.message_id) },
      { key: 'ip', label: 'IP', value: fmt(detailRow.ip_address) },
      { key: 'retry', label: '重试次数', value: String(detailRow.retry_count ?? 0) },
      { key: 'sent', label: '发送时间', value: fmt(detailRow.sent_at) },
      { key: 'created', label: '创建时间', value: fmt(detailRow.created_at) },
      { key: 'updated', label: '更新时间', value: fmt(detailRow.updated_at) },
      { key: 'html', label: 'HTML 正文', value: html.trim() ? `${html.length} 字符（见下方预览）` : '—' },
      { key: 'err', label: '错误信息', value: fmt(detailRow.error_msg) },
    ]
  }, [detailRow])

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        邮件发送日志
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
        分页查询、详情（直接展示 HTML 渲染预览）。发送记录不提供编辑/删除入口。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div>
          <div className="mb-1 text-[12px] text-[var(--color-text-3)]">user_id</div>
          <Input allowClear placeholder="可选" value={dUser} onChange={setDUser} style={{ width: 120 }} />
        </div>
        <div>
          <div className="mb-1 text-[12px] text-[var(--color-text-3)]">status</div>
          <Input allowClear placeholder="如 sent" value={dStatus} onChange={setDStatus} style={{ width: 120 }} />
        </div>
        <div>
          <div className="mb-1 text-[12px] text-[var(--color-text-3)]">provider</div>
          <Input allowClear placeholder="smtp" value={dProvider} onChange={setDProvider} style={{ width: 120 }} />
        </div>
        <Button
          type="secondary"
          onClick={() => {
            setAUser(dUser.trim())
            setAStatus(dStatus.trim())
            setAProvider(dProvider.trim())
            setPage(1)
          }}
        >
          查询
        </Button>
      </div>

      <Table
        loading={loading}
        rowKey="id"
        data={list}
        pagination={false}
        scroll={{ x: 1240 }}
        columns={[
          {
            title: 'ID',
            dataIndex: 'id',
            width: 220,
            render: (v: string | number) => <EllipsisCopyText text={v} maxWidth={200} copiedTip="ID 已复制" />,
          },
          { 
            title: '用户', 
            dataIndex: 'user_id', 
            width: 120,
            render: (v: string | number) => <EllipsisCopyText text={v} maxWidth={100} copiedTip="用户 ID 已复制" />,
          },
          { title: 'Provider', dataIndex: 'provider', width: 120, render: (v: string) => <EllipsisCopyText text={v} maxWidth={104} copiedTip="Provider 已复制" /> },
          { title: '渠道名', dataIndex: 'channel_name', width: 150, render: (v?: string) => <EllipsisCopyText text={v ?? ''} maxWidth={132} copiedTip="渠道名已复制" /> },
          { title: '收件人', dataIndex: 'to_email', width: 220, render: (v: string) => <EllipsisCopyText text={v} maxWidth={200} copiedTip="收件人已复制" /> },
          { title: '主题', dataIndex: 'subject', width: 220, render: (v: string) => <EllipsisCopyText text={v} maxWidth={200} copiedTip="主题已复制" /> },
          { title: '状态', dataIndex: 'status', width: 100 },
          {
            title: 'HTML',
            width: 72,
            render: (_: unknown, row: MailLogRow) =>
              mailLogHtmlBody(row as unknown as Record<string, unknown>).trim() ? (
                <Text type="success">有</Text>
              ) : (
                <Text type="secondary">—</Text>
              ),
          },
          { title: 'message_id', dataIndex: 'message_id', width: 200, render: (v?: string) => <EllipsisCopyText text={v ?? ''} maxWidth={180} copiedTip="Message ID 已复制" /> },
          { title: '创建', dataIndex: 'created_at', width: 180, render: (v?: string) => <EllipsisCopyText text={v ?? ''} maxWidth={164} copiedTip="创建时间已复制" copyable={false} /> },
          {
            title: '操作',
            width: 160,
            fixed: 'right' as const,
            render: (_: unknown, row: MailLogRow) => (
              <Space>
                <Button type="text" size="mini" onClick={() => openDetail(row)}>
                  详情
                </Button>
              </Space>
            ),
          },
        ]}
      />

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
        title={detailRow ? `邮件日志 #${detailRow.id}` : '详情'}
        visible={detailOpen}
        footer={null}
        onCancel={() => {
          setDetailOpen(false)
          setDetailRow(null)
        }}
        style={{ width: 1120 }}
        unmountOnExit
      >
        {detailLoading ? (
          <div className="flex justify-center py-12">
            <Spin />
          </div>
        ) : detailRow ? (
          <div className="flex gap-3">
            <div className="w-[380px] shrink-0">
              <Table<DetailKV>
                size="small"
                borderCell
                pagination={false}
                rowKey="key"
                scroll={{ y: 520 }}
                columns={[
                  {
                    title: '字段',
                    dataIndex: 'label',
                    width: 112,
                    className: '!bg-[var(--color-fill-2)] !font-medium !text-[var(--color-text-2)]',
                  },
                  {
                    title: '值',
                    dataIndex: 'value',
                    render: (v: string) => <EllipsisCopyText text={v} maxWidth={240} copiedTip="已复制" />,
                  },
                ]}
                data={detailTableData}
              />
            </div>
            <div className="min-w-0 flex-1">
              <div className="mb-1.5 text-[13px] font-medium text-[var(--color-text-1)]">HTML 预览</div>
              <iframe
                key={`preview-${detailRow.id}-${previewSrcDoc.length}`}
                title="HTML 预览"
                sandbox=""
                srcDoc={previewSrcDoc}
                className="w-full overflow-hidden rounded-md border border-[var(--color-border-2)] bg-[var(--color-bg-1)]"
                style={{ height: 520, minHeight: 520 }}
              />
            </div>
          </div>
        ) : null}
      </Modal>
    </div>
  )
}
