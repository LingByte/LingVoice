import {
  Button,
  Input,
  Message,
  Modal,
  Pagination,
  Spin,
  Table,
  Tabs,
  Typography,
} from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Bot } from 'lucide-react'
import {
  type AgentRunDetail,
  type AgentRunRow,
  type AgentStepRow,
  getAgentRun,
  listAgentRuns,
  listAgentRunSteps,
} from '@/api/agentRuns'

const { Title, Paragraph, Text } = Typography
const TabPane = Tabs.TabPane

function fmtTime(s?: string): string {
  if (!s || !String(s).trim()) return '—'
  const t = String(s).trim()
  if (t.startsWith('0001-01-01')) return '—'
  return t
}

function previewText(s: string, max = 64): string {
  const t = String(s ?? '').replace(/\s+/g, ' ').trim()
  if (!t) return '—'
  return t.length > max ? `${t.slice(0, max)}…` : t
}

function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message
  const o = e as { msg?: string }
  if (o && typeof o.msg === 'string') return o.msg
  return '加载失败'
}

export function AgentRunsPage() {
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<AgentRunRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)

  const [qUser, setQUser] = useState('')
  const [qSession, setQSession] = useState('')
  const [qStatus, setQStatus] = useState('')
  const [qPhase, setQPhase] = useState('')
  const [qFrom, setQFrom] = useState('')
  const [qTo, setQTo] = useState('')

  const [aUser, setAUser] = useState('')
  const [aSession, setASession] = useState('')
  const [aStatus, setAStatus] = useState('')
  const [aPhase, setAPhase] = useState('')
  const [aFrom, setAFrom] = useState('')
  const [aTo, setATo] = useState('')

  const [detailOpen, setDetailOpen] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailRun, setDetailRun] = useState<AgentRunDetail | null>(null)
  const [steps, setSteps] = useState<AgentStepRow[]>([])
  const [stepsLoading, setStepsLoading] = useState(false)

  const listParams = useMemo(
    () => ({
      page,
      pageSize,
      ...(aUser.trim() ? { user_id: aUser.trim() } : {}),
      ...(aSession.trim() ? { session_id: aSession.trim() } : {}),
      ...(aStatus.trim() ? { status: aStatus.trim() } : {}),
      ...(aPhase.trim() ? { phase: aPhase.trim() } : {}),
      ...(aFrom.trim() ? { from: aFrom.trim() } : {}),
      ...(aTo.trim() ? { to: aTo.trim() } : {}),
    }),
    [page, pageSize, aUser, aSession, aStatus, aPhase, aFrom, aTo],
  )

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listAgentRuns(listParams)
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(errMsg(e))
    } finally {
      setLoading(false)
    }
  }, [listParams])

  useEffect(() => {
    void load()
  }, [load])

  const applyFilters = () => {
    setAUser(qUser)
    setASession(qSession)
    setAStatus(qStatus)
    setAPhase(qPhase)
    setAFrom(qFrom)
    setATo(qTo)
    setPage(1)
  }

  const resetFilters = () => {
    setQUser('')
    setQSession('')
    setQStatus('')
    setQPhase('')
    setQFrom('')
    setQTo('')
    setAUser('')
    setASession('')
    setAStatus('')
    setAPhase('')
    setAFrom('')
    setATo('')
    setPage(1)
  }

  const openDetail = (row: AgentRunRow) => {
    setDetailOpen(true)
    setDetailRun(null)
    setSteps([])
    setDetailLoading(true)
    setStepsLoading(true)
    void (async () => {
      try {
        const [run, stepList] = await Promise.all([getAgentRun(row.id), listAgentRunSteps(row.id)])
        setDetailRun(run)
        setSteps(stepList)
      } catch (e) {
        Message.error(errMsg(e))
        setDetailOpen(false)
      } finally {
        setDetailLoading(false)
        setStepsLoading(false)
      }
    })()
  }

  const metaRows = useMemo(() => {
    if (!detailRun) return []
    const r = detailRun
    return [
      { key: 'id', label: 'run id', value: r.id },
      { key: 'session_id', label: 'session_id', value: r.session_id },
      { key: 'user_id', label: 'user_id', value: r.user_id },
      { key: 'status', label: 'status', value: r.status },
      { key: 'phase', label: 'phase', value: r.phase || '—' },
      { key: 'total_steps', label: 'total_steps', value: String(r.total_steps ?? 0) },
      { key: 'total_tokens', label: 'total_tokens', value: String(r.total_tokens ?? 0) },
      { key: 'goal', label: 'goal', value: r.goal || '—' },
      { key: 'started_at', label: 'started_at', value: fmtTime(r.started_at) },
      { key: 'completed_at', label: 'completed_at', value: fmtTime(r.completed_at) },
      { key: 'created_at', label: 'created_at', value: fmtTime(r.created_at) },
      { key: 'updated_at', label: 'updated_at', value: fmtTime(r.updated_at) },
    ]
  }, [detailRun])

  const stepColumns = [
    { title: 'step', dataIndex: 'step_id', width: 120, ellipsis: true },
    { title: '状态', dataIndex: 'status', width: 100 },
    { title: '标题', dataIndex: 'title', ellipsis: true, render: (v: string) => previewText(v || '', 40) },
    {
      title: 'tokens',
      width: 88,
      render: (_: unknown, s: AgentStepRow) => (
        <span className="tabular-nums text-[12px]">{s.total_tokens ?? 0}</span>
      ),
    },
    { title: '模型', dataIndex: 'model', width: 120, ellipsis: true },
  ]

  const columns = [
    {
      title: 'Run ID',
      dataIndex: 'id',
      width: 200,
      ellipsis: true,
      render: (v: string) => <span title={v}>{previewText(v, 28)}</span>,
    },
    {
      title: '会话',
      dataIndex: 'session_id',
      width: 160,
      ellipsis: true,
      render: (v: string) => previewText(v || '', 22),
    },
    { title: '用户', dataIndex: 'user_id', width: 120, ellipsis: true },
    {
      title: '目标',
      dataIndex: 'goal',
      ellipsis: true,
      render: (v: string) => previewText(v || '', 48),
    },
    { title: '状态', dataIndex: 'status', width: 96 },
    { title: '阶段', dataIndex: 'phase', width: 100, ellipsis: true },
    {
      title: '步数 / Token',
      width: 120,
      render: (_: unknown, r: AgentRunRow) => (
        <span className="tabular-nums text-[12px]">
          {r.total_steps ?? 0} / {r.total_tokens ?? 0}
        </span>
      ),
    },
    { title: '创建时间', dataIndex: 'created_at', width: 168, render: (v: string) => fmtTime(v) },
    {
      title: '操作',
      width: 88,
      fixed: 'right' as const,
      render: (_: unknown, r: AgentRunRow) => (
        <Button type="text" size="mini" onClick={() => openDetail(r)}>
          详情
        </Button>
      ),
    },
  ]

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <div className="mb-3 flex shrink-0 items-center gap-2">
        <Bot size={20} strokeWidth={1.85} className="text-[var(--color-text-2)]" />
        <Title heading={5} className="!mb-0 !mt-0">
          Agent 运行记录
        </Title>
      </div>
      <Paragraph type="secondary" className="!mb-4 !mt-0 max-w-3xl text-[13px]">
        管理员可见：按用户、会话、状态与时间筛选；详情含计划 JSON、结果文本、错误与各步骤输入输出。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <Input addBefore="user_id" style={{ width: 140 }} value={qUser} onChange={setQUser} placeholder="可选" />
        <Input
          addBefore="session"
          style={{ width: 180 }}
          value={qSession}
          onChange={setQSession}
          placeholder="session_id"
        />
        <Input addBefore="status" style={{ width: 120 }} value={qStatus} onChange={setQStatus} placeholder="succeeded" />
        <Input addBefore="phase" style={{ width: 120 }} value={qPhase} onChange={setQPhase} placeholder="executing" />
        <Input addBefore="from" style={{ width: 200 }} value={qFrom} onChange={setQFrom} placeholder="RFC3339" />
        <Input addBefore="to" style={{ width: 200 }} value={qTo} onChange={setQTo} placeholder="RFC3339" />
        <Button type="primary" onClick={applyFilters}>
          查询
        </Button>
        <Button onClick={resetFilters}>重置</Button>
      </div>

      <div className="min-h-0 flex-1 min-w-0">
        <Spin loading={loading} className="block w-full">
          <Table rowKey="id" columns={columns} data={list} pagination={false} borderCell scroll={{ x: 1100 }} />
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
        title="Agent 运行详情"
        visible={detailOpen}
        onCancel={() => setDetailOpen(false)}
        footer={
          <Button type="primary" onClick={() => setDetailOpen(false)}>
            关闭
          </Button>
        }
        style={{ width: 'min(960px, 96vw)' }}
        unmountOnExit
      >
        {detailLoading || !detailRun ? (
          <div className="flex justify-center py-10">
            <Spin />
          </div>
        ) : (
          <Tabs defaultActiveTab="meta">
            <TabPane key="meta" title="概览">
              <Table
                size="small"
                borderCell
                pagination={false}
                rowKey="key"
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
                data={metaRows}
              />
            </TabPane>
            <TabPane key="plan" title="计划 plan_json">
              <pre className="max-h-[min(60vh,480px)] overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-3 font-mono text-[12px]">
                {detailRun.plan_json?.trim() ? detailRun.plan_json : '—'}
              </pre>
            </TabPane>
            <TabPane key="result" title="结果 / 错误">
              <Text className="mb-2 block text-[13px] font-medium">result_text</Text>
              <pre className="mb-4 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-3 font-mono text-[12px]">
                {detailRun.result_text?.trim() ? detailRun.result_text : '—'}
              </pre>
              <Text className="mb-2 block text-[13px] font-medium text-[var(--color-danger-6)]">error_message</Text>
              <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded border border-[var(--color-border-2)] bg-[var(--color-fill-2)] p-3 font-mono text-[12px]">
                {detailRun.error_message?.trim() ? detailRun.error_message : '—'}
              </pre>
            </TabPane>
            <TabPane key="steps" title={`步骤 (${steps.length})`}>
              <Spin loading={stepsLoading} className="block w-full">
                <Table
                  rowKey="id"
                  size="small"
                  borderCell
                  pagination={false}
                  columns={stepColumns}
                  data={steps}
                  scroll={{ y: 320 }}
                  expandedRowRender={(s: AgentStepRow) => (
                    <div className="space-y-3 py-1">
                      <div>
                        <Text className="mb-1 block text-[12px] font-medium">instruction</Text>
                        <pre className="max-h-32 overflow-auto whitespace-pre-wrap rounded bg-[var(--color-fill-2)] p-2 font-mono text-[11px]">
                          {s.instruction?.trim() || '—'}
                        </pre>
                      </div>
                      <div>
                        <Text className="mb-1 block text-[12px] font-medium">input_json</Text>
                        <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded bg-[var(--color-fill-2)] p-2 font-mono text-[11px]">
                          {s.input_json?.trim() || '—'}
                        </pre>
                      </div>
                      <div>
                        <Text className="mb-1 block text-[12px] font-medium">output_text</Text>
                        <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded bg-[var(--color-fill-2)] p-2 font-mono text-[11px]">
                          {s.output_text?.trim() || '—'}
                        </pre>
                      </div>
                      {s.error_message?.trim() ? (
                        <div>
                          <Text className="mb-1 block text-[12px] font-medium text-[var(--color-danger-6)]">
                            step error
                          </Text>
                          <pre className="max-h-28 overflow-auto whitespace-pre-wrap rounded bg-[var(--color-fill-2)] p-2 font-mono text-[11px]">
                            {s.error_message}
                          </pre>
                        </div>
                      ) : null}
                    </div>
                  )}
                />
              </Spin>
            </TabPane>
          </Tabs>
        )}
      </Modal>
    </div>
  )
}
