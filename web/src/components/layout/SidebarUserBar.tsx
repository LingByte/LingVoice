import type { ReactNode } from 'react'
import { Avatar, Button, Dropdown, Menu, Space, Tooltip } from '@arco-design/web-react'
import { Database, Key, LogOut, Moon, Settings, Sun, User } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { useColorModeStore } from '@/stores/colorMode'
import { useUiStore } from '@/stores/ui'
import { cn } from '@/lib/cn'
import { SIDEBAR_WIDTH } from '@/constants/layout'

/** 占位展示名，接入登录态后替换 */
const DISPLAY_NAME = 'wechat-o5Lj…'

function menuIcon(Icon: typeof User) {
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
  const collapsed = useUiStore((s) => s.sidebarCollapsed)
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
          else if (key === 'quotas') navigate('/quotas')
          else if (key === 'logout') navigate('/login')
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
        <Menu.Item key="quotas">
          <span className="inline-flex items-center gap-2">
            {menuIcon(Database)}
            配额管理
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
    { label: '配额管理', icon: menuIcon(Database), onClick: () => navigate('/quotas') },
    {
      label: '退出登录',
      icon: <LogOut size={16} strokeWidth={1.75} className="text-[rgb(var(--danger-6))]" />,
      onClick: () => navigate('/login'),
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

  const settingsBtn = (
    <Button
      type="text"
      size="mini"
      className="!h-7 !min-w-7 !shrink-0 !px-0"
      aria-label="设置"
      icon={<Settings size={15} strokeWidth={1.75} className="text-[var(--color-text-2)]" />}
      onClick={() => navigate('/settings')}
    />
  )

  const triggerClass = cn(
    'flex cursor-pointer items-center rounded-md outline-none transition-colors',
    'border-none bg-transparent text-[var(--color-text-2)] hover:bg-[var(--color-fill-2)] hover:text-[var(--color-text-1)]',
    collapsed ? 'justify-center p-0' : 'min-w-0 w-full gap-2 px-0.5 py-0.5',
  )

  return (
    <div className="sidebar-user shrink-0 border-t border-[var(--color-border-2)] bg-[var(--color-bg-2)]">
      {collapsed ? (
        <div className="flex w-full flex-col items-center py-1.5">
          <Dropdown
            droplist={droplistCollapsed}
            trigger="click"
            position="tr"
            triggerProps={{ className: 'flex w-full justify-center' }}
          >
            <button type="button" className={triggerClass} aria-label="账户菜单">
              <div className="sidebar-collapsed-icon-rail">
                <Avatar size={24} shape="circle" className="shrink-0">
                  <img src="/logo.png" alt="" className="h-full w-full object-cover" />
                </Avatar>
              </div>
            </button>
          </Dropdown>
        </div>
      ) : (
        <div className="flex min-h-[40px] items-center gap-1 px-1.5 py-1.5">
          <div className="min-w-0 flex-1 self-stretch">
            <Dropdown droplist={droplistExpanded} trigger="click" position="top">
              <button type="button" className={triggerClass} aria-label="账户菜单">
                <Avatar size={28} shape="circle" className="shrink-0">
                  <img src="/logo.png" alt="" className="h-full w-full object-cover" />
                </Avatar>
                <span className="min-w-0 flex-1 truncate text-left text-[12px] font-medium leading-normal text-[var(--color-text-1)]">
                  {DISPLAY_NAME}
                </span>
              </button>
            </Dropdown>
          </div>
          <div className="flex shrink-0 items-center gap-0.5 self-center">
            {themeBtn}
            {settingsBtn}
          </div>
        </div>
      )}
    </div>
  )
}
