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
  Switch,
  Table,
  Typography,
} from '@arco-design/web-react'
import type { CSSProperties } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { getLLMAdminFormOptions, type LLMAdminFormOptions } from '@/api/llmAdmin'
import {
  createLLMAbility,
  deleteLLMAbility,
  listLLMAbilities,
  patchLLMAbility,
  syncLLMAbilitiesFromChannel,
  type LLMAbilityRow,
} from '@/api/llmAbilities'
import { AdminOnly } from '@/components/AdminOnly'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'

const { Title, Paragraph } = Typography
const FormItem = Form.Item

const drawerBodyStyle: CSSProperties = { padding: '12px 16px 8px' }

const META_P = '__m:'
const NAME_P = '__n:'

function encMeta(id: number) {
  return `${META_P}${id}`
}
function encName(name: string) {
  return `${NAME_P}${encodeURIComponent(name)}`
}
function parseModelPick(v: string): { modelMetaId?: number; modelName?: string } {
  if (v.startsWith(META_P)) {
    const id = Number(v.slice(META_P.length))
    return Number.isFinite(id) && id > 0 ? { modelMetaId: id } : {}
  }
  if (v.startsWith(NAME_P)) {
    try {
      const modelName = decodeURIComponent(v.slice(NAME_P.length))
      return modelName ? { modelName } : {}
    } catch {
      return {}
    }
  }
  return {}
}

function rowKey(r: LLMAbilityRow) {
  return `${r.group}\t${r.model}\t${r.channel_id}`
}

export function LlmAbilitiesPage() {
  const [form] = Form.useForm<Record<string, unknown>>()
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<LLMAbilityRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(15)

  const [draftGroup, setDraftGroup] = useState('')
  const [draftModel, setDraftModel] = useState('')
  const [draftChannelFilter, setDraftChannelFilter] = useState<number | undefined>(undefined)
  const [appliedGroup, setAppliedGroup] = useState('')
  const [appliedModel, setAppliedModel] = useState('')
  const [appliedChannelId, setAppliedChannelId] = useState<number | undefined>(undefined)

  const [drawerOpen, setDrawerOpen] = useState(false)
  const [drawerMode, setDrawerMode] = useState<'create' | 'edit'>('create')
  const [editingRow, setEditingRow] = useState<LLMAbilityRow | null>(null)
  const [saving, setSaving] = useState(false)

  const [formOpts, setFormOpts] = useState<LLMAdminFormOptions | null>(null)
  const [optsLoading, setOptsLoading] = useState(false)
  const [syncChannelPick, setSyncChannelPick] = useState<number | undefined>(undefined)
  const [syncing, setSyncing] = useState(false)

  const loadFormOpts = useCallback(async () => {
    setOptsLoading(true)
    try {
      const data = await getLLMAdminFormOptions()
      setFormOpts(data)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载表单选项失败')
    } finally {
      setOptsLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadFormOpts()
  }, [loadFormOpts])

  const modelSelectOptions = useMemo(() => {
    if (!formOpts) return []
    const metaNames = new Set(formOpts.model_metas.map((m) => m.model_name))
    const metaOpts = formOpts.model_metas.map((m) => ({
      label: `★ ${m.model_name}${m.vendor ? ` · ${m.vendor}` : ''}`,
      value: encMeta(m.id),
    }))
    const raw = formOpts.model_name_suggestions
      .filter((n) => !metaNames.has(n))
      .map((n) => ({
        label: `◇ ${n}（仅渠道配置，未建元数据）`,
        value: encName(n),
      }))
    return [...metaOpts, ...raw]
  }, [formOpts])

  const channelSelectOptions = useMemo(() => {
    if (!formOpts) return []
    return formOpts.channels.map((ch) => ({
      label: `#${ch.id} ${ch.name} · ${ch.group} · ${ch.protocol}`,
      value: ch.id,
    }))
  }, [formOpts])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listLLMAbilities(page, pageSize, {
        group: appliedGroup || undefined,
        model: appliedModel || undefined,
        channel_id: appliedChannelId,
      })
      setList(data.list)
      setTotal(data.total)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [page, pageSize, appliedGroup, appliedModel, appliedChannelId])

  useEffect(() => {
    void load()
  }, [load])

  const openCreate = () => {
    setDrawerMode('create')
    setEditingRow(null)
    form.resetFields()
    form.setFieldsValue({
      group: appliedGroup || 'default',
      model_pick: undefined,
      channel_pick: undefined,
      enabled: true,
      priority: 0,
      weight: 1,
      tag: '',
    })
    setDrawerOpen(true)
    void loadFormOpts()
  }

  const openEdit = (row: LLMAbilityRow) => {
    setDrawerMode('edit')
    setEditingRow(row)
    form.setFieldsValue({
      group: row.group,
      model_pick:
        row.model_meta_id != null && row.model_meta_id > 0
          ? encMeta(row.model_meta_id)
          : encName(row.model),
      channel_pick: row.channel_id,
      enabled: row.enabled,
      priority: row.priority,
      weight: row.weight,
      tag: row.tag ?? '',
    })
    setDrawerOpen(true)
    void loadFormOpts()
  }

  const submitDrawer = async () => {
    setSaving(true)
    try {
      const v = (await form.validate()) as Record<string, unknown>
      if (drawerMode === 'create') {
        const pick = String(v.model_pick || '')
        const parsed = parseModelPick(pick)
        const channelId = Number(v.channel_pick)
        if (!Number.isFinite(channelId) || channelId <= 0) {
          Message.error('请选择渠道')
          return
        }
        const body: Parameters<typeof createLLMAbility>[0] = {
          group: String(v.group || '').trim(),
          channel_id: channelId,
          enabled: Boolean(v.enabled),
          priority: Number(v.priority) || 0,
          weight: Math.max(1, Number(v.weight) || 1),
          tag: String(v.tag || '').trim() || undefined,
        }
        if (parsed.modelMetaId) {
          body.model_meta_id = parsed.modelMetaId
        } else if (parsed.modelName) {
          body.model = parsed.modelName
        } else {
          Message.error('请选择模型')
          return
        }
        await createLLMAbility(body)
        Message.success('已创建')
      } else if (editingRow) {
        await patchLLMAbility(
          {
            group: editingRow.group,
            model: editingRow.model,
            channel_id: editingRow.channel_id,
          },
          {
            enabled: Boolean(v.enabled),
            priority: Number(v.priority) || 0,
            weight: Math.max(1, Number(v.weight) || 1),
            tag: String(v.tag || '').trim() || undefined,
          },
        )
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

  const onDelete = async (row: LLMAbilityRow) => {
    try {
      await deleteLLMAbility({
        group: row.group,
        model: row.model,
        channel_id: row.channel_id,
      })
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  const onSync = async () => {
    if (syncChannelPick == null || syncChannelPick <= 0) {
      Message.warning('请选择要同步的渠道')
      return
    }
    const id = syncChannelPick
    setSyncing(true)
    try {
      await syncLLMAbilitiesFromChannel(id)
      Message.success('已从渠道同步能力表')
      void load()
      void loadFormOpts()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '同步失败')
    } finally {
      setSyncing(false)
    }
  }

  return (
    <AdminOnly title="LLM 能力">
      <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
        <Title heading={5} className="!mb-1 !mt-0 shrink-0">
          LLM 能力（abilities）
        </Title>
        <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
          按「凭证分组 + 请求体 model」匹配启用中的能力行，再按 priority / weight 选择上游渠道；若无匹配能力则回退为分组下全量渠道。模型与渠道请从下拉选择（模型可来自元数据或渠道 models
          字段解析出的名称）。渠道变更后会自动同步能力表。
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
            placeholder="筛选 model"
            value={draftModel}
            onChange={setDraftModel}
            style={{ width: 180 }}
          />
          <InputNumber
            placeholder="channel_id"
            value={draftChannelFilter}
            min={1}
            onChange={(v) => setDraftChannelFilter(typeof v === 'number' ? v : undefined)}
            style={{ width: 140 }}
          />
          <Button
            type="primary"
            onClick={() => {
              setAppliedGroup(draftGroup.trim())
              setAppliedModel(draftModel.trim())
              setAppliedChannelId(
                draftChannelFilter != null && draftChannelFilter > 0 ? draftChannelFilter : undefined,
              )
              setPage(1)
            }}
          >
            查询
          </Button>
          <Button type="primary" onClick={openCreate}>
            新建能力
          </Button>
          <Button onClick={() => void load()} loading={loading}>
            刷新
          </Button>
          <Select
            allowClear
            showSearch
            placeholder="从渠道同步…"
            loading={optsLoading}
            options={channelSelectOptions}
            value={syncChannelPick}
            onChange={(v) => setSyncChannelPick(typeof v === 'number' ? v : undefined)}
            style={{ minWidth: 280 }}
          />
          <Button onClick={() => void onSync()} loading={syncing}>
            从渠道同步
          </Button>
        </div>

        <Table
          loading={loading}
          rowKey={(r) => rowKey(r)}
          data={list}
          pagination={false}
          scroll={{ x: 1040 }}
          columns={[
            {
              title: '分组',
              dataIndex: 'group',
              width: 120,
              render: (v: string) => <EllipsisCopyText text={v} maxWidth={104} copiedTip="分组已复制" />,
            },
            {
              title: '模型',
              dataIndex: 'model',
              width: 200,
              render: (v: string) => <EllipsisCopyText text={v} maxWidth={184} copiedTip="模型已复制" />,
            },
            {
              title: '渠道',
              width: 260,
              render: (_: unknown, row: LLMAbilityRow) => {
                const label = row.channel_name
                  ? `${row.channel_name} (#${row.channel_id})`
                  : `#${row.channel_id}`
                return (
                  <EllipsisCopyText text={label} maxWidth={240} copiedTip="渠道已复制" tooltipMaxLen={160} />
                )
              },
            },
            {
              title: '元数据',
              width: 88,
              render: (_: unknown, row: LLMAbilityRow) =>
                row.model_meta_id ? (
                  <EllipsisCopyText
                    text={`#${row.model_meta_id}`}
                    maxWidth={72}
                    copiedTip="元数据 ID 已复制"
                    tooltipMaxLen={48}
                  />
                ) : (
                  '—'
                ),
            },
            {
              title: '启用',
              dataIndex: 'enabled',
              width: 72,
              render: (v: boolean) => (v ? '是' : '否'),
            },
            { title: 'Priority', dataIndex: 'priority', width: 88 },
            { title: 'Weight', dataIndex: 'weight', width: 72 },
            {
              title: 'Tag',
              dataIndex: 'tag',
              width: 120,
              render: (t: string | null) => <EllipsisCopyText text={t || '—'} maxWidth={104} copiedTip="Tag 已复制" />,
            },
            {
              title: '操作',
              width: 140,
              fixed: 'right' as const,
              render: (_: unknown, row: LLMAbilityRow) => (
                <Space>
                  <Button type="text" size="mini" onClick={() => openEdit(row)}>
                    编辑
                  </Button>
                  <Popconfirm title="确定删除？" onOk={() => onDelete(row)}>
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
          width={460}
          title={drawerMode === 'create' ? '新建能力' : '编辑能力'}
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
            <FormItem label="分组 group" field="group" rules={[{ required: true, message: '必填' }]}>
              <Input disabled={drawerMode === 'edit'} placeholder="default" />
            </FormItem>
            {drawerMode === 'create' ? (
              <>
                <FormItem
                  label="模型"
                  field="model_pick"
                  rules={[{ required: true, message: '请选择模型' }]}
                  extra="带 ★ 的项来自「模型元数据」；◇ 仅来自渠道 models 字段。"
                >
                  <Select
                    showSearch
                    allowClear
                    loading={optsLoading}
                    options={modelSelectOptions}
                    placeholder="选择模型名（与上游 JSON model 一致）"
                  />
                </FormItem>
                <FormItem
                  label="渠道"
                  field="channel_pick"
                  rules={[{ required: true, message: '请选择渠道' }]}
                >
                  <Select
                    showSearch
                    loading={optsLoading}
                    options={channelSelectOptions}
                    placeholder="选择 LLM 渠道"
                  />
                </FormItem>
              </>
            ) : (
              <>
                <FormItem label="模型（不可改）">
                  <Input disabled value={editingRow?.model} />
                </FormItem>
                <FormItem label="渠道（不可改）">
                  <Input
                    disabled
                    value={
                      editingRow
                        ? `${editingRow.channel_name || ''} (#${editingRow.channel_id})`.trim()
                        : ''
                    }
                  />
                </FormItem>
              </>
            )}
            <FormItem label="启用" field="enabled" triggerPropName="checked">
              <Switch />
            </FormItem>
            <FormItem label="Priority" field="priority">
              <InputNumber style={{ width: '100%' }} />
            </FormItem>
            <FormItem label="Weight" field="weight">
              <InputNumber min={1} style={{ width: '100%' }} />
            </FormItem>
            <FormItem label="Tag" field="tag">
              <Input allowClear />
            </FormItem>
          </Form>
        </Drawer>
      </div>
    </AdminOnly>
  )
}
