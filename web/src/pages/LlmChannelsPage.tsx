import {
  Button,
  Drawer,
  Form,
  Grid,
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
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  createLLMChannel,
  deleteLLMChannel,
  getLLMChannel,
  listLLMChannels,
  updateLLMChannel,
  type LLMChannelInfo,
  type LLMChannelRow,
  type LLMChannelUpsert,
  LLM_CHANNEL_PROTOCOL_OPTIONS,
} from '@/api/channelsAdmin'
import { AdminOnly } from '@/components/AdminOnly'

const { Title, Paragraph, Text } = Typography
const FormItem = Form.Item
const Row = Grid.Row
const Col = Grid.Col

const defaultChannelInfo = (): LLMChannelInfo => ({
  is_multi_key: false,
  multi_key_size: 0,
  multi_key_status_list: {},
  multi_key_disabled_reason: {},
  multi_key_disabled_time: {},
  multi_key_polling_index: 0,
  multi_key_mode: 'random',
})

const drawerBodyStyle: CSSProperties = { padding: '12px 16px 8px' }

function resetLlmForm(
  form: { setFieldsValue: (values: Record<string, unknown>) => void },
  mode: 'create' | 'edit',
  channel?: LLMChannelRow,
) {
  if (mode === 'create' || !channel) {
    form.setFieldsValue({
      protocol: 'openai',
      key: '',
      name: '',
      type: 0,
      status: 1,
      group: 'default',
      models: '',
      base_url: '',
      test_model: '',
      openai_organization: '',
      model_mapping: '',
      status_code_mapping: '',
      priority: 0,
      weight: 1,
      auto_ban: 1,
      tag: '',
      channel_info_json: JSON.stringify(defaultChannelInfo(), null, 2),
    })
    return
  }
  form.setFieldsValue({
    protocol: channel.protocol || 'openai',
    key: channel.key,
    name: channel.name,
    type: channel.type,
    status: channel.status,
    group: channel.group || 'default',
    models: channel.models || '',
    base_url: channel.base_url || '',
    test_model: channel.test_model || '',
    openai_organization: channel.openai_organization || '',
    model_mapping: channel.model_mapping || '',
    status_code_mapping: channel.status_code_mapping || '',
    priority: channel.priority ?? 0,
    weight: channel.weight ?? 1,
    auto_ban: channel.auto_ban ?? 1,
    tag: channel.tag || '',
    channel_info_json: JSON.stringify(
      channel.channel_info && typeof channel.channel_info === 'object'
        ? channel.channel_info
        : defaultChannelInfo(),
      null,
      2,
    ),
  })
}

export function LlmChannelsPage() {
  const [form] = Form.useForm<Record<string, unknown>>()
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<LLMChannelRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)
  const [draftGroup, setDraftGroup] = useState('')
  const [appliedGroup, setAppliedGroup] = useState('')

  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerMode, setDrawerMode] = useState<'create' | 'edit'>('create')
  const [editingId, setEditingId] = useState<number | null>(null)
  const [drawerLoading, setDrawerLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listLLMChannels(page, pageSize, appliedGroup || undefined)
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [page, pageSize, appliedGroup])

  useEffect(() => {
    void load()
  }, [load])

  const openCreate = () => {
    setDrawerMode('create')
    setEditingId(null)
    resetLlmForm(form, 'create')
    setDrawerOpen(true)
  }

  const openEdit = async (id: number) => {
    setDrawerMode('edit')
    setEditingId(id)
    setDrawerOpen(true)
    setDrawerLoading(true)
    try {
      const { channel } = await getLLMChannel(id)
      resetLlmForm(form, 'edit', channel)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
      setDrawerOpen(false)
    } finally {
      setDrawerLoading(false)
    }
  }

  const submitDrawer = async () => {
    setSaving(true)
    try {
      const v = (await form.validate()) as Record<string, unknown>
      let channel_info: LLMChannelInfo
      try {
        channel_info = JSON.parse(String(v.channel_info_json || '{}')) as LLMChannelInfo
      } catch {
        Message.error('channel_info JSON 无效')
        return
      }
      const body: LLMChannelUpsert = {
        protocol: String(v.protocol || 'openai'),
        key: String(v.key || '').trim(),
        name: String(v.name || '').trim(),
        type: Number(v.type) || 0,
        status: Number(v.status) ?? 1,
        group: String(v.group || '').trim() || 'default',
        models: String(v.models || ''),
        base_url: String(v.base_url || '').trim() || undefined,
        test_model: String(v.test_model || '').trim() || undefined,
        openai_organization: String(v.openai_organization || '').trim() || undefined,
        model_mapping: String(v.model_mapping || '').trim() || undefined,
        status_code_mapping: String(v.status_code_mapping || '').trim() || undefined,
        priority: Number(v.priority) || 0,
        weight: Math.max(1, Number(v.weight) || 1),
        auto_ban: Number(v.auto_ban) ?? 1,
        tag: String(v.tag || '').trim() || undefined,
        channel_info,
      }
      if (drawerMode === 'create') {
        if (!body.key) {
          Message.error('新建时 API Key 必填')
          return
        }
        await createLLMChannel(body)
        Message.success('已创建')
      } else if (editingId != null) {
        await updateLLMChannel(editingId, body)
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
      await deleteLLMChannel(id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  const protocolLabels = useMemo(
    () =>
      Object({
        openai: 'OpenAI',
        anthropic: 'Anthropic',
        coze: 'Coze',
        ollama: 'Ollama',
        lmstudio: 'LM Studio',
      }),
    [],
  )

  return (
    <AdminOnly title="LLM 渠道">
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        LLM 渠道
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
        大模型上游配置；新建与编辑均在右侧抽屉完成。Priority 默认 0（同组内越小越优先常见约定），Weight 默认 1（加权随机时的权重）。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Input
          allowClear
          placeholder="筛选 group"
          value={draftGroup}
          onChange={setDraftGroup}
          style={{ width: 200 }}
        />
        <Button
          type="primary"
          onClick={() => {
            setAppliedGroup(draftGroup.trim())
            setPage(1)
          }}
        >
          查询
        </Button>
        <Button type="primary" onClick={openCreate}>
          新建渠道
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
          {
            title: '协议',
            dataIndex: 'protocol',
            width: 100,
            render: (p: string) => protocolLabels[p as keyof typeof protocolLabels] || p || '—',
          },
          { title: '名称', dataIndex: 'name', width: 140, ellipsis: true },
          { title: '分组', dataIndex: 'group', width: 100, ellipsis: true },
          { title: '状态', dataIndex: 'status', width: 64 },
          {
            title: 'P/W',
            width: 72,
            render: (_: unknown, row: LLMChannelRow) => `${row.priority ?? 0}/${row.weight ?? 1}`,
          },
          {
            title: 'Base URL',
            dataIndex: 'base_url',
            width: 200,
            ellipsis: true,
            render: (v: string | null | undefined) => v || '—',
          },
          { title: '模型', dataIndex: 'models', width: 160, ellipsis: true },
          {
            title: '操作',
            width: 140,
            fixed: 'right' as const,
            render: (_: unknown, row: LLMChannelRow) => (
              <Space>
                <Button type="text" size="mini" onClick={() => void openEdit(row.id)}>
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

      <Drawer
        title={
          <span className="text-[15px] font-semibold text-[var(--color-text-1)]">
            {drawerMode === 'create' ? '新建 LLM 渠道' : `编辑 LLM 渠道 #${editingId ?? ''}`}
          </span>
        }
        visible={drawerOpen}
        width={440}
        placement="right"
        bodyStyle={drawerBodyStyle}
        unmountOnExit={false}
        onCancel={() => setDrawerOpen(false)}
        footer={
          <div className="flex justify-end gap-2 border-t border-[var(--color-border-2)] px-3 py-2.5">
            <Button size="small" onClick={() => setDrawerOpen(false)}>
              取消
            </Button>
            <Button size="small" type="primary" loading={saving} onClick={() => void submitDrawer()}>
              {drawerMode === 'create' ? '创建' : '保存'}
            </Button>
          </div>
        }
        className="credential-drawer"
      >
        <Form form={form} layout="vertical" size="small" disabled={drawerLoading} className="credential-drawer__form">
          <FormItem label="渠道协议" field="protocol" rules={[{ required: true }]}>
            <Select
              options={LLM_CHANNEL_PROTOCOL_OPTIONS.map((v) => ({
                value: v,
                label: `${protocolLabels[v as keyof typeof protocolLabels] || v} (${v})`,
              }))}
            />
          </FormItem>
          <FormItem label="名称" field="name" rules={[{ required: true, message: '必填' }]}>
            <Input placeholder="展示名称" />
          </FormItem>
          <FormItem
            label="API Key"
            field="key"
            rules={drawerMode === 'create' ? [{ required: true, message: '新建必填' }] : undefined}
            extra={drawerMode === 'edit' ? '留空则保留原 Key' : undefined}
          >
            <Input.Password placeholder="sk-..." autoComplete="new-password" />
          </FormItem>
          <Row gutter={10}>
            <Col span={12}>
              <FormItem label="Priority" field="priority" extra="同组/多渠道路由时排序权重，常用默认 0">
                <InputNumber min={0} style={{ width: '100%' }} />
              </FormItem>
            </Col>
            <Col span={12}>
              <FormItem label="Weight" field="weight" extra="随机/加权选择时的权重，默认 1；填 0 会按 1 保存">
                <InputNumber min={0} style={{ width: '100%' }} />
              </FormItem>
            </Col>
          </Row>
          <Row gutter={10}>
            <Col span={12}>
              <FormItem label="类型 type" field="type">
                <InputNumber min={0} style={{ width: '100%' }} />
              </FormItem>
            </Col>
            <Col span={12}>
              <FormItem label="状态 status" field="status">
                <InputNumber min={0} style={{ width: '100%' }} />
              </FormItem>
            </Col>
          </Row>
          <FormItem label="分组 group" field="group">
            <Input />
          </FormItem>
          <FormItem label="模型列表 models" field="models" extra="逗号分隔或网关约定">
            <Input.TextArea autoSize={{ minRows: 2, maxRows: 5 }} />
          </FormItem>
          <FormItem
            label="Base URL"
            field="base_url"
            extra="上游 API 根地址；OpenAI 官方多为 https://api.openai.com/v1，Ollama 多为 http://127.0.0.1:11434/v1"
          >
            <Input />
          </FormItem>
          <FormItem label="Test model" field="test_model">
            <Input />
          </FormItem>
          <FormItem
            label="OpenAI Organization"
            field="openai_organization"
            extra="仅 OpenAI 官方云：组织 ID（org-…），用于绑定计费/项目；其它协议可留空"
          >
            <Input placeholder="org-..." />
          </FormItem>
          <FormItem
            label="Model mapping（JSON）"
            field="model_mapping"
            extra="将客户端请求的模型名映射到上游真实模型 ID；空表示不映射、同名透传"
          >
            <Input.TextArea className="font-mono text-[12px]" autoSize={{ minRows: 2, maxRows: 5 }} />
          </FormItem>
          <FormItem
            label="Status code mapping"
            field="status_code_mapping"
            extra="描述上游 HTTP/业务错误码到重试、熔断等策略的映射（多为 JSON 或内部约定串）；可留空"
          >
            <Input />
          </FormItem>
          <FormItem label="Auto ban" field="auto_ban">
            <InputNumber min={0} style={{ width: '100%' }} />
          </FormItem>
          <FormItem label="Tag" field="tag">
            <Input placeholder="可选标签" />
          </FormItem>
          <FormItem
            label="channel_info（多 Key）"
            field="channel_info_json"
            rules={[{ required: true }]}
            extra={
              <span className="text-[12px] leading-snug text-[var(--color-text-3)]">
                多 API Key 时的池配置：<Text code>is_multi_key</Text>、<Text code>multi_key_mode</Text>（random / polling）、各
                key 的启用/禁用原因等；单 Key 时保持默认 JSON 即可。
              </span>
            }
          >
            <Input.TextArea className="font-mono text-[12px]" autoSize={{ minRows: 8, maxRows: 18 }} />
          </FormItem>
        </Form>
      </Drawer>
    </div>
    </AdminOnly>
  )
}
