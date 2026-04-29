import {
  Alert,
  Button,
  DatePicker,
  Drawer,
  Form,
  Grid,
  Input,
  InputNumber,
  Message,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Typography,
} from '@arco-design/web-react'
import dayjs, { type Dayjs } from 'dayjs'
import type { CSSProperties } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  createCredential,
  deleteCredential,
  listCredentialGroups,
  listCredentials,
  listLlmAvailableModelsForGroup,
  updateCredential,
  type CredentialCreateBody,
  type CredentialCreateResult,
  type CredentialKind,
  type CredentialRow,
  type CredentialUpdateBody,
} from '@/api/credentials'
import { EllipsisCopyText } from '@/components/common/EllipsisCopyText'

const { Title, Paragraph, Text } = Typography
const FormItem = Form.Item
const Row = Grid.Row
const Col = Grid.Col

const KIND_OPTIONS: { label: string; value: CredentialKind }[] = [
  { label: '大模型 LLM', value: 'llm' },
  { label: '语音识别 ASR', value: 'asr' },
  { label: '语音合成 TTS', value: 'tts' },
  { label: '邮件 API', value: 'email' },
]

function kindLabel(k: string): string {
  const o = KIND_OPTIONS.find((x) => x.value === k)
  return o?.label ?? k
}

function statusTag(status: number) {
  if (status === 1) return <Tag color="green">启用</Tag>
  return <Tag color="gray">禁用</Tag>
}

function expiredToDayjs(sec: number): Dayjs | undefined {
  if (sec <= 0 || sec === -1) return undefined
  return dayjs.unix(sec)
}

/** Arco DatePicker 可能返回 string / number / Dayjs，统一用 dayjs 再判 isValid。 */
function dayjsToExpiredUnix(d: unknown): number {
  if (d == null || d === '') return -1
  const x = dayjs(d as string | number | Date | Dayjs)
  if (!x.isValid()) return -1
  return x.endOf('day').unix()
}

/** 从凭证 openapi_model_catalog 解析出已选模型 id 列表 */
function parseCatalogToModelIds(cat: unknown): string[] {
  if (cat == null || cat === '') return []
  if (Array.isArray(cat)) {
    const out: string[] = []
    for (const x of cat) {
      if (typeof x === 'string' && x.trim()) out.push(x.trim())
      else if (x && typeof x === 'object' && 'id' in (x as object)) {
        const id = String((x as { id?: unknown }).id ?? '').trim()
        if (id) out.push(id)
      }
    }
    return out
  }
  if (typeof cat === 'string') {
    const raw = cat.trim()
    if (!raw) return []
    try {
      return parseCatalogToModelIds(JSON.parse(raw) as unknown)
    } catch {
      return []
    }
  }
  return []
}

export function CredentialsPage() {
  const [loading, setLoading] = useState(false)
  const [rows, setRows] = useState<CredentialRow[]>([])
  const [kindFilter, setKindFilter] = useState<string>('')
  const [groupFilter, setGroupFilter] = useState<string>('')
  const [groupOptions, setGroupOptions] = useState<string[]>([])

  const [createOpen, setCreateOpen] = useState(false)
  const [createForm] = Form.useForm()
  const [createSaving, setCreateSaving] = useState(false)
  const [createdSecret, setCreatedSecret] = useState<CredentialCreateResult | null>(null)

  const [editOpen, setEditOpen] = useState(false)
  const [editForm] = Form.useForm()
  const [editSaving, setEditSaving] = useState(false)
  const [editing, setEditing] = useState<CredentialRow | null>(null)
  const [apiCatalogModels, setApiCatalogModels] = useState<string[]>([])
  const [apiCatalogLoading, setApiCatalogLoading] = useState(false)
  const [manualModelIdDraft, setManualModelIdDraft] = useState('')
  const catalogIdsWatch = Form.useWatch('openapi_model_catalog_ids', editForm) as string[] | undefined

  const catalogSelectOptions = useMemo(() => {
    const apiSet = new Set(apiCatalogModels)
    const opts: { label: string; value: string }[] = apiCatalogModels.map((id) => ({ label: id, value: id }))
    for (const id of catalogIdsWatch ?? []) {
      const t = String(id).trim()
      if (!t) continue
      if (!apiSet.has(t)) {
        opts.push({ label: `${t}（未在接口列表）`, value: t })
      }
    }
    const seen = new Set<string>()
    const dedup: { label: string; value: string }[] = []
    for (const o of opts) {
      if (seen.has(o.value)) continue
      seen.add(o.value)
      dedup.push(o)
    }
    return dedup.sort((a, b) => a.value.localeCompare(b.value))
  }, [apiCatalogModels, catalogIdsWatch])

  const loadApiCatalogModelsForGroup = useCallback(async (group: string | undefined) => {
    setApiCatalogLoading(true)
    try {
      const g = group != null && String(group).trim() !== '' ? String(group).trim() : undefined
      const { models } = await listLlmAvailableModelsForGroup(g)
      setApiCatalogModels(models)
    } catch {
      setApiCatalogModels([])
    } finally {
      setApiCatalogLoading(false)
    }
  }, [])

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [list, groups] = await Promise.all([
        listCredentials({
          kind: kindFilter || undefined,
          group: groupFilter || undefined,
        }),
        listCredentialGroups(),
      ])
      setRows(list)
      setGroupOptions(groups)
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [kindFilter, groupFilter])

  useEffect(() => {
    void load()
  }, [load])

  const openCreate = () => {
    createForm.resetFields()
    createForm.setFieldsValue({
      kind: 'llm',
      name: '',
      remain_quota: 0,
      unlimited_quota: false,
      allow_ips: '',
      group: '',
      cross_group_retry: false,
      expires_day: undefined,
    })
    setCreateOpen(true)
  }

  const submitCreate = async () => {
    setCreateSaving(true)
    try {
      const v = (await createForm.validate()) as Record<string, unknown>
      const body: CredentialCreateBody = {
        kind: String(v.kind ?? 'llm') as CredentialKind,
        name: String(v.name ?? '').trim(),
        remain_quota: Number(v.remain_quota) || 0,
        unlimited_quota: Boolean(v.unlimited_quota),
        allow_ips: String(v.allow_ips ?? '').trim(),
        group: String(v.group ?? '').trim(),
        cross_group_retry: Boolean(v.cross_group_retry),
        expired_time: dayjsToExpiredUnix(v.expires_day),
      }
      const res = await createCredential(body)
      setCreateOpen(false)
      setCreatedSecret(res)
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '创建失败')
    } finally {
      setCreateSaving(false)
    }
  }

  const openEdit = (row: CredentialRow) => {
    setEditing(row)
    editForm.setFieldsValue({
      name: row.name,
      status: row.status,
      remain_quota: row.remain_quota,
      unlimited_quota: row.unlimited_quota,
      allow_ips: row.allow_ips ?? '',
      group: row.group ?? '',
      cross_group_retry: row.cross_group_retry,
      expires_day: expiredToDayjs(row.expired_time),
      openapi_model_catalog_ids:
        row.kind === 'llm' ? parseCatalogToModelIds(row.openapi_model_catalog) : [],
    })
    setManualModelIdDraft('')
    setEditOpen(true)
    if (row.kind === 'llm') {
      void loadApiCatalogModelsForGroup(row.group || undefined)
    } else {
      setApiCatalogModels([])
    }
  }

  const submitEdit = async () => {
    if (!editing) return
    setEditSaving(true)
    try {
      const v = (await editForm.validate()) as Record<string, unknown>
      const body: CredentialUpdateBody = {
        name: String(v.name ?? '').trim(),
        status: Number(v.status) === 0 ? 0 : 1,
        remain_quota: Number(v.remain_quota) || 0,
        unlimited_quota: Boolean(v.unlimited_quota),
        allow_ips: String(v.allow_ips ?? '').trim(),
        group: String(v.group ?? '').trim(),
        cross_group_retry: Boolean(v.cross_group_retry),
        expired_time: dayjsToExpiredUnix(v.expires_day),
      }
      if (editing.kind === 'llm') {
        const ids = (v.openapi_model_catalog_ids as string[] | undefined) ?? []
        const cleaned = [...new Set(ids.map((x) => String(x).trim()).filter(Boolean))]
        body.openapi_model_catalog = cleaned.length === 0 ? '' : JSON.stringify(cleaned)
      }
      await updateCredential(editing.id, body)
      Message.success('已保存')
      setEditOpen(false)
      setEditing(null)
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '保存失败')
    } finally {
      setEditSaving(false)
    }
  }

  const onDelete = async (id: number) => {
    try {
      await deleteCredential(id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  const cols = useMemo(
    () => [
      {
        title: 'ID',
        dataIndex: 'id',
        width: 88,
        render: (v: number) => <EllipsisCopyText text={v} maxWidth={72} copiedTip="ID 已复制" tooltipMaxLen={96} />,
      },
      {
        title: '类型',
        dataIndex: 'kind',
        width: 140,
        render: (k: string) => kindLabel(k),
      },
      {
        title: '名称',
        dataIndex: 'name',
        width: 160,
        render: (v: string) => <EllipsisCopyText text={v} maxWidth={144} copiedTip="名称已复制" />,
      },
      {
        title: '分组',
        dataIndex: 'group',
        width: 120,
        render: (v: string) => <EllipsisCopyText text={v ?? ''} maxWidth={104} copiedTip="分组已复制" />,
      },
      {
        title: '密钥（脱敏）',
        dataIndex: 'key_masked',
        width: 200,
        render: (v: string) => <EllipsisCopyText text={v ?? ''} maxWidth={184} copiedTip="密钥已复制" />,
      },
      {
        title: '状态',
        dataIndex: 'status',
        width: 88,
        render: (s: number) => statusTag(s),
      },
      { title: '剩余额度', dataIndex: 'remain_quota', width: 96 },
      { title: '已用', dataIndex: 'used_quota', width: 72 },
      {
        title: '操作',
        width: 200,
        fixed: 'right' as const,
        render: (_: unknown, row: CredentialRow) => (
          <Space>
            <Button type="text" size="mini" onClick={() => openEdit(row)}>
              配置
            </Button>
            <Popconfirm title="删除后密钥不可恢复，确定？" onOk={() => onDelete(row.id)}>
              <Button type="text" size="mini" status="danger">
                删除
              </Button>
            </Popconfirm>
          </Space>
        ),
      },
    ],
    [],
  )

  const drawerBodyStyle: CSSProperties = { padding: '12px 16px 8px' }

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        访问密钥
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 max-w-3xl text-[13px]">
        创建后<strong>仅当次</strong>展示完整密钥；列表与详情只显示脱敏。遗失密钥请删除后重新创建。可在此调整额度、禁用凭证等。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <Button type="primary" onClick={openCreate}>
          新建密钥
        </Button>
        <Select
          placeholder="筛选类型"
          allowClear
          style={{ width: 180 }}
          value={kindFilter || undefined}
          onChange={(v) => setKindFilter(v == null || v === '' ? '' : String(v))}
          options={KIND_OPTIONS}
        />
        <Select
          placeholder="筛选分组"
          allowClear
          style={{ width: 200 }}
          value={groupFilter || undefined}
          onChange={(v) => setGroupFilter(v == null || v === '' ? '' : String(v))}
          options={groupOptions.map((g) => ({ label: g, value: g }))}
        />
        <Button onClick={() => void load()} loading={loading}>
          刷新
        </Button>
      </div>

      <Table
        rowKey="id"
        loading={loading}
        data={rows}
        columns={cols}
        pagination={false}
        scroll={{ x: 1220 }}
      />

      <Drawer
        title={<span className="text-[15px] font-semibold text-[var(--color-text-1)]">新建密钥</span>}
        visible={createOpen}
        width={400}
        placement="right"
        bodyStyle={drawerBodyStyle}
        footer={
          <div className="flex justify-end gap-2 border-t border-[var(--color-border-2)] px-3 py-2.5">
            <Button size="small" onClick={() => setCreateOpen(false)}>
              取消
            </Button>
            <Button size="small" type="primary" loading={createSaving} onClick={() => void submitCreate()}>
              创建
            </Button>
          </div>
        }
        onCancel={() => setCreateOpen(false)}
        unmountOnExit
        className="credential-drawer"
      >
        <Form form={createForm} layout="vertical" size="small" className="credential-drawer__form">
          <FormItem label="类型" field="kind" rules={[{ required: true }]}>
            <Select options={KIND_OPTIONS} size="small" />
          </FormItem>
          <FormItem label="名称" field="name" rules={[{ required: true, message: '请输入名称' }]}>
            <Input placeholder="便于识别" size="small" />
          </FormItem>
          <Row gutter={10}>
            <Col span={15}>
              <FormItem label="剩余额度" field="remain_quota" className="!mb-2">
                <InputNumber min={0} size="small" style={{ width: '100%' }} />
              </FormItem>
            </Col>
            <Col span={9}>
              <FormItem label="无限" field="unlimited_quota" triggerPropName="checked" className="!mb-2">
                <Switch size="small" />
              </FormItem>
            </Col>
          </Row>
          <FormItem
            label="过期日期"
            field="expires_day"
            extra="不选表示永不过期；选中日期按当日 23:59:59 失效"
            className="!mb-2"
          >
            <DatePicker style={{ width: '100%' }} size="small" allowClear />
          </FormItem>
          <FormItem label="允许 IP" field="allow_ips" className="!mb-2">
            <Input placeholder="逗号分隔，可选" size="small" />
          </FormItem>
          <FormItem label="分组" field="group" className="!mb-2">
            <Input placeholder="可选" size="small" />
          </FormItem>
          <FormItem label="跨分组重试" field="cross_group_retry" triggerPropName="checked" className="!mb-0">
            <Switch size="small" />
          </FormItem>
        </Form>
      </Drawer>

      <Drawer
        title={
          <span className="text-[15px] font-semibold text-[var(--color-text-1)]">
            {editing ? `编辑 · ${editing.name}` : '编辑'}
          </span>
        }
        visible={editOpen}
        width={editing?.kind === 'llm' ? 460 : 400}
        placement="right"
        bodyStyle={drawerBodyStyle}
        footer={
          <div className="flex justify-end gap-2 border-t border-[var(--color-border-2)] px-3 py-2.5">
            <Button
              size="small"
              onClick={() => {
                setEditOpen(false)
                setEditing(null)
                setApiCatalogModels([])
                setManualModelIdDraft('')
              }}
            >
              取消
            </Button>
            <Button size="small" type="primary" loading={editSaving} onClick={() => void submitEdit()}>
              保存
            </Button>
          </div>
        }
        onCancel={() => {
          setEditOpen(false)
          setEditing(null)
          setApiCatalogModels([])
          setManualModelIdDraft('')
        }}
        unmountOnExit
        className="credential-drawer"
      >
        {editing ? (
          <Form
            form={editForm}
            layout="vertical"
            size="small"
            className="credential-drawer__form"
            onValuesChange={(changed, values) => {
              if (!editing || editing.kind !== 'llm') return
              if ('group' in changed) {
                const g = String(values.group ?? '').trim()
                void loadApiCatalogModelsForGroup(g || undefined)
              }
            }}
          >
            <Alert type="info" className="!mb-2 !rounded-md" content={`#${editing.id} · 已用 ${editing.used_quota}`} />
            <FormItem label="名称" field="name" rules={[{ required: true }]}>
              <Input size="small" />
            </FormItem>
            <FormItem label="状态" field="status">
              <Select
                size="small"
                options={[
                  { label: '启用', value: 1 },
                  { label: '禁用', value: 0 },
                ]}
              />
            </FormItem>
            <Row gutter={10}>
              <Col span={15}>
                <FormItem label="剩余额度" field="remain_quota" className="!mb-2">
                  <InputNumber min={0} size="small" style={{ width: '100%' }} />
                </FormItem>
              </Col>
              <Col span={9}>
                <FormItem label="无限" field="unlimited_quota" triggerPropName="checked" className="!mb-2">
                  <Switch size="small" />
                </FormItem>
              </Col>
            </Row>
            <FormItem label="过期日期" field="expires_day" className="!mb-2" extra="不选表示永不过期">
              <DatePicker style={{ width: '100%' }} size="small" allowClear />
            </FormItem>
            <FormItem label="允许 IP" field="allow_ips" className="!mb-2">
              <Input size="small" />
            </FormItem>
            <FormItem label="分组" field="group" className="!mb-2">
              <Input size="small" />
            </FormItem>
            <FormItem label="跨分组重试" field="cross_group_retry" triggerPropName="checked" className="!mb-2">
              <Switch size="small" />
            </FormItem>
            {editing.kind === 'llm' ? (
              <>
                <FormItem
                  label="OpenAPI 模型目录"
                  field="openapi_model_catalog_ids"
                  className="!mb-2"
                  extra="候选来自接口 GET /api/credentials/llm-available-models（与 /v1/models 无目录时同源：能力表优先）。全部清空则仍走自动汇总。可选手动添加未出现在列表中的上游模型名。"
                >
                  <Select
                    mode="multiple"
                    showSearch
                    loading={apiCatalogLoading}
                    options={catalogSelectOptions}
                    placeholder="从列表勾选模型"
                    size="small"
                  />
                </FormItem>
                <FormItem label="手动添加模型 ID" className="!mb-0">
                  <Input
                    size="small"
                    value={manualModelIdDraft}
                    onChange={setManualModelIdDraft}
                    placeholder="输入后回车加入已选"
                    onPressEnter={() => {
                      const v = manualModelIdDraft.trim()
                      if (!v) return
                      const cur = (editForm.getFieldValue('openapi_model_catalog_ids') as string[]) ?? []
                      if (cur.includes(v)) {
                        Message.info('已在已选列表中')
                        return
                      }
                      editForm.setFieldsValue({ openapi_model_catalog_ids: [...cur, v] })
                      setManualModelIdDraft('')
                    }}
                  />
                </FormItem>
              </>
            ) : null}
          </Form>
        ) : null}
      </Drawer>

      <Modal
        title="密钥已创建"
        visible={createdSecret != null}
        footer={
          <Button type="primary" size="small" onClick={() => setCreatedSecret(null)}>
            我已保存
          </Button>
        }
        closable={false}
        maskClosable={false}
        style={{ width: 440 }}
        className="credential-secret-modal"
      >
        {createdSecret ? (
          <div className="space-y-2.5">
            <Alert type="warning" content={createdSecret.key_hint} />
            <Text style={{ fontWeight: 600 }} className="text-[13px]">
              完整密钥（仅此一次）
            </Text>
            <Input.TextArea
              readOnly
              className="font-mono text-[12px]"
              value={createdSecret.key}
              autoSize={{ minRows: 2, maxRows: 4 }}
            />
            <Button
              type="outline"
              size="small"
              long
              onClick={async () => {
                try {
                  await navigator.clipboard.writeText(createdSecret.key)
                  Message.success('已复制')
                } catch {
                  Message.error('请手动复制')
                }
              }}
            >
              复制密钥
            </Button>
          </div>
        ) : null}
      </Modal>
    </div>
  )
}
