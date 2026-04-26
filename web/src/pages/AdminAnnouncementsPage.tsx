import {
  Button,
  Input,
  Message,
  Modal,
  Popconfirm,
  Space,
  Switch,
  Table,
  Typography,
} from '@arco-design/web-react'
import type { ColumnProps } from '@arco-design/web-react/es/Table'
import { useCallback, useEffect, useState } from 'react'
import {
  createAdminAnnouncement,
  deleteAdminAnnouncement,
  listAdminAnnouncements,
  updateAdminAnnouncement,
  type SiteAnnouncement,
} from '@/api/announcements'
import { AdminOnly } from '@/components/AdminOnly'

const { Title, Paragraph } = Typography
const TextArea = Input.TextArea

function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message
  const o = e as { msg?: string }
  if (o && typeof o.msg === 'string') return o.msg
  return '操作失败'
}

export function AdminAnnouncementsPage() {
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<SiteAnnouncement[]>([])
  const [open, setOpen] = useState(false)
  const [editId, setEditId] = useState<number | null>(null)
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [pinned, setPinned] = useState(false)
  const [enabled, setEnabled] = useState(true)
  const [sortOrder, setSortOrder] = useState(0)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const rows = await listAdminAnnouncements()
      setList(rows)
    } catch (e) {
      Message.error(errMsg(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const openCreate = () => {
    setEditId(null)
    setTitle('')
    setBody('')
    setPinned(false)
    setEnabled(true)
    setSortOrder(0)
    setOpen(true)
  }

  const openEdit = (row: SiteAnnouncement) => {
    setEditId(row.id)
    setTitle(row.title)
    setBody(row.body || '')
    setPinned(row.pinned)
    setEnabled(row.enabled)
    setSortOrder(row.sort_order ?? 0)
    setOpen(true)
  }

  const save = async () => {
    const t = title.trim()
    if (!t) {
      Message.warning('请填写标题')
      return
    }
    setSaving(true)
    try {
      if (editId == null) {
        await createAdminAnnouncement({
          title: t,
          body: body.trim() || undefined,
          pinned,
          enabled,
          sort_order: sortOrder,
        })
        Message.success('已创建')
      } else {
        await updateAdminAnnouncement(editId, {
          title: t,
          body: body.trim(),
          pinned,
          enabled,
          sort_order: sortOrder,
        })
        Message.success('已保存')
      }
      setOpen(false)
      void load()
    } catch (e) {
      Message.error(errMsg(e))
    } finally {
      setSaving(false)
    }
  }

  const columns: ColumnProps<SiteAnnouncement>[] = [
    { title: 'ID', dataIndex: 'id', width: 72 },
    { title: '标题', dataIndex: 'title', ellipsis: true },
    {
      title: '置顶',
      dataIndex: 'pinned',
      width: 80,
      render: (v: boolean) => (v ? '是' : '否'),
    },
    {
      title: '启用',
      dataIndex: 'enabled',
      width: 80,
      render: (v: boolean) => (v ? '是' : '否'),
    },
    { title: '排序', dataIndex: 'sort_order', width: 72 },
    {
      title: '操作',
      width: 160,
      render: (_v: unknown, row: SiteAnnouncement) => (
        <Space>
          <Button type="text" size="mini" onClick={() => openEdit(row)}>
            编辑
          </Button>
          <Popconfirm title="确定删除？" onOk={() => void onDelete(row.id)}>
            <Button type="text" size="mini" status="danger">
              删除
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ]

  const onDelete = async (id: number) => {
    try {
      await deleteAdminAnnouncement(id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(errMsg(e))
    }
  }

  return (
    <AdminOnly title="公告管理">
      <div className="flex h-full min-h-0 flex-1 flex-col overflow-auto px-5 py-5">
        <div className="mb-4 flex flex-wrap items-end justify-between gap-3">
          <div>
            <Title heading={5} className="!mb-1 !mt-0">
              公告管理
            </Title>
            <Paragraph type="secondary" className="!mb-0 !mt-0 text-[13px]">
              维护站点公告；前台「公告」页仅展示已启用条目。
            </Paragraph>
          </div>
          <Button type="primary" onClick={openCreate}>
            新建公告
          </Button>
        </div>

        <Table rowKey="id" loading={loading} columns={columns} data={list} pagination={false} borderCell size="small" />

        <Modal
          title={editId == null ? '新建公告' : `编辑公告 #${editId}`}
          visible={open}
          onOk={() => void save()}
          onCancel={() => setOpen(false)}
          confirmLoading={saving}
          unmountOnExit
          style={{ width: 560 }}
        >
          <div className="space-y-3 pt-1">
            <div>
              <div className="mb-1 text-[12px] text-[var(--color-text-3)]">标题</div>
              <Input value={title} onChange={setTitle} placeholder="必填" maxLength={255} showWordLimit />
            </div>
            <div>
              <div className="mb-1 text-[12px] text-[var(--color-text-3)]">正文</div>
              <TextArea value={body} onChange={setBody} placeholder="支持多行" autoSize={{ minRows: 5, maxRows: 16 }} />
            </div>
            <div className="flex flex-wrap gap-6">
              <Space>
                <span className="text-[13px]">置顶</span>
                <Switch checked={pinned} onChange={setPinned} />
              </Space>
              <Space>
                <span className="text-[13px]">启用</span>
                <Switch checked={enabled} onChange={setEnabled} />
              </Space>
              <Space>
                <span className="text-[13px]">排序</span>
                <Input
                  type="number"
                  value={String(sortOrder)}
                  onChange={(v) => setSortOrder(Number.parseInt(String(v), 10) || 0)}
                  style={{ width: 96 }}
                />
              </Space>
            </div>
          </div>
        </Modal>
      </div>
    </AdminOnly>
  )
}
