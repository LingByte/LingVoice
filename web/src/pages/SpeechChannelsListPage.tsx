import {
  Alert,
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
  Switch,
  Table,
  Typography,
} from '@arco-design/web-react'
import type { CSSProperties } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  createASRChannel,
  createTTSChannel,
  deleteASRChannel,
  deleteTTSChannel,
  getASRChannel,
  getTTSChannel,
  listASRChannels,
  listTTSChannels,
  updateASRChannel,
  updateTTSChannel,
  type SpeechChannelRow,
  type SpeechChannelUpsert,
  type SpeechKind,
} from '@/api/channelsAdmin'
import {
  normalizeASRProviderId,
  normalizeTTSProviderId,
  speechProvidersForKind,
  type SpeechConfigField,
} from '@/config/speechProviders'

const { Title, Paragraph } = Typography
const FormItem = Form.Item

const drawerBodyStyle: CSSProperties = { padding: '12px 16px 8px' }

function emptyConfigForm(): Record<string, string | number | undefined> {
  return {}
}

function configFromParsed(raw: unknown): Record<string, string | number | undefined> {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return emptyConfigForm()
  const out: Record<string, string | number | undefined> = {}
  for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
    if (v === null || v === undefined) continue
    if (typeof v === 'number') out[k] = v
    else out[k] = String(v)
  }
  return out
}

function buildConfigJsonFromForm(
  fields: SpeechConfigField[],
  cfg: Record<string, string | number | undefined> | undefined,
): { ok: true; json: string } | { ok: false; message: string } {
  const obj: Record<string, unknown> = {}
  const src = cfg ?? {}
  for (const f of fields) {
    const raw = src[f.key]
    const empty = raw === undefined || raw === '' || (typeof raw === 'number' && Number.isNaN(raw))
    if (empty) {
      if (f.required) {
        return { ok: false, message: `请填写「${f.label}」` }
      }
      continue
    }
    if (f.type === 'number') {
      const n = typeof raw === 'number' ? raw : Number(String(raw).trim())
      if (Number.isNaN(n)) {
        return { ok: false, message: `「${f.label}」须为数字` }
      }
      obj[f.key] = n
    } else {
      obj[f.key] = typeof raw === 'string' ? raw.trim() : raw
    }
  }
  return { ok: true, json: JSON.stringify(obj) }
}

export function SpeechChannelsListPage(props: {
  kind: SpeechKind
  title: string
  description: string
}) {
  const { kind, title, description } = props
  const [form] = Form.useForm<{
    provider: string
    name: string
    enabled: boolean
    group: string
    sortOrder: number
    config: Record<string, string | number | undefined>
  }>()

  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<SpeechChannelRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)
  const [draftGroup, setDraftGroup] = useState('')
  const [draftProvider, setDraftProvider] = useState('')
  const [appliedGroup, setAppliedGroup] = useState('')
  const [appliedProvider, setAppliedProvider] = useState('')

  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerMode, setDrawerMode] = useState<'create' | 'edit'>('create')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [drawerLoading, setDrawerLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  const providers = useMemo(() => speechProvidersForKind(kind), [kind])

  const label = kind === 'asr' ? 'ASR' : 'TTS'

  const watchedProvider = Form.useWatch('provider', form) as string | undefined

  const activeMeta = useMemo(() => {
    const id = String(watchedProvider || '').trim()
    if (!id) return undefined
    return providers.find((p) => p.id === id)
  }, [providers, watchedProvider])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const filters = {
        ...(appliedGroup ? { group: appliedGroup } : {}),
        ...(appliedProvider ? { provider: appliedProvider } : {}),
      }
      const data =
        kind === 'asr'
          ? await listASRChannels(page, pageSize, filters)
          : await listTTSChannels(page, pageSize, filters)
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [appliedGroup, appliedProvider, kind, page, pageSize])

  useEffect(() => {
    void load()
  }, [load])

  const openCreate = () => {
    setDrawerMode('create')
    setEditingId(null)
    form.setFieldsValue({
      provider: providers[0]?.id ?? '',
      name: '',
      enabled: true,
      group: '',
      sortOrder: 0,
      config: emptyConfigForm(),
    })
    setDrawerOpen(true)
  }

  const openEdit = async (id: string) => {
    setDrawerMode('edit')
    setEditingId(id)
    setDrawerOpen(true)
    setDrawerLoading(true)
    try {
      const { channel } = kind === 'asr' ? await getASRChannel(id) : await getTTSChannel(id)
      let cfg: Record<string, string | number | undefined> = emptyConfigForm()
      const raw = channel.configJson?.trim()
      if (raw) {
        try {
          cfg = configFromParsed(JSON.parse(raw) as unknown)
        } catch {
          Message.warning('configJson 解析失败，已清空表单中的厂商参数')
        }
      }
      form.setFieldsValue({
        provider: channel.provider,
        name: channel.name,
        enabled: channel.enabled,
        group: channel.group || '',
        sortOrder: channel.sortOrder ?? 0,
        config: cfg,
      })
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
      setDrawerOpen(false)
    } finally {
      setDrawerLoading(false)
    }
  }

  const submitDrawer = async () => {
    if (providers.length === 0) {
      Message.warning('厂商列表为空')
      return
    }
    setSaving(true)
    try {
      const v = await form.validate()
      const pid = String(v.provider || '').trim()
      const meta = providers.find((p) => p.id === pid)
      if (!meta) {
        Message.error('请选择有效厂商')
        return
      }
      const built = buildConfigJsonFromForm(meta.configFields, v.config)
      if (built.ok === false) {
        Message.error(built.message)
        return
      }
      const configJson = built.json
      const providerNorm =
        kind === 'asr' ? normalizeASRProviderId(pid) : normalizeTTSProviderId(pid)
      const body: SpeechChannelUpsert = {
        provider: providerNorm,
        name: String(v.name || '').trim(),
        enabled: Boolean(v.enabled),
        group: String(v.group || '').trim(),
        sortOrder: Number(v.sortOrder) || 0,
        configJson,
      }
      if (drawerMode === 'create') {
        if (kind === 'asr') await createASRChannel(body)
        else await createTTSChannel(body)
        Message.success('已创建')
      } else if (editingId != null) {
        if (kind === 'asr') await updateASRChannel(editingId, body)
        else await updateTTSChannel(editingId, body)
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

  const onDelete = async (id: string) => {
    try {
      if (kind === 'asr') await deleteASRChannel(id)
      else await deleteTTSChannel(id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  const providerSelectOptions = useMemo(
    () =>
      providers.map((p) => ({
        value: p.id,
        label: `${p.label}（${p.id}）`,
      })),
    [providers],
  )

  const renderConfigField = (f: SpeechConfigField) => {
    const extra = f.hint ? <span className="text-[12px] text-[var(--color-text-3)]">{f.hint}</span> : undefined
    const rules = f.required ? [{ required: true, message: `请填写${f.label}` }] : undefined
    const field = `config.${f.key}` as const
    if (f.type === 'password') {
      return (
        <FormItem key={f.key} label={f.label} field={field} rules={rules} extra={extra}>
          <Input.Password autoComplete="new-password" placeholder={f.placeholder} />
        </FormItem>
      )
    }
    if (f.type === 'number') {
      return (
        <FormItem key={f.key} label={f.label} field={field} rules={rules} extra={extra}>
          <InputNumber min={0} style={{ width: '100%' }} placeholder={f.placeholder} />
        </FormItem>
      )
    }
    if (f.type === 'textarea') {
      return (
        <FormItem key={f.key} label={f.label} field={field} rules={rules} extra={extra}>
          <Input.TextArea autoSize={{ minRows: 3, maxRows: 8 }} placeholder={f.placeholder} />
        </FormItem>
      )
    }
    return (
      <FormItem key={f.key} label={f.label} field={field} rules={rules} extra={extra}>
        <Input placeholder={f.placeholder} />
      </FormItem>
    )
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        {title}
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
        {description} 新建与编辑在右侧抽屉完成；厂商与参数字段由前端配置维护（与后端 recognizer / synthesizer 的 provider 命名对齐）。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Input
          allowClear
          placeholder="筛选 group"
          value={draftGroup}
          onChange={setDraftGroup}
          style={{ width: 160 }}
        />
        <Input
          allowClear
          placeholder="筛选 provider"
          value={draftProvider}
          onChange={setDraftProvider}
          style={{ width: 180 }}
        />
        <Button
          type="primary"
          onClick={() => {
            setAppliedGroup(draftGroup.trim())
            setAppliedProvider(draftProvider.trim())
            setPage(1)
          }}
        >
          查询
        </Button>
        <Button type="primary" onClick={openCreate} disabled={providers.length === 0}>
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
        scroll={{ x: 900 }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 72 },
          { title: '厂商', dataIndex: 'provider', width: 140, ellipsis: true },
          { title: '名称', dataIndex: 'name', width: 180, ellipsis: true },
          { title: '分组', dataIndex: 'group', width: 120, ellipsis: true },
          { title: '排序', dataIndex: 'sortOrder', width: 72 },
          {
            title: '启用',
            dataIndex: 'enabled',
            width: 72,
            render: (v: boolean) => (v ? '是' : '否'),
          },
          { title: '更新', dataIndex: 'updateAt', width: 168, ellipsis: true },
          {
            title: '操作',
            width: 140,
            fixed: 'right' as const,
            render: (_: unknown, row: SpeechChannelRow) => (
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
            {drawerMode === 'create' ? `新建 ${label} 渠道` : `编辑 ${label} 渠道 #${editingId ?? ''}`}
          </span>
        }
        visible={drawerOpen}
        width={480}
        placement="right"
        bodyStyle={drawerBodyStyle}
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
          <FormItem label="厂商" field="provider" rules={[{ required: true, message: '请选择厂商' }]}>
            <Select
              placeholder="选择厂商"
              options={providerSelectOptions}
              showSearch
              filterOption={(input, option) => {
                const opt = option as { value?: string; label?: string }
                const q = input.toLowerCase()
                return (
                  String(opt.value ?? '')
                    .toLowerCase()
                    .includes(q) || String(opt.label ?? '').toLowerCase().includes(q)
                )
              }}
              onChange={() => {
                form.setFieldValue('config', emptyConfigForm())
              }}
            />
          </FormItem>
          {activeMeta?.description ? (
            <Alert type="info" className="!mb-3" content={activeMeta.description} />
          ) : null}
          {activeMeta?.notes ? (
            <Alert type="warning" className="!mb-3" content={activeMeta.notes} />
          ) : null}
          <FormItem label="名称" field="name" rules={[{ required: true, message: '必填' }]}>
            <Input />
          </FormItem>
          <FormItem label="启用" field="enabled" triggerPropName="checked">
            <Switch />
          </FormItem>
          <FormItem label="分组 group" field="group">
            <Input placeholder="可选" />
          </FormItem>
          <FormItem label="排序 sortOrder" field="sortOrder" extra="默认 0，同组内越小越靠前">
            <InputNumber min={0} style={{ width: '100%' }} />
          </FormItem>
          <Paragraph className="!mb-2 !mt-1 !text-[12px] text-[var(--color-text-3)]">
            以下为写入 configJson 的厂商参数（合法 JSON 对象）；音色等随请求变化的参数见 SPEC，不在此配置。
          </Paragraph>
          {activeMeta ? activeMeta.configFields.map(renderConfigField) : null}
        </Form>
      </Drawer>
    </div>
  )
}
