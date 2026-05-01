import { Button, Input, Message, Pagination, Popconfirm, Select, Space, Table, Typography } from '@arco-design/web-react'
import { useCallback, useEffect, useState } from 'react'
import { AdminOnly } from '@/components/AdminOnly'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'
import { deleteJobletTask, listJobletTasks, type JobletTaskRow } from '@/api/jobletAdmin'

const { Title, Paragraph, Text } = Typography

export function KnowledgeTasksPage() {
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<JobletTaskRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)

  const [status, setStatus] = useState<string>('')
  const [stage, setStage] = useState<string>('')
  const [docId, setDocId] = useState<string>('')
  const [name, setName] = useState<string>('')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listJobletTasks(page, pageSize, {
        ...(status ? { status } : {}),
        ...(stage ? { stage } : {}),
        ...(docId ? { docId } : {}),
        ...(name ? { name } : {}),
      })
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载任务失败')
    } finally {
      setLoading(false)
    }
  }, [page, pageSize, status, stage, docId, name])

  useEffect(() => {
    void load()
  }, [load])

  const onDelete = async (id: string) => {
    try {
      await deleteJobletTask(id)
      Message.success('已删除')
      await load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  return (
    <AdminOnly title="知识库任务管理">
      <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
        <div className="mb-4">
          <Title heading={5} className="!m-0">
            任务管理
          </Title>
          <Paragraph type="secondary" className="!mb-0 !mt-1 text-[13px]">
            用于查看知识库上传/处理的后台任务（joblet_tasks）。
          </Paragraph>
        </div>

        <div className="mb-4 flex flex-wrap items-center gap-3 rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-3">
          <Input style={{ width: 200 }} placeholder="docId" value={docId} onChange={setDocId} allowClear />
          <Input style={{ width: 240 }} placeholder="任务名（模糊）" value={name} onChange={setName} allowClear />
          <Select
            style={{ width: 160 }}
            value={status}
            placeholder="status"
            allowClear
            options={[
              { label: 'pending', value: 'pending' },
              { label: 'scheduled', value: 'scheduled' },
              { label: 'running', value: 'running' },
              { label: 'success', value: 'success' },
              { label: 'failed', value: 'failed' },
              { label: 'canceled', value: 'canceled' },
              { label: 'retry', value: 'retry' },
              { label: 'timeout', value: 'timeout' },
              { label: 'skipped', value: 'skipped' },
            ]}
            onChange={(v) => {
              setStatus(v ? String(v) : '')
              setPage(1)
            }}
          />
          <Select
            style={{ width: 180 }}
            value={stage}
            placeholder="stage"
            allowClear
            options={[
              { label: 'submit', value: 'submit' },
              { label: 'enqueue', value: 'enqueue' },
              { label: 'dequeue', value: 'dequeue' },
              { label: 'start', value: 'start' },
              { label: 'finish', value: 'finish' },
              { label: 'retrying', value: 'retrying' },
              { label: 'dead', value: 'dead' },
              { label: 'discard', value: 'discard' },
            ]}
            onChange={(v) => {
              setStage(v ? String(v) : '')
              setPage(1)
            }}
          />
          <Button onClick={() => void load()} loading={loading}>
            刷新
          </Button>
          <Text type="secondary" className="text-[12px]">
            共 <Text code>{total}</Text> 条
          </Text>
        </div>

        <Table
          loading={loading}
          rowKey="id"
          data={list}
          pagination={false}
          scroll={{ x: 1400 }}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 180, render: (v: string) => <EllipsisCopyText text={v} maxWidth={160} copiedTip="ID 已复制" /> },
            { title: 'doc_id', dataIndex: 'doc_id', width: 110 },
            { title: 'namespace', dataIndex: 'namespace', width: 160, ellipsis: true, tooltip: true },
            { title: 'name', dataIndex: 'name', width: 180, ellipsis: true, tooltip: true },
            { title: 'status', dataIndex: 'status', width: 110 },
            { title: 'stage', dataIndex: 'stage', width: 110 },
            { title: 'attempt', dataIndex: 'attempt', width: 90 },
            { title: 'message', dataIndex: 'message', ellipsis: true, tooltip: true },
            { title: 'error', dataIndex: 'error', width: 260, ellipsis: true, tooltip: true },
            {
              title: '操作',
              fixed: 'right' as const,
              width: 120,
              render: (_: unknown, row: JobletTaskRow) => (
                <Space>
                  <Popconfirm title="确定删除该任务记录？（只删除 DB 记录，不会终止运行中的任务）" onOk={() => void onDelete(row.id)}>
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
    </AdminOnly>
  )
}

