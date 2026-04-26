import { Button, Divider, Input, Menu, Space, Tag, Typography } from '@arco-design/web-react'
import {
  BookOpen,
  Bot,
  Braces,
  Cpu,
  FileText,
  Inbox,
  Info,
  Key,
  LayoutDashboard,
  Mail,
  Mic,
  RadioTower,
  Search,
  Settings,
  Sparkles,
  Users,
  Volume2,
  Zap,
  type LucideIcon,
} from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import type { DocsNavFile } from '@/components/docs/types'
import type { OpenApiDoc } from '@/components/docs/openapi'

const { Text } = Typography

const ICON_MAP: Record<string, LucideIcon> = {
  Info,
  BookOpen,
  Bot,
  Cpu,
  LayoutDashboard,
  Zap,
  RadioTower,
  Users,
  Settings,
  Braces,
  FileText,
  Key,
  Sparkles,
  Mic,
  Volume2,
  Mail,
  Inbox,
}

function NavIcon({ name }: { name?: string }) {
  const I = (name && ICON_MAP[name]) || BookOpen
  return (
    <span className="inline-flex h-4 w-4 shrink-0 items-center justify-center self-start pt-0.5">
      <I size={16} strokeWidth={1.85} />
    </span>
  )
}

export interface DocsSidebarProps {
  nav: DocsNavFile
  pageId: string
  openapi: OpenApiDoc | null
  onOpenSearch: () => void
  onSelectPage: (id: string) => void
}

/**
 * 文档站侧栏：Arco Menu + ItemGroup，固定展示、无折叠。
 */
export function DocsSidebar({ nav, pageId, openapi, onOpenSearch, onSelectPage }: DocsSidebarProps) {
  const navigate = useNavigate()
  return (
    <div className="flex h-full min-h-0 flex-col bg-[var(--color-bg-1)]">
      <div className="flex items-start justify-between gap-2 px-4 py-3">
        <Space align="center" size={12} className="min-w-0 flex-1">
          <img src="/logo.png" alt="" className="h-8 w-8 shrink-0 rounded-lg object-contain shadow-sm" />
          <div className="min-w-0 flex-1 leading-tight">
            <div className="break-words text-[15px] font-semibold tracking-tight text-[var(--color-text-1)]">
              LingVoice
            </div>
            <Text type="secondary" className="!m-0 !block !text-[11px] !font-medium">
              API 文档
            </Text>
          </div>
        </Space>
        <Button type="outline" size="mini" className="shrink-0" onClick={() => navigate('/')}>
          返回首页
        </Button>
      </div>

      <Divider className="!m-0" />

      <div className="shrink-0 px-2 py-2">
        <Input
          readOnly
          placeholder="搜索文档…"
          suffix={
            <Text type="secondary" className="!mr-1 !font-mono !text-[10px]">
              ⌘K
            </Text>
          }
          prefix={<Search size={15} className="text-[var(--color-text-3)]" />}
          onClick={onOpenSearch}
          onFocus={(e) => e.target.blur()}
          className="cursor-pointer"
        />
      </div>

      <Divider className="!m-0" />

      <div className="docs-sidebar-scroll min-h-0 flex-1 overflow-y-auto py-2">
        <Menu
          mode="vertical"
          ellipsis={false}
          selectedKeys={pageId ? [pageId] : []}
          onClickMenuItem={(key) => onSelectPage(key)}
          className="docs-sidebar-menu !border-0 !bg-transparent"
          style={{ background: 'transparent' }}
        >
          {nav.groups.map((g) => (
            <Menu.ItemGroup key={g.id} title={g.title}>
              {g.items.map((item) => (
                <Menu.Item key={item.id}>
                  <Space size={8} align="start" className="w-full justify-start py-0.5">
                    <NavIcon name={item.icon} />
                    <span className="min-w-0 flex-1 break-words text-[13px] leading-snug">{item.label}</span>
                    {item.badge ? (
                      <Tag size="small" color={item.badgeColor ?? 'gray'} className="!m-0 shrink-0 self-start">
                        {item.badge}
                      </Tag>
                    ) : null}
                  </Space>
                </Menu.Item>
              ))}
            </Menu.ItemGroup>
          ))}
        </Menu>
      </div>

      {openapi?.info?.version ? (
        <>
          <Divider className="!m-0" />
          <div className="px-4 py-2.5">
            <Text type="secondary" className="!m-0 !block !text-[10px] !leading-relaxed">
              OpenAPI {openapi.info.version}
              {openapi.servers?.[0]?.url ? (
                <>
                  {' '}
                  <Text type="secondary" className="!text-[10px] !opacity-60">
                    ·
                  </Text>{' '}
                  <Text code className="!text-[10px]">
                    {openapi.servers[0].url}
                  </Text>
                </>
              ) : null}
            </Text>
          </div>
        </>
      ) : null}
    </div>
  )
}
