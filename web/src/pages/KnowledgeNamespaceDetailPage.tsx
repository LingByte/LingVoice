import { Button, Input, Message, Pagination, Popconfirm, Select, Space, Table, Typography, Upload } from '@arco-design/web-react'
import type { UploadItem } from '@arco-design/web-react/es/Upload'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { AdminOnly } from '@/components/AdminOnly'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'
import {
  deleteKnowledgeDocument,
  getKnowledgeNamespace,
  listKnowledgeDocuments,
  recallTest,
  reuploadKnowledgeDocument,
  uploadKnowledgeDocument,
  type KnowledgeDocumentRow,
  type KnowledgeNamespaceRow,
} from '@/api/knowledgeAdmin'

const { Title, Paragraph, Text } = Typography

export function KnowledgeNamespaceDetailPage() {
  const navigate = useNavigate()
  const { id } = useParams()
  const namespaceID = id || ''

  const [ns, setNs] = useState<KnowledgeNamespaceRow | null>(null)
  const [nsLoading, setNsLoading] = useState(false)

  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<KnowledgeDocumentRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)
  const [status, setStatus] = useState('active')

  const [uploading, setUploading] = useState(false)
  const [rowUploadingID, setRowUploadingID] = useState<string | null>(null)

  const [testQuery, setTestQuery] = useState('')
  const [testTopK, setTestTopK] = useState(5)
  const [testMinScore, setTestMinScore] = useState(0)
  const [selectedDocId, setSelectedDocId] = useState<string | undefined>(undefined)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{
    hits: number
    expected: number
    recall_at_k: number
    precision_at_k: number
    results: Array<{ record: { id: string; title: string; content: string }; score: number }>
  } | null>(null)

  const loadNamespace = useCallback(async () => {
    if (!namespaceID) return
    setNsLoading(true)
    try {
      const data = await getKnowledgeNamespace(namespaceID)
      setNs(data.namespace)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载知识库失败')
    } finally {
      setNsLoading(false)
    }
  }, [namespaceID])

  const loadDocs = useCallback(async () => {
    if (!ns?.namespace) return
    setLoading(true)
    try {
      const data = await listKnowledgeDocuments(page, pageSize, {
        namespace: ns.namespace,
        status: status || undefined,
      })
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载文档失败')
    } finally {
      setLoading(false)
    }
  }, [ns?.namespace, page, pageSize, status])

  useEffect(() => {
    void loadNamespace()
  }, [loadNamespace])

  useEffect(() => {
    void loadDocs()
  }, [loadDocs])

  const docOptions = useMemo(
    () =>
      list.map((d) => ({
        label: `${d.title} (#${d.id})`,
        value: d.id,
      })),
    [list],
  )

  const doUpload = async (file: File) => {
    setUploading(true)
    try {
      const res = await uploadKnowledgeDocument(namespaceID, file)
      Message.success('已提交后台处理（请稍后刷新查看结果）')
      setSelectedDocId(res.document.id)
      await loadDocs()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '上传失败')
    } finally {
      setUploading(false)
    }
  }

  const doReupload = async (docId: string, file: File) => {
    setRowUploadingID(docId)
    try {
      const res = await reuploadKnowledgeDocument(docId, file)
      Message.success('已提交后台处理（请稍后刷新查看结果）')
      setSelectedDocId(res.document.id)
      await loadDocs()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '更新失败')
    } finally {
      setRowUploadingID(null)
    }
  }

  const onDeleteDoc = async (docId: string) => {
    try {
      await deleteKnowledgeDocument(docId)
      Message.success('已删除')
      if (selectedDocId && selectedDocId === docId) setSelectedDocId(undefined)
      await loadDocs()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }
  const onUploadChange = async (_: UploadItem[], current: UploadItem) => {
    const raw = current.originFile as File | undefined
    if (!raw) return
    await doUpload(raw)
  }

  const runTest = async () => {
    if (!testQuery.trim()) {
      Message.error('请输入 Query')
      return
    }
    const topK = Math.max(1, Number(testTopK) || 5)
    let minScore = Number(testMinScore) || 0
    if (minScore < 0) minScore = 0
    if (minScore > 1) minScore = 1
    setTesting(true)
    setTestResult(null)
    try {
      const res = await recallTest(namespaceID, {
        query: testQuery.trim(),
        topK,
        minScore,
        ...(selectedDocId ? { docId: selectedDocId } : {}),
      })
      setTestResult({
        hits: res.hits,
        expected: res.expected,
        recall_at_k: res.recall_at_k,
        precision_at_k: res.precision_at_k,
        results: res.results,
      })
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '召回测试失败')
    } finally {
      setTesting(false)
    }
  }

  return (
    <AdminOnly title="知识库详情">
      <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
        <div className="mb-4 flex flex-wrap items-start gap-3">
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-3">
            <Button onClick={() => navigate('/knowledge')}>返回知识库列表</Button>
            <div className="min-w-0">
              <Title heading={5} className="!m-0">
                {ns?.name || '知识库'}
              </Title>
              <Paragraph type="secondary" className="!mb-0 !mt-1 text-[13px]">
                namespace: <Text code>{ns?.namespace || '-'}</Text>
              </Paragraph>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            <Button loading={nsLoading} onClick={() => void loadNamespace()}>
              刷新
            </Button>
          </div>
        </div>

        <div className="mb-4 flex flex-wrap items-center gap-3 rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-3">
          <Upload
            multiple={false}
            showUploadList={false}
            autoUpload={false}
            disabled={!namespaceID || uploading}
            onChange={onUploadChange}
          >
            <Button type="primary" loading={uploading} disabled={!namespaceID}>
              上传文档并入库
            </Button>
          </Upload>
          <Select
            style={{ width: 160 }}
            value={status}
            options={[
              { label: 'active', value: 'active' },
              { label: 'deleted', value: 'deleted' },
            ]}
            onChange={(v) => {
              setStatus(String(v))
              setPage(1)
            }}
          />
          <Button onClick={() => void loadDocs()} loading={loading} disabled={!ns?.namespace}>
            刷新文档
          </Button>
        </div>

        <Table
          loading={loading}
          rowKey="id"
          data={list}
          pagination={false}
          scroll={{ x: 1200 }}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 120, render: (v: string) => <EllipsisCopyText text={v} maxWidth={96} copiedTip="ID 已复制" /> },
            { title: '文件名', dataIndex: 'title', width: 260, ellipsis: true, tooltip: true },
            { title: '来源', dataIndex: 'source', width: 120 },
            { title: '文件 MD5', dataIndex: 'file_hash', width: 220, render: (v: string) => <EllipsisCopyText text={v} maxWidth={200} copiedTip="MD5 已复制" /> },
            { title: '状态', dataIndex: 'status', width: 100 },
            { title: '向量 IDs', dataIndex: 'record_ids', ellipsis: true, tooltip: true },
            {
              title: '操作',
              width: 220,
              fixed: 'right' as const,
              render: (_: unknown, row: KnowledgeDocumentRow) => (
                <Space>
                  <Button type="text" size="mini" onClick={() => navigate(`/knowledge/documents/${row.id}`)}>
                    编辑
                  </Button>
                  <Upload
                    multiple={false}
                    showUploadList={false}
                    autoUpload={false}
                    disabled={rowUploadingID === row.id}
                    onChange={async (_list: UploadItem[], current: UploadItem) => {
                      const raw = current.originFile as File | undefined
                      if (!raw) return
                      await doReupload(row.id, raw)
                    }}
                  >
                    <Button type="text" size="mini" loading={rowUploadingID === row.id}>
                      重新上传
                    </Button>
                  </Upload>
                  <Popconfirm title="确定删除该文档？（会删除 Qdrant points）" onOk={() => void onDeleteDoc(row.id)}>
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

        <div className="mt-6 rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-4">
          <Title heading={6} className="!mb-2 !mt-0">
            召回测试
          </Title>
          <Paragraph type="secondary" className="!mb-3 !mt-0 text-[13px]">
            输入 Query 后检索该知识库。可选“对某个文档计算 recall@k / precision@k”（基于该文档写入的 record_ids）。
          </Paragraph>
          <div className="flex flex-wrap items-center gap-3">
            <Input
              style={{ width: 360 }}
              placeholder="Query，例如：介绍一下陈挺"
              value={testQuery}
              onChange={setTestQuery}
              allowClear
            />
            <Input
              style={{ width: 120 }}
              placeholder="TopK"
              value={String(testTopK)}
              onChange={(v) => setTestTopK(Number(v) || 5)}
            />
            <Input
              style={{ width: 140 }}
              placeholder="MinScore (0~1)"
              value={String(testMinScore)}
              onChange={(v) => setTestMinScore(Number(v) || 0)}
            />
            <Select
              allowClear
              style={{ width: 280 }}
              placeholder="选择文档计算 recall/precision（可选）"
              options={docOptions}
              value={selectedDocId}
              onChange={(v) => setSelectedDocId(v ? String(v) : undefined)}
            />
            <Button type="primary" loading={testing} onClick={() => void runTest()} disabled={!ns?.namespace}>
              开始测试
            </Button>
          </div>

          {testResult ? (
            <div className="mt-4">
              <div className="mb-2 text-[13px] text-[var(--color-text-2)]">
                <Space>
                  <span>
                    hits: <Text code>{testResult.hits}</Text>
                  </span>
                  <span>
                    expected: <Text code>{testResult.expected}</Text>
                  </span>
                  <span>
                    recall@k: <Text code>{testResult.recall_at_k.toFixed(4)}</Text>
                  </span>
                  <span>
                    precision@k: <Text code>{testResult.precision_at_k.toFixed(4)}</Text>
                  </span>
                </Space>
              </div>
              <Table
                rowKey={(r) => r.record.id}
                data={testResult.results}
                pagination={false}
                columns={[
                  { title: 'Score', dataIndex: 'score', width: 120, render: (v: number) => v.toFixed(6) },
                  {
                    title: 'ID',
                    width: 220,
                    render: (_: unknown, item: any) => (
                      <EllipsisCopyText text={String(item?.record?.id || '')} maxWidth={200} copiedTip="ID 已复制" />
                    ),
                  },
                  {
                    title: 'Title',
                    width: 240,
                    ellipsis: true,
                    tooltip: true,
                    render: (_: unknown, item: any) => String(item?.record?.title || ''),
                  },
                  {
                    title: 'Content',
                    ellipsis: true,
                    tooltip: true,
                    render: (_: unknown, item: any) => String(item?.record?.content || ''),
                  },
                ]}
              />
            </div>
          ) : null}
        </div>
      </div>
    </AdminOnly>
  )
}

