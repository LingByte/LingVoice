import { Layout } from '@arco-design/web-react'
import { Outlet } from 'react-router-dom'

const { Content } = Layout

/**
 * 文档专用布局：全屏内容，无侧栏
 */
export function DocsLayout() {
  return (
    <Layout className="h-screen min-h-0">
      <Content className="flex h-full min-h-0 w-full flex-1 flex-col overflow-hidden bg-[var(--color-bg-1)]">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <Outlet />
        </div>
      </Content>
    </Layout>
  )
}
