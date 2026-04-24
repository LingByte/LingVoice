/**
 * 后端 API 根地址。在 `.env` / `.env.development` 中配置：
 * `VITE_API_BASE_URL=http://127.0.0.1:8080`
 */
export function getApiBaseURL(): string {
  const raw = import.meta.env.VITE_API_BASE_URL
  if (typeof raw === 'string' && raw.trim()) {
    return raw.replace(/\/$/, '')
  }
  return ''
}
