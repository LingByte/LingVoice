import { Button, Layout, Menu } from '@arco-design/web-react'
import { LayoutDashboard, PanelLeft, PanelLeftClose } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useUiStore } from '@/stores/ui'
import { SidebarUserBar } from '@/components/layout/SidebarUserBar'
import { SIDEBAR_COLLAPSED_WIDTH, SIDEBAR_WIDTH } from '@/constants/layout'

const { Sider } = Layout
const menu = [{ key: '/', label: '首页', icon: <LayoutDashboard size={16} /> }]

export function AppSidebar() {
  const navigate = useNavigate()
  const location = useLocation()
  const collapsed = useUiStore((s) => s.sidebarCollapsed)
  const setCollapsed = useUiStore((s) => s.setSidebarCollapsed)

  return (
    <Sider
      theme="light"
      collapsible
      collapsed={collapsed}
      onCollapse={setCollapsed}
      breakpoint="lg"
      width={SIDEBAR_WIDTH}
      collapsedWidth={SIDEBAR_COLLAPSED_WIDTH}
      trigger={null}
      className="!h-full !min-h-0 shrink-0 border-r border-[var(--color-border-2)]"
    >
      <div className="flex h-full min-h-0 flex-col">
        {!collapsed ? (
          <div className="sidebar-frame-brand flex min-h-[52px] shrink-0 items-center gap-3 border-b border-[var(--color-border-2)] px-4 py-3">
            <img
              src="/logo.png"
              alt="LingVoice"
              className="h-10 w-10 shrink-0 rounded-xl object-contain"
            />
            <div className="min-w-0 flex-1 truncate text-left text-[15px] font-semibold tracking-tight text-[var(--color-text-1)]">
              LingVoice
            </div>
            <Button
              type="text"
              size="mini"
              className="shrink-0 !h-8 !min-w-8 !px-0"
              aria-label="收起侧栏"
              icon={<PanelLeftClose size={16} />}
              onClick={() => setCollapsed(true)}
            />
          </div>
        ) : (
          <div className="sidebar-frame-brand flex shrink-0 flex-col items-center gap-2 border-b border-[var(--color-border-2)] px-1 py-3">
            <img
              src="/logo.png"
              alt="LingVoice"
              className="h-9 w-9 shrink-0 rounded-xl object-contain"
            />
            <Button
              type="text"
              size="mini"
              className="!h-8 !min-w-8 !px-0"
              aria-label="展开侧栏"
              icon={<PanelLeft size={16} />}
              onClick={() => setCollapsed(false)}
            />
          </div>
        )}
        <div className="min-h-0 flex-1 overflow-y-auto">
          <Menu
            selectedKeys={[location.pathname]}
            onClickMenuItem={(key) => navigate(key)}
            className="sidebar-frame-menu border-none"
          >
            {menu.map((item) => (
              <Menu.Item key={item.key}>
                <span className="inline-flex items-center gap-2 [&_svg]:h-4 [&_svg]:w-4">
                  {item.icon}
                  {item.label}
                </span>
              </Menu.Item>
            ))}
          </Menu>
        </div>
        <SidebarUserBar />
      </div>
    </Sider>
  )
}
