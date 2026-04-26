import {
  Button,
  Form,
  Input,
  Message,
  Modal,
  Pagination,
  Popconfirm,
  Space,
  Spin,
  Table,
  Typography,
} from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  createMailLog,
  deleteMailLog,
  getMailLog,
  listMailLogs,
  mailLogHtmlBody,
  type MailLogCreateBody,
  type MailLogRow,
} from '@/api/mailAdmin'

const { Title, Paragraph, Text } = Typography
const FormItem = Form.Item

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

  const [createOpen, setCreateOpen] = useState(false)
  const [createForm] = Form.useForm()

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

  const openCreate = () => {
    createForm.resetFields()
    createForm.setFieldsValue({
      user_id: 0,
      provider: 'manual',
      status: 'draft',
    })
    setCreateOpen(true)
  }

  const submitCreate = async () => {
    try {
      const v = await createForm.validate()
      const body: MailLogCreateBody = {
        user_id: Number(v.user_id) >= 0 ? Number(v.user_id) : 0,
        provider: String(v.provider ?? '').trim(),
        channel_name: String(v.channel_name ?? '').trim() || undefined,
        to_email: String(v.to_email ?? '').trim(),
        subject: String(v.subject ?? '').trim(),
        status: String(v.status ?? '').trim(),
        html_body: String(v.html_body ?? ''),
        error_msg: String(v.error_msg ?? ''),
        message_id: String(v.message_id ?? '').trim() || undefined,
        ip_address: String(v.ip_address ?? '').trim() || undefined,
      }
      await createMailLog(body)
      Message.success('已创建')
      setCreateOpen(false)
      setPage(1)
      void load()
    } catch (e) {
      if (e instanceof Error && e.message) Message.error(e.message)
    }
  }

  const onDelete = async (id: string | number) => {
    try {
      await deleteMailLog(id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
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
        分页查询、详情（直接展示 HTML 渲染预览）、新增与删除。发送记录不设「编辑」。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <Button type="primary" onClick={openCreate}>
          新增记录
        </Button>
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
          { title: 'ID', dataIndex: 'id', width: 196, ellipsis: true },
          { title: '用户', dataIndex: 'user_id', width: 80 },
          { title: 'Provider', dataIndex: 'provider', width: 120 },
          { title: '渠道名', dataIndex: 'channel_name', width: 120, ellipsis: true },
          { title: '收件人', dataIndex: 'to_email', width: 180, ellipsis: true },
          { title: '主题', dataIndex: 'subject', width: 160, ellipsis: true },
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
          { title: 'message_id', dataIndex: 'message_id', width: 140, ellipsis: true },
          { title: '创建', dataIndex: 'created_at', width: 168, ellipsis: true },
          {
            title: '操作',
            width: 160,
            fixed: 'right' as const,
            render: (_: unknown, row: MailLogRow) => (
              <Space>
                <Button type="text" size="mini" onClick={() => openDetail(row)}>
                  详情
                </Button>
                <Popconfirm title="确定删除？" onOk={() => onDelete(row.id)}>
                  <Button type="text" size="mini" status="danger">
                    删除
                  </Button>
                </Popconfirm>
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
        style={{ width: 880 }}
        unmountOnExit
      >
        {detailLoading ? (
          <div className="flex justify-center py-12">
            <Spin />
          </div>
        ) : detailRow ? (
          <div className="flex flex-col gap-3">
            <Table<DetailKV>
              size="small"
              borderCell
              pagination={false}
              rowKey="key"
              scroll={{ y: 280 }}
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
                  render: (v: string) => (
                    <span className="break-words text-[13px] text-[var(--color-text-1)]">{v}</span>
                  ),
                },
              ]}
              data={detailTableData}
            />
            <div>
              <div className="mb-1.5 text-[13px] font-medium text-[var(--color-text-1)]">HTML 预览</div>
              <iframe
                key={`preview-${detailRow.id}-${previewSrcDoc.length}`}
                title="HTML 预览"
                sandbox=""
                srcDoc={previewSrcDoc}
                className="w-full overflow-hidden rounded-md border border-[var(--color-border-2)] bg-[var(--color-bg-1)]"
                style={{ height: 'min(420px, 50vh)', minHeight: 200 }}
              />
            </div>
          </div>
        ) : null}
      </Modal>

      <Modal
        title="新增邮件日志"
        visible={createOpen}
        onOk={() => void submitCreate()}
        onCancel={() => setCreateOpen(false)}
        style={{ width: 640 }}
        unmountOnExit
      >
        <Form form={createForm} layout="vertical">
          <FormItem label="user_id" field="user_id" extra="无系统用户可填 0">
            <Input type="number" />
          </FormItem>
          <FormItem label="provider" field="provider" rules={[{ required: true }]}>
            <Input placeholder="如 manual、smtp" />
          </FormItem>
          <FormItem label="channel_name" field="channel_name">
            <Input />
          </FormItem>
          <FormItem label="to_email" field="to_email" rules={[{ required: true }]}>
            <Input />
          </FormItem>
          <FormItem label="subject" field="subject" rules={[{ required: true }]}>
            <Input />
          </FormItem>
          <FormItem label="status" field="status" rules={[{ required: true }]}>
            <Input placeholder="如 draft、sent、failed" />
          </FormItem>
          <FormItem label="html_body" field="html_body">
            <Input.TextArea placeholder="可选；填写后详情里可预览渲染" autoSize={{ minRows: 6, maxRows: 16 }} />
          </FormItem>
          <FormItem label="error_msg" field="error_msg">
            <Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} />
          </FormItem>
          <FormItem label="message_id" field="message_id">
            <Input />
          </FormItem>
          <FormItem label="ip_address" field="ip_address">
            <Input />
          </FormItem>
        </Form>
      </Modal>
    </div>
  )
}
