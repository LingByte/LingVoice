import {
  Button,
  Drawer,
  Form,
  Input,
  InputNumber,
  Message,
  Pagination,
  Popconfirm,
  Space,
  Switch,
  Table,
  Typography,
} from '@arco-design/web-react'
import type { CSSProperties } from 'react'
import { useCallback, useEffect, useState } from 'react'
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

const { Title, Paragraph } = Typography
const FormItem = Form.Item

const drawerBodyStyle: CSSProperties = { padding: '12px 16px 8px' }

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
    configJson: string
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
  const [editingId, setEditingId] = useState<number | null>(null)
  const [drawerLoading, setDrawerLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  const label = kind === 'asr' ? 'ASR' : 'TTS'

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
      provider: '',
      name: '',
      enabled: true,
      group: '',
      sortOrder: 0,
      configJson: '{\n  \n}\n',
    })
    setDrawerOpen(true)
  }

  const openEdit = async (id: number) => {
    setDrawerMode('edit')
    setEditingId(id)
    setDrawerOpen(true)
    setDrawerLoading(true)
    try {
      const { channel } = kind === 'asr' ? await getASRChannel(id) : await getTTSChannel(id)
      let cfg = channel.configJson || ''
      if (cfg.trim() === '') {
        cfg = '{\n  \n}\n'
      } else {
        try {
          cfg = JSON.stringify(JSON.parse(cfg), null, 2)
        } catch {
          /* keep */
        }
      }
      form.setFieldsValue({
        provider: channel.provider,
        name: channel.name,
        enabled: channel.enabled,
        group: channel.group || '',
        sortOrder: channel.sortOrder ?? 0,
        configJson: cfg,
      })
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
      const v = await form.validate()
      const raw = String(v.configJson || '').trim()
      if (raw !== '') {
        try {
          JSON.parse(raw)
        } catch {
          Message.error('configJson 须为合法 JSON')
          return
        }
      }
      const body: SpeechChannelUpsert = {
        provider: String(v.provider || '').trim(),
        name: String(v.name || '').trim(),
        enabled: Boolean(v.enabled),
        group: String(v.group || '').trim(),
        sortOrder: Number(v.sortOrder) || 0,
        configJson: raw,
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

  const onDelete = async (id: number) => {
    try {
      if (kind === 'asr') await deleteASRChannel(id)
      else await deleteTTSChannel(id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '删除失败')
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        {title}
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 text-[13px]">
        {description} 新建与编辑在右侧抽屉完成。
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
        width={420}
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
          <FormItem label="厂商 provider" field="provider" rules={[{ required: true, message: '必填' }]}>
            <Input placeholder="如 aliyun_funasr、azure" />
          </FormItem>
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
          <FormItem
            label="配置 configJson"
            field="configJson"
            extra="厂商相关参数（endpoint、密钥等），须为合法 JSON；可为 {}"
          >
            <Input.TextArea className="font-mono text-[12px]" autoSize={{ minRows: 10, maxRows: 24 }} />
          </FormItem>
        </Form>
      </Drawer>
    </div>
  )
}
