import type { ComponentType, ReactNode } from 'react'
import { useState } from 'react'
import { Avatar, Button, Dropdown, Menu, Space, Tooltip } from '@arco-design/web-react'
import { Key, LogOut, Megaphone, Moon, Sun, User } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { AnnouncementsModal } from '@/components/announcements/AnnouncementsModal'
import { logoutSession } from '@/api/auth'
import { useAuthStore } from '@/stores/authStore'
import { useColorModeStore } from '@/stores/colorMode'
import { useUiStore } from '@/stores/ui'
import { cn } from '@/lib/cn'
import { SIDEBAR_WIDTH } from '@/constants/layout'

function roleLabel(role: string): string {
  const r = (role || '').toLowerCase()
  if (r === 'superadmin') return '超级管理员'
  if (r === 'admin') return '管理员'
  return '用户'
}

function menuIcon(Icon: ComponentType<{ size?: number; strokeWidth?: number; className?: string }>) {
  return (
    <Icon size={16} strokeWidth={1.75} className="text-[var(--color-text-2)]" />
  )
}

/** 收起侧栏：下拉与主导航一致，仅图标 + 右侧 Tooltip 文案 */
function iconOnlyNavBtn(
  label: string,
  icon: ReactNode,
  onClick: () => void,
  opts?: { danger?: boolean },
) {
  return (
    <Tooltip content={label} position="right" mini>
      <Button
        type="text"
        size="mini"
        className={cn(
          '!flex !h-8 !w-8 !min-w-8 !items-center !justify-center !p-0',
          opts?.danger && '!text-[rgb(var(--danger-6))]',
        )}
        aria-label={label}
        icon={icon}
        onClick={onClick}
      />
    </Tooltip>
  )
}

export function SidebarUserBar() {
  const navigate = useNavigate()
  const authUser = useAuthStore((s) => s.user)
  const displayName =
    (authUser?.displayName && String(authUser.displayName).trim()) ||
    authUser?.email ||
    '未登录'
  const emailInitial = (authUser?.email?.[0] ?? '?').toUpperCase()
  const accountTitleHint =
    authUser != null
      ? `${authUser.email} · ${roleLabel(authUser.role)}${authUser.status ? ` · ${authUser.status}` : ''}`
      : undefined
  const clearUser = useAuthStore((s) => s.clearUser)
  const collapsed = useUiStore((s) => s.sidebarCollapsed)
  const [announcementsOpen, setAnnouncementsOpen] = useState(false)

  const doLogout = () => {
    void (async () => {
      try {
        await logoutSession()
      } catch {
        /* still clear local session */
      }
      clearUser()
      navigate('/login', { replace: true })
    })()
  }
  const mode = useColorModeStore((s) => s.mode)
  const toggleMode = useColorModeStore((s) => s.toggleMode)
  const menuWidth = SIDEBAR_WIDTH

  const droplistExpanded = (
    <div
      className="sidebar-user-droplist-wrap"
      style={{ width: menuWidth, boxSizing: 'border-box' }}
    >
      <Menu
        className="sidebar-user-dropdown-menu"
        style={{ width: '100%', boxSizing: 'border-box' }}
        onClickMenuItem={(key) => {
          if (key === 'profile') navigate('/profile')
          else if (key === 'credential') navigate('/credential')
          else if (key === 'announcements') setAnnouncementsOpen(true)
          else if (key === 'logout') doLogout()
        }}
      >
        <Menu.Item key="profile">
          <span className="inline-flex items-center gap-2">
            {menuIcon(User)}
            个人中心
          </span>
        </Menu.Item>
        <Menu.Item key="credential">
          <span className="inline-flex items-center gap-2">
            {menuIcon(Key)}
            密钥与凭证
          </span>
        </Menu.Item>
        <Menu.Item key="logout">
          <span className="inline-flex items-center gap-2 text-[rgb(var(--danger-6))]">
            <LogOut
              size={16}
              strokeWidth={1.75}
              className="shrink-0 text-[rgb(var(--danger-6))]"
            />
            退出登录
          </span>
        </Menu.Item>
      </Menu>
    </div>
  )

  const collapsedNavItems: {
    label: string
    icon: ReactNode
    onClick: () => void
    danger?: boolean
  }[] = [
    { label: '个人中心', icon: menuIcon(User), onClick: () => navigate('/profile') },
    { label: '密钥与凭证', icon: menuIcon(Key), onClick: () => navigate('/credential') },
    { label: '站点公告', icon: menuIcon(Megaphone), onClick: () => setAnnouncementsOpen(true) },
    {
      label: '退出登录',
      icon: <LogOut size={16} strokeWidth={1.75} className="text-[rgb(var(--danger-6))]" />,
      onClick: doLogout,
      danger: true,
    },
  ]

  const droplistCollapsed = (
    <div
      className="sidebar-user-droplist-icon-only box-border rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-1 shadow-md"
      style={{ width: 40, boxSizing: 'border-box' }}
    >
      <Space direction="vertical" size={4} className="!w-full !items-center">
        {collapsedNavItems.map((row) => (
          <span key={row.label} className="flex justify-center">
            {iconOnlyNavBtn(row.label, row.icon, row.onClick, { danger: row.danger })}
          </span>
        ))}
      </Space>
    </div>
  )

  const themeBtn = (
    <Button
      type="text"
      size="mini"
      className="!h-7 !min-w-7 !shrink-0 !px-0"
      aria-label={mode === 'dark' ? '切换为亮色' : '切换为暗色'}
      icon={
        mode === 'dark' ? (
          <Sun size={15} strokeWidth={1.75} className="text-[var(--color-text-2)]" />
        ) : (
          <Moon size={15} strokeWidth={1.75} className="text-[var(--color-text-2)]" />
        )
      }
      onClick={(e) => {
        e.stopPropagation()
        toggleMode()
      }}
    />
  )

  const announcementsBtn = (
    <Tooltip content="站点公告" position="top" mini>
      <Button
        type="text"
        size="mini"
        className="!h-7 !min-w-7 !shrink-0 !px-0"
        aria-label="站点公告"
        icon={<Megaphone size={15} strokeWidth={1.75} className="text-[var(--color-text-2)]" />}
        onClick={() => setAnnouncementsOpen(true)}
      />
    </Tooltip>
  )

  const triggerClass = cn(
    'flex cursor-pointer items-center rounded-md outline-none transition-colors',
    'border-none bg-transparent text-[var(--color-text-2)] hover:bg-[var(--color-fill-2)] hover:text-[var(--color-text-1)]',
    collapsed ? 'justify-center p-0' : 'min-w-0 w-full gap-2 px-0.5 py-0.5',
  )

  return (
    <div className="sidebar-user shrink-0 border-t border-[var(--color-border-2)] bg-[var(--color-bg-2)]">
      <AnnouncementsModal visible={announcementsOpen} onClose={() => setAnnouncementsOpen(false)} />
      {collapsed ? (
        <div className="flex w-full flex-col items-center gap-1 py-1.5">
          <Dropdown
            droplist={droplistCollapsed}
            trigger="click"
            position="tr"
            triggerProps={{ className: 'flex w-full justify-center' }}
          >
            <button
              type="button"
              className={triggerClass}
              aria-label="账户菜单"
              title={accountTitleHint}
            >
              <div className="sidebar-collapsed-icon-rail">
                <Avatar
                  size={24}
                  shape="circle"
                  className="shrink-0 !bg-[var(--color-primary-light-2)] !text-[var(--color-primary-6)]"
                >
                  {authUser?.avatar ? (
                    <img src={authUser.avatar} alt="" className="h-full w-full object-cover" />
                  ) : (
                    <span className="text-[10px] font-semibold">{emailInitial}</span>
                  )}
                </Avatar>
              </div>
            </button>
          </Dropdown>
        </div>
      ) : (
        <div className="flex min-h-[40px] items-center gap-1 px-1.5 py-1.5">
          <div className="min-w-0 flex-1 self-stretch">
            <Dropdown droplist={droplistExpanded} trigger="click" position="top">
              <button
                type="button"
                className={triggerClass}
                aria-label="账户菜单"
                title={accountTitleHint}
              >
                <Avatar
                  size={28}
                  shape="circle"
                  className="shrink-0 !bg-[var(--color-primary-light-2)] !text-[var(--color-primary-6)]"
                >
                  {authUser?.avatar ? (
                    <img src={authUser.avatar} alt="" className="h-full w-full object-cover" />
                  ) : (
                    <span className="text-[11px] font-semibold">{emailInitial}</span>
                  )}
                </Avatar>
                <div className="min-w-0 flex-1 text-left leading-tight">
                  <div className="truncate text-[12px] font-medium text-[var(--color-text-1)]">
                    {displayName}
                  </div>
                  {authUser ? (
                    <div className="truncate text-[11px] text-[var(--color-text-3)]">
                      {roleLabel(authUser.role)}
                      {authUser.status ? ` · ${authUser.status}` : ''}
                    </div>
                  ) : null}
                </div>
              </button>
            </Dropdown>
          </div>
          <div className="flex shrink-0 items-center gap-0.5 self-center">
            {themeBtn}
            {announcementsBtn}
          </div>
        </div>
      )}
    </div>
  )
}
