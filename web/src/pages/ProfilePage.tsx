import { useCallback, useEffect, useState } from 'react'
import { Button, Card, Typography } from '@arco-design/web-react'
import { LogOut, Settings, User } from 'lucide-react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { logoutSession } from '@/api/auth'
import { ProfileSettingsSection } from '@/components/profile/ProfileSettingsSection'
import { cn } from '@/lib/cn'
import { useAuthStore } from '@/stores/authStore'

const { Title, Text } = Typography

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
  const [activeKey, setActiveKey] = useState<NavKey>(() =>
    searchParams.get('tab') === 'settings' ? 'settings' : 'profile',
  )
  const LogoutIcon = NAV_LOGOUT.icon

  useEffect(() => {
    const t = searchParams.get('tab')
    setActiveKey(t === 'settings' ? 'settings' : 'profile')
  }, [searchParams])

  const goProfile = useCallback(() => {
    setActiveKey('profile')
    setSearchParams({}, { replace: true })
  }, [setSearchParams])

  const goSettings = useCallback(() => {
    setActiveKey('settings')
    setSearchParams({ tab: 'settings' }, { replace: true })
  }, [setSearchParams])

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
              <LogoutIcon size={16} strokeWidth={1.85} />
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
          <>
            <Title heading={5} className="!mb-4 !mt-0 shrink-0">
              个人信息
            </Title>
            <Card title="通用信息" bordered={false} className="w-full min-w-0 shadow-sm">
              <div className="space-y-3 text-[13px]">
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">账号 ID</Text>
                  <Text>{authUser?.id ?? '—'}</Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">邮箱</Text>
                  <Text className="max-w-[60%] truncate text-right">
                    {authUser?.email ?? '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">显示名</Text>
                  <Text className="max-w-[60%] truncate text-right">
                    {authUser?.displayName ?? '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">姓名</Text>
                  <Text className="max-w-[60%] truncate text-right">
                    {[authUser?.firstName, authUser?.lastName].filter(Boolean).join(' ') || '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">角色</Text>
                  <Text>{authUser ? roleLabel(authUser.role) : '—'}</Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">状态</Text>
                  <Text>{authUser?.status ?? '—'}</Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">来源</Text>
                  <Text>{authUser?.source ?? '—'}</Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">语言 / 时区</Text>
                  <Text className="max-w-[55%] truncate text-right">
                    {[authUser?.locale, authUser?.timezone].filter(Boolean).join(' · ') || '—'}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">资料完整度</Text>
                  <Text>{authUser != null ? `${authUser.profileComplete}%` : '—'}</Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">验证</Text>
                  <Text>
                    {authUser == null
                      ? '—'
                      : `邮箱${authUser.emailVerified ? '已' : '未'}验证 · 手机${
                          authUser.phoneVerified ? '已' : '未'
                        }验证`}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">登录次数</Text>
                  <Text>{authUser?.loginCount ?? '—'}</Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">注册时间</Text>
                  <Text className="max-w-[55%] text-right text-[12px]">
                    {fmtTime(authUser?.createdAt)}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">上次登录</Text>
                  <Text className="max-w-[55%] text-right text-[12px]">
                    {fmtTime(authUser?.lastLogin)}
                  </Text>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">密码</Text>
                  <div className="flex items-center gap-2">
                    <Text>••••••••</Text>
                    <Button type="text" size="mini">
                      变更
                    </Button>
                  </div>
                </div>
                <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
                  <Text type="secondary">通知</Text>
                  <div className="flex items-center gap-2">
                    <Text>—</Text>
                    <Button type="text" size="mini">
                      变更
                    </Button>
                  </div>
                </div>
                <div className="flex justify-between gap-4 py-2">
                  <Text type="secondary">偏好设置</Text>
                  <Button type="text" size="mini" onClick={() => handleNav('settings')}>
                    主题与外观
                  </Button>
                </div>
              </div>
            </Card>
          </>
        )}
      </div>
    </div>
  )
}
