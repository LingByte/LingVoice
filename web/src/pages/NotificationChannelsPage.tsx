import { Button, Input, Message, Pagination, Popconfirm, Space, Table, Typography } from '@arco-design/web-react'
import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { deleteNotificationChannel, listNotificationChannels, type NotificationChannelRow } from '@/api/mailAdmin'

const { Title, Paragraph } = Typography

export function NotificationChannelsPage() {
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<NotificationChannelRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)
  const [draftType, setDraftType] = useState('')
  const [appliedType, setAppliedType] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listNotificationChannels(page, pageSize, appliedType || undefined)
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [page, pageSize, appliedType])

  useEffect(() => {
    void load()
  }, [load])

  const onDelete = async (id: number) => {
    try {
      await deleteNotificationChannel(id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        通知渠道
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
        邮件渠道编码由服务端生成；在编辑页填写 SMTP 或 SendCloud 表单。当前接口未鉴权，仅用于开发调试。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Input
          allowClear
          placeholder="筛选 type，如 email"
          value={draftType}
          onChange={setDraftType}
          style={{ width: 200 }}
        />
        <Button
          type="primary"
          onClick={() => {
            setAppliedType(draftType.trim())
            setPage(1)
          }}
        >
          查询
        </Button>
        <Button type="primary" onClick={() => navigate('/notify/channels/new')}>
          新建渠道
        </Button>
      </div>

      <Table
        loading={loading}
        rowKey="id"
        data={list}
        pagination={false}
        scroll={{ x: 960 }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 70 },
          { title: '类型', dataIndex: 'type', width: 88 },
          { title: '编码', dataIndex: 'code', width: 160, ellipsis: true },
          { title: '名称', dataIndex: 'name', width: 160, ellipsis: true },
          { title: '排序', dataIndex: 'sortOrder', width: 72 },
          {
            title: '启用',
            dataIndex: 'enabled',
            width: 80,
            render: (v: boolean) => (v ? '是' : '否'),
          },
          { title: '更新', dataIndex: 'updateAt', width: 168, ellipsis: true },
          {
            title: '操作',
            width: 180,
            fixed: 'right' as const,
            render: (_: unknown, row: NotificationChannelRow) => (
              <Space>
                <Button type="text" size="mini" onClick={() => navigate(`/notify/channels/${row.id}`)}>
                  编辑
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
    </div>
  )
}
