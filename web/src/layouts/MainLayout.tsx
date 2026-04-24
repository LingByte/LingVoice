import { Layout } from '@arco-design/web-react'
import { Outlet } from 'react-router-dom'
import { AppSidebar } from '@/components/layout/AppSidebar'

const { Content } = Layout

/**
 * 左侧 Sidebar 全高；右侧仅主内容区铺满（无顶栏、无页脚）。
 */
export function MainLayout() {
  return (
    <Layout
      className="arco-layout-has-sider h-screen min-h-0"
      style={{ flexDirection: 'row' }}
    >
      <AppSidebar />
      <Layout className="flex h-full min-h-0 min-w-0 flex-1 flex-col">
        <Content className="flex min-h-0 w-full flex-1 flex-col overflow-hidden bg-[var(--color-bg-1)] p-0">
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            <Outlet />
          </div>
        </Content>
      </Layout>
    </Layout>
  )
}
