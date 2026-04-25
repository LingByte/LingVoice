import { Message } from '@arco-design/web-react'
import { AUTH_ACCESS_TOKEN_KEY, useAuthStore } from '@/stores/authStore'

let redirectScheduled = false

/** 会话失效：清空本地态、提示并回到首页（用于 HTTP 401 或业务 code 401）。 */
export function redirectUnauthorizedToHome(): void {
  if (redirectScheduled) return
  redirectScheduled = true
  useAuthStore.getState().clearUser()
  try {
    localStorage.removeItem(AUTH_ACCESS_TOKEN_KEY)
  } catch {
    /* ignore */
  }
  Message.warning({ content: '暂未登录', duration: 2200 })
  window.setTimeout(() => {
    redirectScheduled = false
    if (window.location.pathname !== '/login') {
      window.location.assign('/login')
    }
  }, 280)
}
