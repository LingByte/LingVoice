import { useEffect, useState } from 'react'
import { Avatar, Button, Card, Form, Input, Message, Progress, Select, Tag, Typography, Upload } from '@arco-design/web-react'
import { Key, LogOut, Pencil, Save, Settings, Shield, User, X } from 'lucide-react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { logoutSession, type AuthUser, updateUserProfile, uploadUserAvatar } from '@/api/auth'
import { ProfileSettingsSection } from '@/components/profile/ProfileSettingsSection'
import { useAuthStore } from '@/stores/authStore'
import { cn } from '@/lib/cn'

const { Title, Text } = Typography
const { Option } = Select

function fmtTime(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

function roleLabel(role: string): string {
  const r = (role || '').toLowerCase()
  if (r === 'superadmin') return '超级管理员'
  if (r === 'admin') return '管理员'
  return '用户'
}

function roleColor(role: string): string {
  const r = (role || '').toLowerCase()
  if (r === 'superadmin') return 'magenta'
  if (r === 'admin') return 'orangered'
  return 'arcoblue'
}

function statusLabel(status?: string): string {
  if (!status) return '—'
  const s = status.toLowerCase()
  if (s === 'active') return '活跃'
  if (s === 'inactive') return '未激活'
  if (s === 'suspended') return '已停用'
  if (s === 'banned') return '已封禁'
  return status
}

function genderLabel(gender?: string): string {
  if (!gender) return '—'
  const g = gender.toLowerCase()
  if (g === 'male') return '男'
  if (g === 'female') return '女'
  if (g === 'other') return '其他'
  return gender
}

function fmtQuota(n?: number, unlimited?: boolean): string {
  if (unlimited) return '无限制'
  if (n == null || n === 0) return '0'
  if (n >= 1_000_000) return `${(n / 1e6).toFixed(2)}M`
  if (n >= 1_000) return `${(n / 1e3).toFixed(2)}K`
  return String(n)
}

const TIMEZONES = [
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Asia/Seoul',
  'Asia/Singapore',
  'Asia/Hong_Kong',
  'Asia/Dubai',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'Europe/Moscow',
  'America/New_York',
  'America/Los_Angeles',
  'America/Chicago',
  'Australia/Sydney',
  'UTC',
]

const LOCALES = [
  { value: 'zh-CN', label: '简体中文' },
  { value: 'zh-TW', label: '繁體中文' },
  { value: 'en-US', label: 'English' },
  { value: 'ja-JP', label: '日本語' },
  { value: 'ko-KR', label: '한국어' },
]

const GENDER_OPTIONS = [
  { value: 'male', label: '男' },
  { value: 'female', label: '女' },
  { value: 'other', label: '其他' },
]

type NavKey = 'profile' | 'settings' | 'logout'

const NAV_PROFILE: { key: 'profile'; label: string; icon: typeof User } = {
  key: 'profile',
  label: '个人信息',
  icon: User,
}

const NAV_SETTINGS: { key: 'settings'; label: string; icon: typeof Settings } = {
  key: 'settings',
  label: '偏好设置',
  icon: Settings,
}

const NAV_LOGOUT: { key: 'logout'; label: string; icon: typeof LogOut } = {
  key: 'logout',
  label: '登出',
  icon: LogOut,
}

export function ProfilePage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const authUser = useAuthStore((s) => s.user)
  const setUser = useAuthStore((s) => s.setUser)
  const [activeKey, setActiveKey] = useState<NavKey>(() =>
    searchParams.get('tab') === 'settings' ? 'settings' : 'profile',
  )
  const [isEditing, setIsEditing] = useState(false)
  const [loading, setLoading] = useState(false)

  const [form] = Form.useForm()

  useEffect(() => {
    const t = searchParams.get('tab')
    setActiveKey(t === 'settings' ? 'settings' : 'profile')
  }, [searchParams])

  useEffect(() => {
    if (authUser) {
      form.setFieldsValue({
        displayName: authUser.displayName || '',
        firstName: authUser.firstName || '',
        lastName: authUser.lastName || '',
        gender: authUser.gender || '',
        city: authUser.city || '',
        region: authUser.region || '',
        timezone: authUser.timezone || 'Asia/Shanghai',
        locale: authUser.locale || 'zh-CN',
      })
    }
  }, [authUser, form])

  const goProfile = () => {
    setActiveKey('profile')
    setSearchParams({}, { replace: true })
  }

  const goSettings = () => {
    setActiveKey('settings')
    setSearchParams({ tab: 'settings' }, { replace: true })
  }

  const handleNav = (key: NavKey) => {
    if (key === 'logout') {
      void (async () => {
        try {
          await logoutSession()
        } catch {
          /* ignore */
        }
        useAuthStore.getState().clearUser()
        navigate('/login', { replace: true })
      })()
      return
    }
    if (key === 'settings') {
      goSettings()
      return
    }
    goProfile()
  }

  const handleEdit = () => {
    setIsEditing(true)
  }

  const handleCancel = () => {
    setIsEditing(false)
    if (authUser) {
      form.setFieldsValue({
        displayName: authUser.displayName || '',
        firstName: authUser.firstName || '',
        lastName: authUser.lastName || '',
        gender: authUser.gender || '',
        city: authUser.city || '',
        region: authUser.region || '',
        timezone: authUser.timezone || 'Asia/Shanghai',
        locale: authUser.locale || 'zh-CN',
      })
    }
  }

  const handleSave = async () => {
    try {
      const values = await form.validate()
      setLoading(true)
      const updatedUser = await updateUserProfile({
        displayName: values.displayName || undefined,
        firstName: values.firstName || undefined,
        lastName: values.lastName || undefined,
        gender: values.gender || undefined,
        city: values.city || undefined,
        region: values.region || undefined,
        locale: values.locale,
        timezone: values.timezone,
      })
      setUser(updatedUser)
      Message.success('保存成功')
      setIsEditing(false)
    } catch (error) {
      console.error('Save failed:', error)
      Message.error('保存失败: ' + (error as Error).message)
    } finally {
      setLoading(false)
    }
  }

  const handleAvatarUpload = async (file: File) => {
    try {
      const url = await uploadUserAvatar(file)
      if (authUser) {
        const updatedUser: AuthUser = {
          ...authUser,
          avatar: url,
        }
        setUser(updatedUser)
      }
      Message.success('头像上传成功')
      return true
    } catch (error) {
      console.error('Avatar upload failed:', error)
      Message.error('头像上传失败: ' + (error as Error).message)
      return false
    }
  }

  const emailInitial = (authUser?.email?.[0] ?? '?').toUpperCase()
  const displayName =
    (authUser?.displayName && String(authUser.displayName).trim()) ||
    authUser?.email ||
    '未登录'

  const showSource = authUser?.source && authUser.source !== 'SYSTEM'

  return (
    <div className="profile-shell flex h-full min-h-0 w-full flex-1 bg-[var(--color-bg-2)]">
      <aside className="profile-shell__nav flex h-full min-h-0 w-[220px] shrink-0 flex-col border-r border-[var(--color-border-2)] bg-[var(--color-bg-1)]">
        <div className="profile-nav flex min-h-0 flex-1 flex-col">
          <nav
            className="profile-nav__list min-h-0 flex-1 overflow-x-hidden overflow-y-auto"
            aria-label="个人中心"
          >
            <button
              type="button"
              onClick={() => handleNav('profile')}
              className={cn(
                'profile-nav__item',
                activeKey === 'profile' && 'profile-nav__item--active',
              )}
            >
              <span className="profile-nav__icon" aria-hidden>
                <NAV_PROFILE.icon size={16} strokeWidth={1.85} />
              </span>
              <span className="profile-nav__label">{NAV_PROFILE.label}</span>
            </button>
            <button
              type="button"
              onClick={() => handleNav('settings')}
              className={cn(
                'profile-nav__item',
                activeKey === 'settings' && 'profile-nav__item--active',
              )}
            >
              <span className="profile-nav__icon" aria-hidden>
                <NAV_SETTINGS.icon size={16} strokeWidth={1.85} />
              </span>
              <span className="profile-nav__label">{NAV_SETTINGS.label}</span>
            </button>
          </nav>
          <div className="profile-nav__sep" role="presentation" />
          <button
            type="button"
            className="profile-nav__item profile-nav__item--danger"
            onClick={() => handleNav('logout')}
          >
            <span className="profile-nav__icon" aria-hidden>
              <NAV_LOGOUT.icon size={16} strokeWidth={1.85} />
            </span>
            <span className="profile-nav__label">{NAV_LOGOUT.label}</span>
          </button>
        </div>
      </aside>

      <div className="profile-shell__main flex h-full min-h-0 min-w-0 flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
        {activeKey === 'settings' ? (
          <>
            <Title heading={5} className="!mb-1 !mt-0 shrink-0">
              偏好设置
            </Title>
            <Text type="secondary" className="!mb-6 block text-[13px]">
              原独立「设置」页已合并至个人中心；主题仍可在侧栏底部快速切换。
            </Text>
            <ProfileSettingsSection />
          </>
        ) : (
          <div className="mx-auto flex w-full max-w-[960px] min-w-0 flex-col gap-5">
          {/* 个人资料头部 */}
          <div className="relative overflow-hidden rounded-xl bg-[var(--color-bg-1)] shadow-sm">
            {/* 渐变背景 */}
            <div
              className="pointer-events-none absolute inset-0"
              style={{
                background:
                  'linear-gradient(135deg, rgb(var(--primary-1)) 0%, rgb(var(--primary-2)) 50%, rgba(var(--primary-3), 0.3) 100%)',
              }}
            />
            <div className="pointer-events-none absolute -right-12 -top-12 h-40 w-40 rounded-full bg-[rgb(var(--primary-3))] opacity-15 blur-2xl" />
            <div className="pointer-events-none absolute -left-6 bottom-0 h-24 w-24 rounded-full bg-[rgb(var(--primary-2))] opacity-20 blur-xl" />

            <div className="relative flex items-center justify-between px-6 py-6">
              <div className="flex items-center gap-5">
                <Upload
                  customRequest={async ({ file }) => {
                    const success = await handleAvatarUpload(file as File)
                    return success
                  }}
                  showUploadList={false}
                  accept="image/*"
                >
                  <Avatar
                    size={72}
                    shape="circle"
                    className="shrink-0 !bg-[var(--color-primary-light-2)] !text-[var(--color-primary-6)] !text-[28px] !font-bold ring-2 ring-white/20 cursor-pointer hover:ring-4 transition-all"
                  >
                    {authUser?.avatar ? (
                      <img src={authUser.avatar} alt="" className="h-full w-full object-cover" />
                    ) : (
                      <span className="text-[28px] font-bold">{emailInitial}</span>
                    )}
                  </Avatar>
                </Upload>
                <div className="min-w-0 flex-1">
                  <div className="mb-1 flex flex-wrap items-center gap-2">
                    <Title heading={5} className="!mb-0 !mt-0 !text-[20px] !font-semibold text-[var(--color-text-1)]">
                      {displayName}
                    </Title>
                    {authUser ? (
                      <Tag size="small" color={roleColor(authUser.role)}>
                        {roleLabel(authUser.role)}
                      </Tag>
                    ) : null}
                    {authUser?.status ? (
                      <Tag size="small" className="bg-[var(--color-fill-2)] text-[var(--color-text-2)]">
                        {statusLabel(authUser.status)}
                      </Tag>
                    ) : null}
                  </div>
                  <Text type="secondary" className="block text-[13px]">
                    {authUser?.email ?? '—'}
                  </Text>
                  <div className="mt-2 flex flex-wrap items-center gap-3 text-[12px]">
                    {authUser?.emailVerified ? (
                      <span className="inline-flex items-center gap-1 text-[rgb(var(--success-6))]">
                        <Shield size={12} /> 邮箱已验证
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-[var(--color-text-3)]">
                        <Shield size={12} /> 邮箱未验证
                      </span>
                    )}
                    {authUser?.phoneVerified ? (
                      <span className="inline-flex items-center gap-1 text-[rgb(var(--success-6))]">
                        <Shield size={12} /> 手机已验证
                      </span>
                    ) : authUser ? (
                      <span className="inline-flex items-center gap-1 text-[var(--color-text-3)]">
                        <Shield size={12} /> 手机未验证
                      </span>
                    ) : null}
                    {showSource ? (
                      <span className="text-[var(--color-text-3)]">来源: {authUser.source}</span>
                    ) : null}
                  </div>
                </div>
              </div>
              {!isEditing && (
                <Button type="primary" size="small" icon={<Pencil size={14} />} onClick={handleEdit}>
                  编辑
                </Button>
              )}
            </div>
          </div>

          {/* 配额信息 */}
          {authUser ? (
            <div className="grid grid-cols-3 gap-4">
              <div className="rounded-xl bg-[var(--color-bg-1)] p-4 shadow-sm">
                <div className="mb-1 flex items-center gap-1.5">
                  <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-[rgb(var(--primary-1))]">
                    <Key size={14} className="text-[rgb(var(--primary-6))]" />
                  </div>
                  <Text type="secondary" className="text-[12px]">剩余配额</Text>
                </div>
                <Text bold className="block text-[18px] text-[var(--color-text-1)]">
                  {fmtQuota(authUser.remainQuota, authUser.unlimitedQuota)}
                </Text>
              </div>
              <div className="rounded-xl bg-[var(--color-bg-1)] p-4 shadow-sm">
                <div className="mb-1 flex items-center gap-1.5">
                  <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-[rgba(var(--warning-1))]">
                    <Key size={14} className="text-[rgb(var(--warning-6))]" />
                  </div>
                  <Text type="secondary" className="text-[12px]">已用配额</Text>
                </div>
                <Text bold className="block text-[18px] text-[var(--color-text-1)]">
                  {fmtQuota(authUser.usedQuota)}
                </Text>
              </div>
              <div className="rounded-xl bg-[var(--color-bg-1)] p-4 shadow-sm">
                <div className="mb-1 flex items-center gap-1.5">
                  <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-[rgba(var(--success-1))]">
                    <User size={14} className="text-[rgb(var(--success-6))]" />
                  </div>
                  <Text type="secondary" className="text-[12px]">资料完整度</Text>
                </div>
                <div className="flex items-center gap-3">
                  <Text bold className="text-[18px] text-[var(--color-text-1)]">
                    {authUser.profileComplete}%
                  </Text>
                  <Progress
                    percent={authUser.profileComplete}
                    size="small"
                    className="flex-1"
                    color={authUser.profileComplete >= 80 ? 'rgb(var(--success-6))' : authUser.profileComplete >= 50 ? 'rgb(var(--warning-6))' : 'rgb(var(--danger-6))'}
                  />
                </div>
              </div>
            </div>
          ) : null}

          {/* 详细信息 */}
          <Card
            title="账号详情"
            bordered={false}
            className="w-full min-w-0 shadow-sm"
            extra={
              isEditing ? (
                <div className="flex gap-2">
                  <Button size="small" icon={<X size={14} />} onClick={handleCancel}>
                    取消
                  </Button>
                  <Button type="primary" size="small" icon={<Save size={14} />} loading={loading} onClick={handleSave}>
                    保存
                  </Button>
                </div>
              ) : null
            }
          >
            {isEditing ? (
              <Form layout="vertical" form={form} size="small">
                <Form.Item label="显示名" field="displayName" rules={[{ required: false }]}>
                  <Input placeholder="输入显示名" maxLength={50} />
                </Form.Item>
                <div className="grid grid-cols-2 gap-4">
                  <Form.Item label="名" field="firstName" rules={[{ required: false }]}>
                    <Input placeholder="名" maxLength={50} />
                  </Form.Item>
                  <Form.Item label="姓" field="lastName" rules={[{ required: false }]}>
                    <Input placeholder="姓" maxLength={50} />
                  </Form.Item>
                </div>
                <Form.Item label="性别" field="gender" rules={[{ required: false }]}>
                  <Select placeholder="选择性别">
                    {GENDER_OPTIONS.map((opt) => (
                      <Option key={opt.value} value={opt.value}>
                        {opt.label}
                      </Option>
                    ))}
                  </Select>
                </Form.Item>
                <div className="grid grid-cols-2 gap-4">
                  <Form.Item label="城市" field="city" rules={[{ required: false }]}>
                    <Input placeholder="城市" maxLength={50} />
                  </Form.Item>
                  <Form.Item label="地区" field="region" rules={[{ required: false }]}>
                    <Input placeholder="地区" maxLength={50} />
                  </Form.Item>
                </div>
                <div className="grid grid-cols-2 gap-4">
                  <Form.Item label="语言" field="locale" rules={[{ required: true }]}>
                    <Select placeholder="选择语言">
                      {LOCALES.map((opt) => (
                        <Option key={opt.value} value={opt.value}>
                          {opt.label}
                        </Option>
                      ))}
                    </Select>
                  </Form.Item>
                  <Form.Item label="时区" field="timezone" rules={[{ required: true }]}>
                    <Select placeholder="选择时区">
                      {TIMEZONES.map((tz) => (
                        <Option key={tz} value={tz}>
                          {tz}
                        </Option>
                      ))}
                    </Select>
                  </Form.Item>
                </div>
              </Form>
            ) : (
              <div className="space-y-0 text-[13px]">
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">账号 ID</Text>
                  <Text className="font-mono text-[12px]">{authUser?.id ?? '—'}</Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">邮箱</Text>
                  <Text className="max-w-[60%] truncate text-right">
                    {authUser?.email ?? '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">显示名</Text>
                  <Text className="max-w-[60%] truncate text-right">
                    {authUser?.displayName || '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">姓名</Text>
                  <Text className="max-w-[60%] truncate text-right">
                    {[authUser?.firstName, authUser?.lastName].filter(Boolean).join(' ') || '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">性别</Text>
                  <Text className="max-w-[60%] truncate text-right">
                    {genderLabel(authUser?.gender)}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">城市</Text>
                  <Text className="max-w-[60%] truncate text-right">
                    {authUser?.city || '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">地区</Text>
                  <Text className="max-w-[60%] truncate text-right">
                    {authUser?.region || '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">角色</Text>
                  <Text>{authUser ? roleLabel(authUser.role) : '—'}</Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">状态</Text>
                  <Text>{statusLabel(authUser?.status)}</Text>
                </div>
                {showSource ? (
                  <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                    <Text type="secondary">来源</Text>
                    <Text>{authUser.source}</Text>
                  </div>
                ) : null}
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">语言 / 时区</Text>
                  <Text className="max-w-[55%] truncate text-right">
                    {[authUser?.locale, authUser?.timezone].filter(Boolean).join(' · ') || '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">登录次数</Text>
                  <Text>{authUser?.loginCount ?? '—'}</Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">注册时间</Text>
                  <Text className="max-w-[55%] text-right text-[12px]">
                    {fmtTime(authUser?.createdAt)}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">上次登录</Text>
                  <Text className="max-w-[55%] text-right text-[12px]">
                    {fmtTime(authUser?.lastLogin)}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2.5">
                  <Text type="secondary">密码</Text>
                  <div className="flex items-center gap-2">
                    <Text>••••••••</Text>
                    <Button type="text" size="mini">
                      变更
                    </Button>
                  </div>
                </div>
              </div>
            )}
          </Card>
        </div>
        )}
      </div>
    </div>
  )
}
