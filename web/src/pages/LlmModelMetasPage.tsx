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
import type { CSSProperties } from 'react'
import { useCallback, useEffect, useState } from 'react'
import {
  createLLMModelMeta,
  deleteLLMModelMeta,
  listLLMModelMetas,
  updateLLMModelMeta,
  type LLMModelMetaRow,
} from '@/api/llmModelMetas'
import { AdminOnly } from '@/components/AdminOnly'
import { VENDOR_PRESET_OPTIONS } from '@/utils/modelVendorIcon'

const { Title, Paragraph } = Typography
const FormItem = Form.Item

const drawerBodyStyle: CSSProperties = { padding: '12px 16px 8px' }

export function LlmModelMetasPage() {
  const [form] = Form.useForm<Record<string, unknown>>()
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<LLMModelMetaRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)
  const [draftQ, setDraftQ] = useState('')
  const [appliedQ, setAppliedQ] = useState('')

  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerMode, setDrawerMode] = useState<'create' | 'edit'>('create')
  const [editingId, setEditingId] = useState<number | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listLLMModelMetas(page, pageSize, {
        q: appliedQ || undefined,
      })
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [page, pageSize, appliedQ])

  useEffect(() => {
    void load()
  }, [load])

  const openCreate = () => {
    setDrawerMode('create')
    setEditingId(null)
    form.resetFields()
    form.setFieldsValue({
      model_name: '',
      description: '',
      tags: '',
      status: 1,
      icon_url: '',
      vendor: '',
      sort_order: 0,
      context_length: undefined as number | undefined,
      max_output_tokens: undefined as number | undefined,
      quota_billing_mode: 'token',
      quota_model_ratio: 1,
      quota_prompt_ratio: 1,
      quota_completion_ratio: 1,
      quota_cache_read_ratio: 0.25,
    })
    setDrawerOpen(true)
  }

  const openEdit = (row: LLMModelMetaRow) => {
    setDrawerMode('edit')
    setEditingId(row.id)
    form.setFieldsValue({
      model_name: row.model_name,
      description: row.description ?? '',
      tags: row.tags ?? '',
      status: row.status,
      icon_url: row.icon_url ?? '',
      vendor: row.vendor ?? '',
      sort_order: row.sort_order ?? 0,
      context_length: row.context_length ?? undefined,
      max_output_tokens: row.max_output_tokens ?? undefined,
      quota_billing_mode: (row.quota_billing_mode || 'token').replace('count', 'times'),
      quota_model_ratio: row.quota_model_ratio ?? 1,
      quota_prompt_ratio: row.quota_prompt_ratio ?? 1,
      quota_completion_ratio: row.quota_completion_ratio ?? 1,
      quota_cache_read_ratio: row.quota_cache_read_ratio ?? 0.25,
    })
    setDrawerOpen(true)
  }

  const submitDrawer = async () => {
    setSaving(true)
    try {
      const v = (await form.validate()) as Record<string, unknown>
      const ctx = v.context_length
      const maxo = v.max_output_tokens
      const modeRaw = String(v.quota_billing_mode || 'token').toLowerCase()
      const mode = modeRaw === 'tokens' ? 'token' : modeRaw === 'count' ? 'times' : modeRaw
      const body = {
        model_name: String(v.model_name || '').trim(),
        description: String(v.description || '').trim(),
        tags: String(v.tags || '').trim(),
        status: Number(v.status) ?? 1,
        icon_url: String(v.icon_url || '').trim(),
        vendor: String(v.vendor || '').trim(),
        sort_order: Number(v.sort_order) || 0,
        context_length: typeof ctx === 'number' && Number.isFinite(ctx) ? ctx : null,
        max_output_tokens: typeof maxo === 'number' && Number.isFinite(maxo) ? maxo : null,
        quota_billing_mode: mode === 'token' || mode === 'times' ? mode : 'times',
        quota_model_ratio: Number(v.quota_model_ratio) || 1,
        quota_prompt_ratio: Number(v.quota_prompt_ratio) || 1,
        quota_completion_ratio: Number(v.quota_completion_ratio) || 1,
        quota_cache_read_ratio:
          typeof v.quota_cache_read_ratio === 'number' && Number.isFinite(v.quota_cache_read_ratio)
            ? Math.max(0, v.quota_cache_read_ratio)
            : 0.25,
      }
      if (!body.model_name) {
        Message.error('模型名必填')
        return
      }
      if (drawerMode === 'create') {
        await createLLMModelMeta(body)
        Message.success('已创建')
      } else if (editingId != null) {
        await updateLLMModelMeta(editingId, body)
        Message.success('已保存')
      }
      setDrawerOpen(false)
      void load()
    } catch (e) {
      if (e instanceof Error && e.message) Message.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  const onDelete = async (id: number) => {
    try {
      await deleteLLMModelMeta(id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  return (
    <AdminOnly title="模型元数据">
      <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
        <Title heading={5} className="!mb-1 !mt-0 shrink-0">
          模型元数据
        </Title>
        <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
          模型目录、厂商、图标与上下文规模说明；与能力表通过 model 名及可选 model_meta_id 关联。图标留空时模型广场会按 vendor
          / 模型名推断（Simple Icons CDN，对齐 new-api 常用展示方式）。下方可配置「额度计费」字段，用于 OpenAPI 按次/按 token
          及缓存折算扣减 remain_quota（与 new-api 模型倍率思路一致）。
        </Paragraph>

        <div className="mb-4 flex flex-wrap items-center gap-3">
          <Input
            allowClear
            placeholder="搜索 model_name / 描述 / tags"
            value={draftQ}
            onChange={setDraftQ}
            style={{ width: 280 }}
          />
          <Button
            type="primary"
            onClick={() => {
              setAppliedQ(draftQ.trim())
              setPage(1)
            }}
          >
            查询
          </Button>
          <Button type="primary" onClick={openCreate}>
            新建
          </Button>
          <Button onClick={() => void load()} loading={loading}>
            刷新
          </Button>
        </div>

        <Table
          loading={loading}
          rowKey="id"
          data={list}
          pagination={false}
          scroll={{ x: 1180 }}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 64 },
            { title: '排序', dataIndex: 'sort_order', width: 56 },
            { title: '模型名', dataIndex: 'model_name', width: 200, ellipsis: true },
            { title: '厂商', dataIndex: 'vendor', width: 100, ellipsis: true, render: (v: string) => v || '—' },
            { title: '描述', dataIndex: 'description', ellipsis: true, render: (d: string) => d || '—' },
            { title: 'Tags', dataIndex: 'tags', width: 120, ellipsis: true, render: (t: string) => t || '—' },
            {
              title: 'Ctx/Out',
              width: 100,
              render: (_: unknown, row: LLMModelMetaRow) =>
                `${row.context_length ?? '—'} / ${row.max_output_tokens ?? '—'}`,
            },
            {
              title: '计费',
              width: 120,
              ellipsis: true,
              render: (_: unknown, row: LLMModelMetaRow) => {
                const m = (row.quota_billing_mode || 'token').replace('count', 'times')
                const r = row.quota_model_ratio ?? 1
                return `${m} ×${r}`
              },
            },
            { title: '状态', dataIndex: 'status', width: 64 },
            {
              title: '操作',
              width: 140,
              fixed: 'right' as const,
              render: (_: unknown, row: LLMModelMetaRow) => (
                <Space>
                  <Button type="text" size="mini" onClick={() => openEdit(row)}>
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
            sizeCanChange
            showTotal
            pageSizeChangeResetCurrent
            onChange={(p, ps) => {
              setPage(p)
              setPageSize(ps)
            }}
          />
        </div>

        <Drawer
          width={480}
          title={drawerMode === 'create' ? '新建模型元数据' : '编辑模型元数据'}
          visible={drawerOpen}
          onCancel={() => setDrawerOpen(false)}
          unmountOnExit
          bodyStyle={drawerBodyStyle}
          footer={
            <Space>
              <Button onClick={() => setDrawerOpen(false)}>取消</Button>
              <Button type="primary" loading={saving} onClick={() => void submitDrawer()}>
                保存
              </Button>
            </Space>
          }
        >
          <Form form={form} layout="vertical">
            <FormItem label="模型名" field="model_name" rules={[{ required: true, message: '必填' }]}>
              <Input placeholder="与上游 model 字符串一致" />
            </FormItem>
            <FormItem label="厂商 vendor" field="vendor">
              <Select allowClear placeholder="推断图标用" options={[...VENDOR_PRESET_OPTIONS]} />
            </FormItem>
            <FormItem label="图标 URL（可选，覆盖推断）" field="icon_url">
              <Input allowClear placeholder="https://…" />
            </FormItem>
            <FormItem label="排序 sort_order" field="sort_order">
              <InputNumber style={{ width: '100%' }} />
            </FormItem>
            <FormItem label="上下文长度 context_length" field="context_length">
              <InputNumber min={0} style={{ width: '100%' }} placeholder="可选" />
            </FormItem>
            <FormItem label="最大输出 max_output_tokens" field="max_output_tokens">
              <InputNumber min={0} style={{ width: '100%' }} placeholder="可选" />
            </FormItem>
            <FormItem label="描述" field="description">
              <Input.TextArea rows={4} placeholder="可选" />
            </FormItem>
            <FormItem label="Tags" field="tags">
              <Input placeholder="逗号分隔或自由文本" />
            </FormItem>
            <FormItem label="状态（1 启用 0 停用）" field="status">
              <InputNumber min={0} max={1} style={{ width: '100%' }} />
            </FormItem>
            <FormItem
              label="额度计费模式"
              field="quota_billing_mode"
              extra="默认 token：按折算 token 扣额度；仅当显式 times 时按次（ceil 模型倍率）。"
            >
              <Select
                options={[
                  { label: '按 token（默认）', value: 'token' },
                  { label: '按次 times', value: 'times' },
                ]}
              />
            </FormItem>
            <FormItem label="模型倍率 quota_model_ratio" field="quota_model_ratio">
              <InputNumber min={0.01} step={0.01} style={{ width: '100%' }} />
            </FormItem>
            <FormItem label="输入 token 权重 quota_prompt_ratio" field="quota_prompt_ratio">
              <InputNumber min={0.01} step={0.01} style={{ width: '100%' }} />
            </FormItem>
            <FormItem label="输出 token 权重 quota_completion_ratio" field="quota_completion_ratio">
              <InputNumber min={0.01} step={0.01} style={{ width: '100%' }} />
            </FormItem>
            <FormItem
              label="缓存读折算 quota_cache_read_ratio"
              field="quota_cache_read_ratio"
              extra="相对非缓存 prompt 的折算系数，0~1，常用 0.25"
            >
              <InputNumber min={0} max={1} step={0.05} style={{ width: '100%' }} />
            </FormItem>
          </Form>
        </Drawer>
      </div>
    </AdminOnly>
  )
}
