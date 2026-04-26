import { Button, Layout, Menu, Tooltip } from '@arco-design/web-react'
import {
  BarChart3,
  Bot,
  Braces,
  ClipboardList,
  Cpu,
  BookOpen,
  FileText,
  Info,
  LayoutDashboard,
  LayoutTemplate,
  Layers,
  MessageSquare,
  Mic,
  Mic2,
  PanelLeft,
  PanelLeftClose,
  RadioTower,
  ScrollText,
  Store,
  Users,
  Volume2,
} from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useColorModeStore } from '@/stores/colorMode'
import { useUiStore } from '@/stores/ui'
import { SidebarUserBar } from '@/components/layout/SidebarUserBar'
import { SIDEBAR_COLLAPSED_WIDTH, SIDEBAR_WIDTH } from '@/constants/layout'

const { Sider } = Layout

const menu = [
  {
    key: '/',
    label: '聊天',
    icon: <MessageSquare size={16} strokeWidth={1.85} />,
  },
  {
    key: '/dashboard',
    label: '数据面板',
    icon: <LayoutDashboard size={16} strokeWidth={1.85} />,
  },
  {
    key: '/docs',
    label: '文档',
    icon: <FileText size={16} strokeWidth={1.85} />,
  },
  {
    key: '/about',
    label: '关于',
    icon: <Info size={16} strokeWidth={1.85} />,
  },
  {
    key: '/notify/channels',
    label: '通知渠道',
    icon: <RadioTower size={16} strokeWidth={1.85} />,
  },
  {
      key: '/channels/llm',
      label: 'LLM 渠道',
      icon: <Cpu size={16} strokeWidth={1.85} />,
  },
  {
      key: '/channels/llm-abilities',
      label: 'LLM 能力',
      icon: <Layers size={16} strokeWidth={1.85} />,
  },
  {
      key: '/channels/llm-model-metas',
      label: '模型元数据',
      icon: <BookOpen size={16} strokeWidth={1.85} />,
  },
  {
      key: '/channels/llm-plaza',
      label: '模型广场',
      icon: <Store size={16} strokeWidth={1.85} />,
  },
  {
      key: '/channels/asr',
      label: 'ASR 渠道',
      icon: <Mic size={16} strokeWidth={1.85} />,
  },
  {
      key: '/channels/tts',
      label: 'TTS 渠道',
      icon: <Volume2 size={16} strokeWidth={1.85} />,
  },
  {
    key: '/notify/mail-templates',
    label: '邮件模版',
    icon: <LayoutTemplate size={16} strokeWidth={1.85} />,
  },
  {
    key: '/notify/mail-logs',
    label: '邮件日志',
    icon: <ScrollText size={16} strokeWidth={1.85} />,
  },
  {
    key: '/notify/llm-usage',
    label: 'LLM 用量',
    icon: <BarChart3 size={16} strokeWidth={1.85} />,
  },
  {
    key: '/notify/speech-usage',
    label: '语音用量',
    icon: <Mic2 size={16} strokeWidth={1.85} />,
  },
  {
    key: '/admin/agent-runs',
    label: 'Agent 运行',
    icon: <Bot size={16} strokeWidth={1.85} />,
  },
  {
    key: '/admin/users',
    label: '用户管理',
    icon: <Users size={16} strokeWidth={1.85} />,
  },
  {
    key: '/admin/announcements',
    label: '公告管理',
    icon: <ClipboardList size={16} strokeWidth={1.85} />,
  },
  {
    key: '/debug/v1',
    label: 'V1 网关调试',
    icon: <Braces size={16} strokeWidth={1.85} />,
  },
] as const

function menuPathSelected(pathname: string, itemKey: string): boolean {
  if (itemKey === '/dashboard') return pathname === '/dashboard' || pathname === '/quotas'
  if (itemKey === '/notify/channels') return pathname.startsWith('/notify/channels')
  if (itemKey === '/notify/mail-templates') return pathname.startsWith('/notify/mail-templates')
  if (itemKey === '/notify/llm-usage') return pathname === '/notify/llm-usage'
  if (itemKey === '/notify/speech-usage') return pathname === '/notify/speech-usage'
  if (itemKey === '/admin/agent-runs') return pathname === '/admin/agent-runs'
  if (itemKey === '/admin/users') return pathname === '/admin/users'
  if (itemKey === '/admin/announcements') return pathname === '/admin/announcements'
  if (itemKey === '/docs') return pathname === '/docs'
  if (itemKey === '/about') return pathname === '/about'
  if (itemKey === '/channels/llm') {
    return (
      pathname.startsWith('/channels/llm') &&
      !pathname.startsWith('/channels/llm-abilities') &&
      !pathname.startsWith('/channels/llm-model-metas') &&
      !pathname.startsWith('/channels/llm-plaza')
    )
  }
  if (itemKey === '/channels/llm-abilities') return pathname.startsWith('/channels/llm-abilities')
  if (itemKey === '/channels/llm-model-metas') return pathname.startsWith('/channels/llm-model-metas')
  if (itemKey === '/channels/llm-plaza') return pathname === '/channels/llm-plaza'
  if (itemKey === '/channels/asr') return pathname.startsWith('/channels/asr')
  if (itemKey === '/channels/tts') return pathname.startsWith('/channels/tts')
  if (itemKey === '/debug/v1') return pathname === '/debug/v1'
  return pathname === itemKey
}

/**
 * 收起态不用 Arco Menu：Menu 在 Layout.Sider 内会自动进入 arco-menu-collapse（48px 宽、
 * margin-right:100vw 藏字等），与自定义侧栏宽度/对齐冲突。收起改用 Tooltip + Button。
 */
export function AppSidebar() {
  const navigate = useNavigate()
  const location = useLocation()
  const collapsed = useUiStore((s) => s.sidebarCollapsed)
  const setCollapsed = useUiStore((s) => s.setSidebarCollapsed)
  const colorMode = useColorModeStore((s) => s.mode)

  return (
    <Sider
      theme={colorMode === 'dark' ? 'dark' : 'light'}
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
          <div className="sidebar-frame-brand sidebar-frame-brand--collapsed flex w-full shrink-0 flex-col items-center border-b border-[var(--color-border-2)]">
            <div className="sidebar-collapsed-icon-rail">
              <img
                src="/logo.png"
                alt="LingVoice"
                className="h-8 w-8 rounded-lg object-contain"
              />
            </div>
            <div className="sidebar-collapsed-icon-rail">
              <Button
                type="text"
                size="mini"
                className="!flex !h-8 !w-8 !min-w-8 !items-center !justify-center !p-0"
                aria-label="展开侧栏"
                icon={<PanelLeft size={16} strokeWidth={1.85} />}
                onClick={() => setCollapsed(false)}
              />
            </div>
          </div>
        )}

        <div className="min-h-0 flex-1 overflow-y-auto">
          {collapsed ? (
            <nav
              className="sidebar-collapsed-nav flex flex-col items-center gap-2 py-2"
              aria-label="主导航"
            >
              {menu.map((item) => {
                const selected = menuPathSelected(location.pathname, item.key)
                return (
                  <Tooltip key={item.key} content={item.label} position="right" mini>
                    <Button
                      type={selected ? 'secondary' : 'text'}
                      size="mini"
                      className="!flex !h-8 !w-8 !min-w-8 !items-center !justify-center !p-0"
                      aria-label={item.label}
                      aria-current={selected ? 'page' : undefined}
                      icon={item.icon}
                      onClick={() => navigate(item.key)}
                    />
                  </Tooltip>
                )
              })}
            </nav>
          ) : (
            <Menu
              selectedKeys={(() => {
                const hit = menu.find((item) => menuPathSelected(location.pathname, item.key))
                return hit ? [hit.key] : []
              })()}
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
          )}
        </div>
        <SidebarUserBar />
      </div>
    </Sider>
  )
}
