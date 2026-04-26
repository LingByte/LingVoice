import { Navigate } from 'react-router-dom'

/** 设置已合并至个人中心，保留旧路径兼容。 */
export function SettingsPage() {
  return <Navigate to="/profile?tab=settings" replace />
}
