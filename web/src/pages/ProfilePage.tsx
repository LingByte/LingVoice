import { useState } from 'react'
import { Button, Card, Typography } from '@arco-design/web-react'
import { LogOut, User } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { cn } from '@/lib/cn'
import { useAuthStore } from '@/stores/authStore'

const { Title, Text } = Typography

type NavKey = 'profile' | 'logout'

const NAV_PROFILE: { key: 'profile'; label: string; icon: typeof User } = {
  key: 'profile',
  label: '个人信息',
  icon: User,
}

const NAV_LOGOUT: { key: 'logout'; label: string; icon: typeof LogOut } = {
  key: 'logout',
  label: '登出',
  icon: LogOut,
}

export function ProfilePage() {
  const navigate = useNavigate()
  const clearUser = useAuthStore((s) => s.clearUser)
  const [activeKey, setActiveKey] = useState<NavKey>('profile')
  const LogoutIcon = NAV_LOGOUT.icon

  const handleNav = (key: NavKey) => {
    if (key === 'logout') {
      clearUser()
      navigate('/login', { replace: true })
      return
    }
    setActiveKey(key)
  }

  return (
    <div className="profile-shell flex h-full min-h-0 w-full flex-1 bg-[var(--color-bg-2)]">
      <aside className="profile-shell__nav flex h-full min-h-0 w-[220px] shrink-0 flex-col border-r border-[var(--color-border-2)] bg-[var(--color-bg-1)]">
        <div className="profile-nav flex min-h-0 flex-1 flex-col">
          <nav
            className="profile-nav__list flex-1 min-h-0 overflow-x-hidden overflow-y-auto"
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
        <Title heading={5} className="!mb-4 !mt-0 shrink-0">
          个人信息
        </Title>
        <Card title="通用信息" bordered={false} className="w-full min-w-0 shadow-sm">
          <div className="space-y-3 text-[13px]">
            <div className="flex justify-between gap-4 border-b border-[var(--color-border-1)] py-2">
              <Text type="secondary">账号 ID</Text>
              <Text>—</Text>
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
            <div className="flex justify-between gap-4 py-2">
              <Text type="secondary">通知</Text>
              <div className="flex items-center gap-2">
                <Text>—</Text>
                <Button type="text" size="mini">
                  变更
                </Button>
              </div>
            </div>
          </div>
        </Card>
      </div>
    </div>
  )
}
