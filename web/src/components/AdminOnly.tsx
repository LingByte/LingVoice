import { Result } from '@arco-design/web-react'
import type { ReactNode } from 'react'
import { useAuthStore } from '@/stores/authStore'
import { isAdminRole } from '@/utils/authz'

type Props = {
  title?: string
  children: ReactNode
}

/** 非管理员或未登录时展示占位，不发起业务子组件渲染（由父页面控制是否仍拉接口）。 */
export function AdminOnly(props: Props) {
  const user = useAuthStore((s) => s.user)
  const title = props.title ?? 'LLM 管理'

  if (!user) {
    return (
      <div className="flex h-full min-h-0 flex-1 items-center justify-center p-6">
        <Result status="403" title="请先登录" subTitle="该页面仅限管理员使用，请先登录后再访问。" />
      </div>
    )
  }
  if (!isAdminRole(user.role)) {
    return (
      <div className="flex h-full min-h-0 flex-1 items-center justify-center p-6">
        <Result status="403" title="需要管理员权限" subTitle={`「${title}」仅对 admin / superadmin 开放。`} />
      </div>
    )
  }
  return <>{props.children}</>
}
