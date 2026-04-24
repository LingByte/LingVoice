import { Avatar, Dropdown, Menu, Typography } from '@arco-design/web-react'
import { Database, Key, LogOut, Settings, User } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { useUiStore } from '@/stores/ui'
import { cn } from '@/lib/cn'
import { SIDEBAR_WIDTH } from '@/constants/layout'

const { Text } = Typography

/** 占位展示名，接入登录态后替换 */
const DISPLAY_NAME = 'wechat-o5Lj…'

function menuIcon(Icon: typeof User) {
  return (
    <Icon size={16} strokeWidth={1.75} className="text-[var(--color-text-2)]" />
  )
}

export function SidebarUserBar() {
  const navigate = useNavigate()
  const collapsed = useUiStore((s) => s.sidebarCollapsed)
  /** 与展开侧栏同宽，收起时侧栏变窄仍用该宽度保证菜单可读 */
  const menuWidth = SIDEBAR_WIDTH

  const droplist = (
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

  return (
    <div className="sidebar-user shrink-0 border-t border-[var(--color-border-2)] bg-[var(--color-bg-2)] px-1.5 py-1.5">
      <Dropdown droplist={droplist} trigger="click" position="top">
        <div
          role="button"
          tabIndex={0}
          className={cn(
            'sidebar-user__trigger group flex w-full cursor-pointer items-center gap-1.5 rounded-md px-0.5 py-0.5 outline-none transition-colors',
            'text-[var(--color-text-2)] hover:bg-[var(--color-fill-2)] hover:text-[var(--color-text-1)]',
            collapsed && 'justify-center',
          )}
          onKeyDown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault()
              e.currentTarget.click()
            }
          }}
        >
          <Avatar size={28} shape="circle" className="shrink-0">
            <img src="/logo.png" alt="" className="h-full w-full object-cover" />
          </Avatar>
          {!collapsed && (
            <>
              <Text
                ellipsis
                className="min-w-0 flex-1 text-left text-[12px] font-medium text-[var(--color-text-1)]"
              >
                {DISPLAY_NAME}
              </Text>
              <span
                className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-[var(--color-fill-2)] text-[var(--color-text-2)] transition-colors group-hover:bg-[var(--color-fill-3)] group-hover:text-[var(--color-text-1)]"
                aria-hidden
              >
                <Settings size={15} strokeWidth={1.75} />
              </span>
            </>
          )}
        </div>
      </Dropdown>
    </div>
  )
}
