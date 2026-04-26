import {
  Avatar,
  Button,
  Input,
  InputNumber,
  Message,
  Modal,
  Pagination,
  Popconfirm,
  Select,
  Spin,
  Switch,
  Table,
  Typography,
} from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Users } from 'lucide-react'
import {
  type AdminPatchUserBody,
  type AdminUserRow,
  deleteAdminUser,
  listAdminUsers,
  patchAdminUser,
} from '@/api/adminUsers'
import { useAuthStore } from '@/stores/authStore'

const { Title, Paragraph, Text } = Typography

/** 与界面展示一致：内部额度整数按 1e6 缩放为美元金额，保留 6 位小数 */
const QUOTA_MICRO_PER_UNIT = 1_000_000

function quotaToDollarAmount(n: number | undefined): string {
  const v = Number(n) || 0
  return (v / QUOTA_MICRO_PER_UNIT).toFixed(6)
}

function parseDollarToQuota(s: string): number {
  const x = Number.parseFloat(String(s).replace(/[$,\s]/g, ''))
  if (!Number.isFinite(x) || x < 0) return 0
  return Math.round(x * QUOTA_MICRO_PER_UNIT)
}

function formatQuotaMoney(n: number | undefined, unlimited?: boolean): string {
  if (unlimited) return '无限'
  return `$${quotaToDollarAmount(n)}`
}

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
  const meId = me?.id != null ? String(me.id) : ''

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
  const [fPhone, setFPhone] = useState('')
  const [fFirstName, setFFirstName] = useState('')
  const [fLastName, setFLastName] = useState('')
  const [fAvatar, setFAvatar] = useState('')
  const [fTimezone, setFTimezone] = useState('')
  const [fGender, setFGender] = useState('')
  const [fCity, setFCity] = useState('')
  const [fRegion, setFRegion] = useState('')
  const [fEmailNotif, setFEmailNotif] = useState(false)
  const [fPhoneVerified, setFPhoneVerified] = useState(false)
  const [fEmailVerified, setFEmailVerified] = useState(false)
  const [fRemainQuota, setFRemainQuota] = useState(0)
  const [fUsedQuota, setFUsedQuota] = useState(0)
  const [fUnlimitedQuota, setFUnlimitedQuota] = useState(false)

  const [quotaModalOpen, setQuotaModalOpen] = useState(false)
  const [quotaDollarInput, setQuotaDollarInput] = useState('0.000000')

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
    setFPhone(row.phone ?? '')
    setFFirstName(row.firstName ?? '')
    setFLastName(row.lastName ?? '')
    setFAvatar(row.avatar ?? '')
    setFTimezone(row.timezone ?? '')
    setFGender(row.gender ?? '')
    setFCity(row.city ?? '')
    setFRegion(row.region ?? '')
    setFEmailNotif(Boolean(row.emailNotifications))
    setFPhoneVerified(Boolean(row.phoneVerified))
    setFEmailVerified(Boolean(row.emailVerified))
    setFRemainQuota(row.remainQuota ?? 0)
    setFUsedQuota(row.usedQuota ?? 0)
    setFUnlimitedQuota(Boolean(row.unlimitedQuota))
    setEditOpen(true)
  }

  const openQuotaModal = () => {
    setQuotaDollarInput(quotaToDollarAmount(fRemainQuota))
    setQuotaModalOpen(true)
  }

  const applyQuotaModal = () => {
    setFRemainQuota(parseDollarToQuota(quotaDollarInput))
    setQuotaModalOpen(false)
  }

  const submitEdit = async () => {
    if (!editRow) return
    const body: AdminPatchUserBody = {}
    if (fStatus !== (editRow.status || '')) body.status = fStatus
    if (isSuper && fRole !== (editRow.role || 'user')) body.role = fRole
    if (fDisplay.trim() !== (editRow.displayName ?? '').trim()) body.display_name = fDisplay.trim()
    if (fLocale.trim() !== (editRow.locale ?? '').trim()) body.locale = fLocale.trim()
    if (fPhone.trim() !== (editRow.phone ?? '').trim()) body.phone = fPhone.trim()
    if (fFirstName.trim() !== (editRow.firstName ?? '').trim()) body.first_name = fFirstName.trim()
    if (fLastName.trim() !== (editRow.lastName ?? '').trim()) body.last_name = fLastName.trim()
    if (fAvatar.trim() !== (editRow.avatar ?? '').trim()) body.avatar = fAvatar.trim()
    if (fTimezone.trim() !== (editRow.timezone ?? '').trim()) body.timezone = fTimezone.trim()
    if (fGender.trim() !== (editRow.gender ?? '').trim()) body.gender = fGender.trim()
    if (fCity.trim() !== (editRow.city ?? '').trim()) body.city = fCity.trim()
    if (fRegion.trim() !== (editRow.region ?? '').trim()) body.region = fRegion.trim()
    if (fEmailNotif !== Boolean(editRow.emailNotifications)) body.email_notifications = fEmailNotif
    if (fPhoneVerified !== Boolean(editRow.phoneVerified)) body.phone_verified = fPhoneVerified
    if (fEmailVerified !== Boolean(editRow.emailVerified)) body.email_verified = fEmailVerified
    if (fRemainQuota !== (editRow.remainQuota ?? 0)) body.remain_quota = fRemainQuota
    if (fUsedQuota !== (editRow.usedQuota ?? 0)) body.used_quota = fUsedQuota
    if (fUnlimitedQuota !== Boolean(editRow.unlimitedQuota)) body.unlimited_quota = fUnlimitedQuota

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

  const onDelete = async (row: AdminUserRow) => {
    try {
      await deleteAdminUser(row.id)
      Message.success('已删除')
      void load()
    } catch (e) {
      Message.error(errMsg(e))
    }
  }

  const canDeleteRow = (r: AdminUserRow) => {
    if (meId && r.id === meId) return false
    if ((r.role || '').toLowerCase() === 'superadmin' && !isSuper) return false
    return true
  }

  const columns = [
    {
      title: '',
      dataIndex: 'avatar',
      width: 52,
      render: (_: unknown, r: AdminUserRow) => (
        <Avatar size={32} className="shrink-0">
          {r.avatar ? <img src={r.avatar} alt="" className="h-full w-full object-cover" /> : (r.email || '?').slice(0, 1).toUpperCase()}
        </Avatar>
      ),
    },
    {
      title: 'ID',
      dataIndex: 'id',
      width: 120,
      render: (v: string) => <CellEllipsis className="font-mono text-[12px] tabular-nums" text={v || '—'} />,
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
      title: '手机',
      dataIndex: 'phone',
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
      title: '剩余额度',
      width: 140,
      render: (_: unknown, r: AdminUserRow) => (
        <span className="whitespace-nowrap font-mono text-[12px] tabular-nums">
          {formatQuotaMoney(r.remainQuota, r.unlimitedQuota)}
        </span>
      ),
    },
    {
      title: '已用额度',
      width: 140,
      render: (_: unknown, r: AdminUserRow) => (
        <span className="whitespace-nowrap font-mono text-[12px] tabular-nums">
          {r.unlimitedQuota ? '—' : formatQuotaMoney(r.usedQuota, false)}
        </span>
      ),
    },
    {
      title: '验证',
      width: 100,
      render: (_: unknown, r: AdminUserRow) => (
        <span className="block whitespace-nowrap text-[12px]">
          {r.emailVerified ? <Text type="success">邮</Text> : <Text type="secondary">邮</Text>}
          <Text type="secondary"> / </Text>
          {r.phoneVerified ? <Text type="success">手</Text> : <Text type="secondary">手</Text>}
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
      width: 120,
      fixed: 'right' as const,
      render: (_: unknown, r: AdminUserRow) => (
        <span className="flex flex-nowrap items-center gap-0">
          <Button type="text" size="mini" onClick={() => openEdit(r)}>
            编辑
          </Button>
          {canDeleteRow(r) ? (
            <Popconfirm title="确认删除该用户？" content="将执行软删除，相关会话等数据策略以服务端为准。" onOk={() => void onDelete(r)}>
              <Button type="text" size="mini" status="danger">
                删除
              </Button>
            </Popconfirm>
          ) : (
            <Button type="text" size="mini" disabled>
              删除
            </Button>
          )}
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
        支持检索用户并维护账号资料、验证状态与用户级额度。额度在界面中以美元金额展示（6
        位小数，内部按 10⁶ 缩放存储）；变更角色仅超级管理员可用。删除为软删除，不可删除本人；非超级管理员不可删除超级管理员账号。
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
              scroll={{ x: 1400 }}
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
        style={{ width: 'min(640px, 96vw)' }}
        unmountOnExit
      >
        {editRow ? (
          <div className="flex max-h-[min(72vh,640px)] flex-col gap-3 overflow-y-auto pt-1 pr-1">
            <div className="flex items-start gap-3 rounded border border-[var(--color-border-2)] p-3">
              <Avatar size={56} className="shrink-0">
                {editRow.avatar ? (
                  <img src={fAvatar || editRow.avatar} alt="" className="h-full w-full object-cover" />
                ) : (
                  (editRow.email || '?').slice(0, 1).toUpperCase()
                )}
              </Avatar>
              <div className="min-w-0 flex-1 space-y-1">
                <Text className="block font-medium">{editRow.email}</Text>
                <Text type="secondary" className="!block !text-[12px]">
                  GitHub：{editRow.githubLogin || '—'} · 微信 OpenID：{editRow.wechatOpenId || '—'}
                </Text>
                <Text type="secondary" className="!block !text-[12px]">
                  登录次数 {editRow.loginCount ?? 0} · 资料完整度 {editRow.profileComplete ?? 0}% · 2FA{' '}
                  {editRow.twoFactorEnabled ? '已开启' : '未开启'}
                </Text>
              </div>
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
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  显示名
                </Text>
                <Input value={fDisplay} onChange={setFDisplay} />
              </div>
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  语言 locale
                </Text>
                <Input value={fLocale} onChange={setFLocale} placeholder="如 zh-CN" />
              </div>
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  手机
                </Text>
                <Input value={fPhone} onChange={setFPhone} />
              </div>
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  时区
                </Text>
                <Input value={fTimezone} onChange={setFTimezone} placeholder="如 Asia/Shanghai" />
              </div>
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  名
                </Text>
                <Input value={fFirstName} onChange={setFFirstName} />
              </div>
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  姓
                </Text>
                <Input value={fLastName} onChange={setFLastName} />
              </div>
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  性别
                </Text>
                <Input value={fGender} onChange={setFGender} />
              </div>
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  城市
                </Text>
                <Input value={fCity} onChange={setFCity} />
              </div>
              <div>
                <Text type="secondary" className="mb-1 block text-[12px]">
                  省州 region
                </Text>
                <Input value={fRegion} onChange={setFRegion} />
              </div>
              <div className="sm:col-span-2">
                <Text type="secondary" className="mb-1 block text-[12px]">
                  头像 URL
                </Text>
                <Input value={fAvatar} onChange={setFAvatar} placeholder="https://…" />
              </div>
            </div>
            <div className="flex flex-wrap gap-6">
              <div className="flex items-center gap-2">
                <Text type="secondary" className="text-[12px]">
                  邮件通知
                </Text>
                <Switch checked={fEmailNotif} onChange={setFEmailNotif} />
              </div>
              <div className="flex items-center gap-2">
                <Text type="secondary" className="text-[12px]">
                  邮箱已验证
                </Text>
                <Switch checked={fEmailVerified} onChange={setFEmailVerified} />
              </div>
              <div className="flex items-center gap-2">
                <Text type="secondary" className="text-[12px]">
                  手机已验证
                </Text>
                <Switch checked={fPhoneVerified} onChange={setFPhoneVerified} />
              </div>
            </div>
            <div className="rounded border border-[var(--color-border-2)] p-3">
              <Text className="mb-2 block text-[13px] font-medium">用户额度</Text>
              <div className="mb-2 flex items-center gap-2">
                <Text type="secondary" className="shrink-0 text-[12px]">
                  无限额度
                </Text>
                <Switch checked={fUnlimitedQuota} onChange={setFUnlimitedQuota} />
              </div>
              {!fUnlimitedQuota ? (
                <div className="space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <Text type="secondary" className="text-[12px]">
                      剩余（金额）
                    </Text>
                    <Text className="font-mono text-[13px] tabular-nums">{formatQuotaMoney(fRemainQuota, false)}</Text>
                    <Button type="outline" size="mini" onClick={openQuotaModal}>
                      调整额度
                    </Button>
                  </div>
                  <div>
                    <Text type="secondary" className="mb-1 block text-[12px]">
                      已用额度（金额展示，可直接改内部整数）
                    </Text>
                    <div className="font-mono text-[12px] text-[var(--color-text-2)] tabular-nums">
                      {formatQuotaMoney(fUsedQuota, false)}
                    </div>
                    <InputNumber
                      className="mt-1 w-full max-w-xs"
                      min={0}
                      value={fUsedQuota}
                      onChange={(v) => setFUsedQuota(Number(v) || 0)}
                    />
                  </div>
                </div>
              ) : null}
            </div>
            <div className="grid grid-cols-1 gap-1 text-[12px] text-[var(--color-text-3)] sm:grid-cols-2">
              <span>最近登录：{fmtTime(editRow.lastLogin)}</span>
              <span>更新于：{fmtTime(editRow.updatedAt)}</span>
              <span>注册于：{fmtTime(editRow.createdAt)}</span>
              <span>上次改密：{fmtTime(editRow.lastPasswordChange)}</span>
            </div>
          </div>
        ) : null}
      </Modal>

      <Modal
        title="调整剩余额度"
        visible={quotaModalOpen}
        onOk={applyQuotaModal}
        onCancel={() => setQuotaModalOpen(false)}
        unmountOnExit
      >
        <Text type="secondary" className="!mb-2 !block text-[12px]">
          请输入美元金额，最多 6 位小数（与列表展示一致，内部存储为整数 ×10⁶）。
        </Text>
        <Input
          value={quotaDollarInput}
          onChange={setQuotaDollarInput}
          placeholder="例如 200.000000"
          prefix="$"
          className="font-mono"
        />
      </Modal>
    </div>
  )
}
