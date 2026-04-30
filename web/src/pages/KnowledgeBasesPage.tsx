import {
  Button,
  Drawer,
  Form,
  Input,
  InputNumber,
  Message,
  Pagination,
  Popconfirm,
  Select,
  Space,
  Table,
  Typography,
} from '@arco-design/web-react'
import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { AdminOnly } from '@/components/AdminOnly'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'
import {
  createKnowledgeNamespace,
  deleteKnowledgeNamespace,
  listKnowledgeNamespaces,
  updateKnowledgeNamespace,
  type KnowledgeNamespaceRow,
  type KnowledgeNamespaceUpsertBody,
} from '@/api/knowledgeAdmin'

const { Title, Paragraph } = Typography
const TextArea = Input.TextArea

type NamespaceFormValues = KnowledgeNamespaceUpsertBody

const STATUS_OPTIONS = [
  { label: 'active', value: 'active' },
  { label: 'deleted', value: 'deleted' },
]

export function KnowledgeBasesPage() {
  const navigate = useNavigate()

  const [nsForm] = Form.useForm<NamespaceFormValues>()

  const [namespacesLoading, setNamespacesLoading] = useState(false)
  const [namespaceList, setNamespaceList] = useState<KnowledgeNamespaceRow[]>([])
  const [namespaceTotal, setNamespaceTotal] = useState(0)
  const [namespacePage, setNamespacePage] = useState(1)
  const [namespacePageSize, setNamespacePageSize] = useState(15)
  const [namespaceStatus, setNamespaceStatus] = useState('active')

  const [nsDrawerOpen, setNsDrawerOpen] = useState(false)
  const [nsDrawerMode, setNsDrawerMode] = useState<'create' | 'edit'>('create')
  const [editingNamespace, setEditingNamespace] = useState<KnowledgeNamespaceRow | null>(null)
  const [nsSaving, setNsSaving] = useState(false)

  const loadNamespaces = useCallback(async () => {
    setNamespacesLoading(true)
    try {
      const data = await listKnowledgeNamespaces(namespacePage, namespacePageSize, namespaceStatus || undefined)
      setNamespaceList(data.list)
      setNamespaceTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '知识库加载失败')
    } finally {
      setNamespacesLoading(false)
    }
  }, [namespacePage, namespacePageSize, namespaceStatus])

  const loadNamespaceOptions = useCallback(async () => {
    try {
      const data = await listKnowledgeNamespaces(1, 100, 'active')
      setNamespaceList(data.list)
    } catch {
      // options failure should not block page rendering
    }
  }, [])

  useEffect(() => {
    void loadNamespaces()
  }, [loadNamespaces])

  useEffect(() => {
    void loadNamespaceOptions()
  }, [loadNamespaceOptions])

  const openNamespaceCreate = () => {
    setNsDrawerMode('create')
    setEditingNamespace(null)
    nsForm.setFieldsValue({
      namespace: '',
      name: '',
      description: '',
      embed_model: 'bge',
      vector_dim: 1024,
      status: 'active',
    })
    setNsDrawerOpen(true)
  }

  const openNamespaceEdit = (row: KnowledgeNamespaceRow) => {
    setNsDrawerMode('edit')
    setEditingNamespace(row)
    nsForm.setFieldsValue({
      namespace: row.namespace,
      name: row.name,
      description: row.description || '',
      embed_model: row.embed_model,
      vector_dim: row.vector_dim,
      status: row.status,
    })
    setNsDrawerOpen(true)
  }

  const submitNamespace = async () => {
    setNsSaving(true)
    try {
      const values = await nsForm.validate()
      const body: KnowledgeNamespaceUpsertBody = {
        namespace: String(values.namespace || '').trim(),
        name: String(values.name || '').trim(),
        description: String(values.description || '').trim() || undefined,
        embed_model: String(values.embed_model || '').trim(),
        vector_dim: Number(values.vector_dim) || 0,
        status: String(values.status || 'active'),
      }
      if (nsDrawerMode === 'create') {
        await createKnowledgeNamespace(body)
        Message.success('知识库已创建')
      } else if (editingNamespace) {
        await updateKnowledgeNamespace(editingNamespace.id, body)
        Message.success('知识库已更新')
      }
      setNsDrawerOpen(false)
      await Promise.all([loadNamespaces(), loadNamespaceOptions()])
    } catch (e) {
      if (e instanceof Error && e.message) Message.error(e.message)
    } finally {
      setNsSaving(false)
    }
  }

  const onDeleteNamespace = async (id: string) => {
    try {
      await deleteKnowledgeNamespace(id)
      Message.success('已删除知识库')
      await Promise.all([loadNamespaces(), loadNamespaceOptions()])
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  return (
    <AdminOnly title="知识库">
      <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
        <Title heading={5} className="!mb-1 !mt-0 shrink-0">
          知识库
        </Title>
        <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
          管理知识库 namespace；点击进入后可查看文档、上传入库、进行召回测试。当前页面主要面向后台调试与租户内运营管理。
        </Paragraph>

        <div className="mb-4 flex flex-wrap items-center gap-3">
          <Select
            style={{ width: 180 }}
            options={STATUS_OPTIONS}
            value={namespaceStatus}
            onChange={(v) => {
              setNamespaceStatus(String(v))
              setNamespacePage(1)
            }}
          />
          <Button type="primary" onClick={openNamespaceCreate}>
            新建知识库
          </Button>
          <Button onClick={() => void loadNamespaces()}>刷新</Button>
        </div>

        <Table
          loading={namespacesLoading}
          rowKey="id"
          data={namespaceList}
          pagination={false}
          scroll={{ x: 1100 }}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 120, render: (v: string) => <EllipsisCopyText text={v} maxWidth={96} copiedTip="ID 已复制" /> },
            { title: '命名空间', dataIndex: 'namespace', width: 220, render: (v: string) => <EllipsisCopyText text={v} maxWidth={200} copiedTip="namespace 已复制" /> },
            { title: '名称', dataIndex: 'name', width: 180 },
            { title: '向量模型', dataIndex: 'embed_model', width: 120 },
            { title: '维度', dataIndex: 'vector_dim', width: 90 },
            { title: '状态', dataIndex: 'status', width: 100 },
            { title: '描述', dataIndex: 'description', ellipsis: true, tooltip: true },
            {
              title: '操作',
              width: 220,
              fixed: 'right' as const,
              render: (_: unknown, row: KnowledgeNamespaceRow) => (
                <Space>
                  <Button type="primary" size="mini" onClick={() => navigate(`/knowledge/${row.id}`)}>
                    进入
                  </Button>
                  <Button type="text" size="mini" onClick={() => openNamespaceEdit(row)}>
                    编辑
                  </Button>
                  <Popconfirm title="确定删除这个知识库？（将删除 Qdrant collection）" onOk={() => onDeleteNamespace(row.id)}>
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
            current={namespacePage}
            pageSize={namespacePageSize}
            total={namespaceTotal}
            showTotal
            onChange={(p, ps) => {
              setNamespacePage(p)
              setNamespacePageSize(ps)
            }}
          />
        </div>

        <Drawer
          width={560}
          title={nsDrawerMode === 'create' ? '新建知识库' : '编辑知识库'}
          visible={nsDrawerOpen}
          onCancel={() => setNsDrawerOpen(false)}
          onOk={() => void submitNamespace()}
          confirmLoading={nsSaving}
          unmountOnExit
        >
          <Form form={nsForm} layout="vertical">
            <Form.Item field="namespace" label="Namespace" rules={[{ required: true, message: '请输入 namespace' }]}>
              <Input placeholder="例如 resume_cn" />
            </Form.Item>
            <Form.Item field="name" label="知识库名称" rules={[{ required: true, message: '请输入名称' }]}>
              <Input placeholder="例如 简历知识库" />
            </Form.Item>
            <Form.Item field="description" label="描述">
              <TextArea placeholder="知识库用途描述" autoSize={{ minRows: 3, maxRows: 6 }} />
            </Form.Item>
            <Form.Item field="embed_model" label="向量模型" rules={[{ required: true, message: '请输入向量模型' }]}>
              <Input placeholder="例如 bge / m3e / aliyun" />
            </Form.Item>
            <Form.Item field="vector_dim" label="向量维度" rules={[{ required: true, message: '请输入向量维度' }]}>
              <InputNumber min={1} precision={0} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item field="status" label="状态" rules={[{ required: true, message: '请选择状态' }]}>
              <Select options={STATUS_OPTIONS} />
            </Form.Item>
          </Form>
        </Drawer>
      </div>
    </AdminOnly>
  )
}
