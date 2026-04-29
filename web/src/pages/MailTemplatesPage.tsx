import { Button, Message, Pagination, Popconfirm, Space, Table, Typography } from '@arco-design/web-react'
import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { deleteMailTemplate, listMailTemplates, type MailTemplateRow } from '@/api/mailAdmin'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'

const { Title, Paragraph } = Typography

export function MailTemplatesPage() {
  const navigate = useNavigate()
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<MailTemplateRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listMailTemplates(page, pageSize)
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [page, pageSize])

  useEffect(() => {
    void load()
  }, [load])

  const onDelete = async (id: string) => {
    try {
      await deleteMailTemplate(id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        邮件模版
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
        模版在独立页面编辑，支持 HTML 实时预览。占位符变量由后端根据内容自动解析。当前接口未鉴权。
      </Paragraph>

      <div className="mb-4">
        <Button type="primary" onClick={() => navigate('/notify/mail-templates/new')}>
          新建模版
        </Button>
      </div>

      <Table
        loading={loading}
        rowKey="id"
        data={list}
        pagination={false}
        scroll={{ x: 1000 }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 90, render: (v: string) => <EllipsisCopyText text={v} maxWidth={70} copiedTip="ID 已复制" /> },
          { title: 'Code', dataIndex: 'code', width: 180, render: (v: string) => <EllipsisCopyText text={v} maxWidth={160} copiedTip="Code 已复制" /> },
          { title: '名称', dataIndex: 'name', width: 220, render: (v: string) => <EllipsisCopyText text={v} maxWidth={200} copiedTip="名称已复制" /> },
          { title: '语言', dataIndex: 'locale', width: 100 },
          {
            title: '启用',
            dataIndex: 'enabled',
            width: 72,
            render: (v: boolean) => (v ? '是' : '否'),
          },
          { title: '更新', dataIndex: 'updateAt', width: 180, render: (v?: string) => <EllipsisCopyText text={v ?? ''} maxWidth={164} copyable={false} /> },
          {
            title: '操作',
            width: 180,
            fixed: 'right' as const,
            render: (_: unknown, row: MailTemplateRow) => (
              <Space>
                <Button type="text" size="mini" onClick={() => navigate(`/notify/mail-templates/${row.id}`)}>
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
