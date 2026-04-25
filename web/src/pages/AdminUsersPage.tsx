import {
  Button,
  Input,
  Message,
  Modal,
  Pagination,
  Select,
  Spin,
  Table,
  Typography,
} from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Users } from 'lucide-react'
import { type AdminUserRow, listAdminUsers, patchAdminUser } from '@/api/adminUsers'
import { useAuthStore } from '@/stores/authStore'

const { Title, Paragraph, Text } = Typography

const STATUS_OPTIONS = [
  { label: '正常 active', value: 'active' },
  { label: '待验证 pending_verification', value: 'pending_verification' },
  { label: '暂停 suspended', value: 'suspended' },
  { label: '封禁 banned', value: 'banned' },
]

const ROLE_OPTIONS = [
  { label: '用户 user', value: 'user' },
  { label: '管理员 admin', value: 'admin' },
  { label: '超级管理员 superadmin', value: 'superadmin' },
]

function fmtTime(s?: string): string {
  if (!s || !String(s).trim()) return '—'
  return String(s)
}

function roleLabel(role: string): string {
  const r = (role || '').toLowerCase()
  if (r === 'superadmin') return '超级管理员'
  if (r === 'admin') return '管理员'
  return '用户'
}

function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message
  const o = e as { msg?: string }
  if (o && typeof o.msg === 'string') return o.msg
  return '操作失败'
}

/** 表格单元格单行省略，title 悬停可看全文 */
function CellEllipsis(props: { text: string; className?: string }) {
  const t = props.text ?? ''
  return (
    <span
      className={`block min-w-0 max-w-full truncate whitespace-nowrap ${props.className ?? ''}`}
      title={t}
    >
      {t || '—'}
    </span>
  )
}

export function AdminUsersPage() {
  const me = useAuthStore((s) => s.user)
  const isSuper = (me?.role || '').toLowerCase() === 'superadmin'

  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<AdminUserRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)

  const [qEmail, setQEmail] = useState('')
  const [qStatus, setQStatus] = useState('')
  const [qRole, setQRole] = useState('')
  const [aEmail, setAEmail] = useState('')
  const [aStatus, setAStatus] = useState('')
  const [aRole, setARole] = useState('')

  const [editOpen, setEditOpen] = useState(false)
  const [editRow, setEditRow] = useState<AdminUserRow | null>(null)
  const [saving, setSaving] = useState(false)
  const [fStatus, setFStatus] = useState('active')
  const [fRole, setFRole] = useState('user')
  const [fDisplay, setFDisplay] = useState('')
  const [fLocale, setFLocale] = useState('')

  const listParams = useMemo(
    () => ({
      page,
      pageSize,
      ...(aEmail.trim() ? { email: aEmail.trim() } : {}),
      ...(aStatus.trim() ? { status: aStatus.trim() } : {}),
      ...(aRole.trim() ? { role: aRole.trim() } : {}),
    }),
    [page, pageSize, aEmail, aStatus, aRole],
  )

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await listAdminUsers(listParams)
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
    setAEmail(qEmail)
    setAStatus(qStatus)
    setARole(qRole)
    setPage(1)
  }

  const resetFilters = () => {
    setQEmail('')
    setQStatus('')
    setQRole('')
    setAEmail('')
    setAStatus('')
    setARole('')
    setPage(1)
  }

  const openEdit = (row: AdminUserRow) => {
    setEditRow(row)
    setFStatus(row.status || 'active')
    setFRole(row.role || 'user')
    setFDisplay(row.displayName ?? '')
    setFLocale(row.locale ?? '')
    setEditOpen(true)
  }

  const submitEdit = async () => {
    if (!editRow) return
    const body: Parameters<typeof patchAdminUser>[1] = {}
    if (fStatus !== (editRow.status || '')) body.status = fStatus
    if (isSuper && fRole !== (editRow.role || 'user')) body.role = fRole
    const disp = fDisplay.trim()
    if (disp && disp !== (editRow.displayName ?? '').trim()) body.display_name = disp
    const loc = fLocale.trim()
    if (loc && loc !== (editRow.locale ?? '').trim()) body.locale = loc

    if (Object.keys(body).length === 0) {
      Message.warning('没有修改任何字段')
      return
    }
    setSaving(true)
    try {
      await patchAdminUser(editRow.id, body)
      Message.success('已保存')
      setEditOpen(false)
      setEditRow(null)
      void load()
    } catch (e) {
      Message.error(errMsg(e))
    } finally {
      setSaving(false)
    }
  }

  const columns = [
    {
      title: 'ID',
      dataIndex: 'id',
      width: 120,
      render: (v: number) => <CellEllipsis className="font-mono text-[12px] tabular-nums" text={String(v)} />,
    },
    {
      title: '邮箱',
      dataIndex: 'email',
      width: 200,
      render: (v: string) => <CellEllipsis text={v ?? ''} />,
    },
    {
      title: '显示名',
      dataIndex: 'displayName',
      width: 120,
      render: (v: string) => <CellEllipsis text={v ?? ''} />,
    },
    {
      title: '角色',
      dataIndex: 'role',
      width: 100,
      render: (v: string) => <CellEllipsis text={roleLabel(v || 'user')} />,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 88,
      render: (v: string) => <CellEllipsis text={v ?? ''} />,
    },
    {
      title: '验证',
      width: 72,
      render: (_: unknown, r: AdminUserRow) => (
        <span className="block whitespace-nowrap">
          {r.emailVerified ? <Text type="success">已验证</Text> : '—'}
        </span>
      ),
    },
    {
      title: '注册时间',
      dataIndex: 'createdAt',
      width: 176,
      render: (v: string) => <CellEllipsis className="font-mono text-[12px]" text={fmtTime(v)} />,
    },
    {
      title: '操作',
      width: 72,
      fixed: 'right' as const,
      render: (_: unknown, r: AdminUserRow) => (
        <span className="block whitespace-nowrap">
          <Button type="text" size="mini" onClick={() => openEdit(r)}>
            编辑
          </Button>
        </span>
      ),
    },
  ]

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <div className="mb-3 flex shrink-0 items-center gap-2">
        <Users size={20} strokeWidth={1.85} className="text-[var(--color-text-2)]" />
        <Title heading={5} className="!mb-0 !mt-0">
          用户管理
        </Title>
      </div>
      <Paragraph type="secondary" className="!mb-4 !mt-0 max-w-3xl text-[13px]">
        管理员可检索用户并修改状态、显示名与语言；变更角色仅超级管理员可用，且不能在前端修改超级管理员账号（由后端校验）。
      </Paragraph>

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <Input addBefore="邮箱" style={{ width: 220 }} value={qEmail} onChange={setQEmail} placeholder="模糊匹配" />
        <Input addBefore="状态" style={{ width: 140 }} value={qStatus} onChange={setQStatus} placeholder="active" />
        <Input addBefore="角色" style={{ width: 120 }} value={qRole} onChange={setQRole} placeholder="user" />
        <Button type="primary" onClick={applyFilters}>
          查询
        </Button>
        <Button onClick={resetFilters}>重置</Button>
      </div>

      <div className="min-h-0 flex-1 min-w-0">
        <Spin loading={loading} className="block w-full">
          <div className="min-w-0 [&_table]:w-full [&_table]:table-fixed">
            <Table
              rowKey={(r) => String(r.id)}
              columns={columns}
              data={list}
              pagination={false}
              borderCell
              scroll={{ x: 1100 }}
              className="[&_.arco-table-th]:whitespace-nowrap [&_.arco-table-td]:align-middle"
            />
          </div>
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
        title={editRow ? `编辑用户 #${editRow.id}` : '编辑用户'}
        visible={editOpen}
        confirmLoading={saving}
        onOk={() => void submitEdit()}
        onCancel={() => {
          setEditOpen(false)
          setEditRow(null)
        }}
        unmountOnExit
      >
        {editRow ? (
          <div className="flex flex-col gap-3 pt-1">
            <div>
              <Text type="secondary" className="mb-1 block text-[12px]">
                邮箱
              </Text>
              <Input value={editRow.email} disabled />
            </div>
            <div>
              <Text type="secondary" className="mb-1 block text-[12px]">
                状态
              </Text>
              <Select value={fStatus} onChange={(v) => setFStatus(String(v))} options={STATUS_OPTIONS} />
            </div>
            {isSuper ? (
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  角色（仅超级管理员可改）
                </Text>
                <Select value={fRole} onChange={(v) => setFRole(String(v))} options={ROLE_OPTIONS} />
              </div>
            ) : null}
            <div>
              <Text type="secondary" className="mb-1 block text-[12px]">
                显示名 display_name
              </Text>
              <Input value={fDisplay} onChange={setFDisplay} placeholder="留空则不修改" />
            </div>
            <div>
              <Text type="secondary" className="mb-1 block text-[12px]">
                语言 locale
              </Text>
              <Input value={fLocale} onChange={setFLocale} placeholder="如 zh-CN" />
            </div>
          </div>
        ) : null}
      </Modal>
    </div>
  )
}
