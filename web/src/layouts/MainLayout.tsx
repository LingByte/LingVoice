import { useEffect } from 'react'
import { Layout } from '@arco-design/web-react'
import { Outlet } from 'react-router-dom'
import { fetchAuthMe } from '@/api/auth'
import { AppSidebar } from '@/components/layout/AppSidebar'
import { useAuthStore } from '@/stores/authStore'

const { Content } = Layout

/**
 * 左侧 Sidebar 全高；右侧仅主内容区铺满（无顶栏、无页脚）。
 */
export function MainLayout() {
  const setUser = useAuthStore((s) => s.setUser)
  const clearUser = useAuthStore((s) => s.clearUser)

  const hydrateFromStorage = useAuthStore((s) => s.hydrateFromStorage)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const res = await fetchAuthMe()
        if (cancelled) return
        if (res.kind === 'ok') {
          setUser(res.user)
        } else if (res.kind === 'unauthorized') {
          clearUser()
        } else {
          hydrateFromStorage()
        }
      } catch {
        if (!cancelled) hydrateFromStorage()
      }
    })()
    return () => {
      cancelled = true
    }
  }, [setUser, clearUser, hydrateFromStorage])

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
