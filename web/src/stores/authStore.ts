import { create } from 'zustand'
import { parseLoginResponseData, type AuthSession, type AuthUser } from '@/api/auth'
import { syncLocaleFromAuthUser } from '@/locale/sync'

/** localStorage key for access JWT; axios reads this if Zustand has not hydrated yet. */
export const AUTH_ACCESS_TOKEN_KEY = 'auth_token'

export const AUTH_REFRESH_TOKEN_KEY = 'auth_refresh'
export const AUTH_USER_KEY = 'auth_user'
/** 可选：保存 tokenType、过期秒数、写入时间，便于前端展示或调试 */
export const AUTH_SESSION_META_KEY = 'auth_session_meta'

export type AuthSessionMeta = {
  tokenType: string
  expiresIn: number
  refreshExpiresIn: number
  storedAt: number
}

type AuthState = {
  token: string | null
  refreshToken: string | null
  user: AuthUser | null
  sessionMeta: AuthSessionMeta | null
  setToken: (token: string | null) => void
  setRefreshToken: (token: string | null) => void
  setUser: (user: AuthUser | null) => void
  clearUser: () => void
  hydrateFromStorage: () => void
}

function readStoredAccess(): string | null {
  try {
    return localStorage.getItem(AUTH_ACCESS_TOKEN_KEY)
  } catch {
    return null
  }
}

function readStoredRefresh(): string | null {
  try {
    return localStorage.getItem(AUTH_REFRESH_TOKEN_KEY)
  } catch {
    return null
  }
}

function readStoredUser(): AuthUser | null {
  try {
    const raw = localStorage.getItem(AUTH_USER_KEY)
    if (!raw) return null
    return JSON.parse(raw) as AuthUser
  } catch {
    return null
  }
}

function readStoredMeta(): AuthSessionMeta | null {
  try {
    const raw = localStorage.getItem(AUTH_SESSION_META_KEY)
    if (!raw) return null
    return JSON.parse(raw) as AuthSessionMeta
  } catch {
    return null
  }
}

function writeUser(user: AuthUser | null) {
  try {
    if (user) localStorage.setItem(AUTH_USER_KEY, JSON.stringify(user))
    else localStorage.removeItem(AUTH_USER_KEY)
  } catch {
    /* ignore */
  }
}

/**
 * 登录/注册/刷新成功后调用：把解析后的会话写入 localStorage 并同步到 Zustand（axios 会带上 Authorization）。
 * 也可传入原始 `response.data`，内部会 `parseLoginResponseData`。
 */
export function persistAuthSession(sessionOrRaw: AuthSession | unknown): AuthSession {
  const session = parseLoginResponseData(sessionOrRaw)

  const meta: AuthSessionMeta = {
    tokenType: session.tokenType,
    expiresIn: session.expiresIn,
    refreshExpiresIn: session.refreshExpiresIn,
    storedAt: Date.now(),
  }

  try {
    localStorage.setItem(AUTH_ACCESS_TOKEN_KEY, session.accessToken)
    localStorage.setItem(AUTH_REFRESH_TOKEN_KEY, session.refreshToken)
    localStorage.setItem(AUTH_USER_KEY, JSON.stringify(session.user))
    localStorage.setItem(AUTH_SESSION_META_KEY, JSON.stringify(meta))
  } catch {
    throw new Error('无法写入本地存储')
  }

  useAuthStore.setState({
    token: session.accessToken,
    refreshToken: session.refreshToken,
    user: session.user,
    sessionMeta: meta,
  })

  syncLocaleFromAuthUser(session.user)

  return session
}

export const useAuthStore = create<AuthState>((set) => ({
  token: readStoredAccess(),
  refreshToken: readStoredRefresh(),
  user: readStoredUser(),
  sessionMeta: readStoredMeta(),
  setToken: (token) => {
    try {
      if (token) localStorage.setItem(AUTH_ACCESS_TOKEN_KEY, token)
      else localStorage.removeItem(AUTH_ACCESS_TOKEN_KEY)
    } catch {
      /* ignore */
    }
    set({ token })
  },
  setRefreshToken: (refreshToken) => {
    try {
      if (refreshToken) localStorage.setItem(AUTH_REFRESH_TOKEN_KEY, refreshToken)
      else localStorage.removeItem(AUTH_REFRESH_TOKEN_KEY)
    } catch {
      /* ignore */
    }
    set({ refreshToken })
  },
  setUser: (user) => {
    writeUser(user)
    set({ user })
  },
  clearUser: () => {
    try {
      localStorage.removeItem(AUTH_ACCESS_TOKEN_KEY)
      localStorage.removeItem(AUTH_REFRESH_TOKEN_KEY)
      localStorage.removeItem(AUTH_USER_KEY)
      localStorage.removeItem(AUTH_SESSION_META_KEY)
    } catch {
      /* ignore */
    }
    set({ token: null, refreshToken: null, user: null, sessionMeta: null })
  },
  hydrateFromStorage: () => {
    set({
      token: readStoredAccess(),
      refreshToken: readStoredRefresh(),
      user: readStoredUser(),
      sessionMeta: readStoredMeta(),
    })
  },
}))
