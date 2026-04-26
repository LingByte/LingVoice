import type { ApiResponse } from '@/utils/request'
import { get, post } from '@/utils/request'

export type AuthUser = {
  /** 十进制字符串，兼容超过 JS 安全整数范围的雪花 ID */
  id: string
  email: string
  displayName?: string
  firstName?: string
  lastName?: string
  role: string
  status?: string
  source?: string
  locale?: string
  timezone?: string
  avatar?: string
  emailVerified: boolean
  phoneVerified?: boolean
  profileComplete: number
  loginCount?: number
  remainQuota?: number
  usedQuota?: number
  unlimitedQuota?: boolean
  createdAt?: string
  lastLogin?: string
}

export type AuthSession = {
  user: AuthUser
  accessToken: string
  refreshToken: string
  tokenType: string
  expiresIn: number
  refreshExpiresIn: number
}

function ensureOk(r: ApiResponse<unknown>): void {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
}

function normalizeUserId(raw: unknown): string {
  if (raw == null) throw new Error('登录响应用户信息不完整')
  if (typeof raw === 'string') {
    const s = raw.trim()
    if (/^\d{1,32}$/.test(s)) return s
    throw new Error('登录响应用户信息不完整')
  }
  if (typeof raw === 'number' && Number.isFinite(raw)) {
    const n = Math.trunc(raw)
    if (n < 1) throw new Error('登录响应用户信息不完整')
    if (n > Number.MAX_SAFE_INTEGER) {
      throw new Error('登录响应用户信息不完整')
    }
    return String(n)
  }
  throw new Error('登录响应用户信息不完整')
}

/** 将接口返回的 user 对象规范为 AuthUser（id 优先字符串，兼容旧 number）。 */
export function normalizeAuthUser(raw: unknown): AuthUser {
  const u = raw as Partial<AuthUser> & { id?: number | string }
  const id = normalizeUserId(u?.id)
  const email = String(u?.email ?? '').trim()
  if (!email) {
    throw new Error('登录响应用户信息不完整')
  }
  const str = (v: unknown) => (v != null && String(v).trim() !== '' ? String(v).trim() : undefined)
  return {
    id,
    email,
    displayName: str(u.displayName),
    firstName: str(u.firstName),
    lastName: str(u.lastName),
    role: str(u.role) ?? 'user',
    status: str(u.status),
    source: str(u.source),
    locale: str(u.locale),
    timezone: str(u.timezone),
    avatar: str(u.avatar),
    emailVerified: Boolean(u.emailVerified),
    phoneVerified: Boolean(u.phoneVerified),
    profileComplete: Number(u.profileComplete) || 0,
    loginCount: u.loginCount != null ? Number(u.loginCount) : undefined,
    remainQuota: Number(u.remainQuota ?? 0) || 0,
    usedQuota: Number(u.usedQuota ?? 0) || 0,
    unlimitedQuota: Boolean(u.unlimitedQuota),
    createdAt: str(u.createdAt),
    lastLogin: str(u.lastLogin),
  }
}

/** 将后端 `data`（如登录/注册/刷新接口的 JSON）规范为 AuthSession；缺字段会抛错。 */
export function parseLoginResponseData(data: unknown): AuthSession {
  const d = data as Partial<AuthSession> & { user?: unknown }
  if (!d?.user || !d?.accessToken || !d?.refreshToken) {
    throw new Error('无效的登录响应')
  }
  const user = normalizeAuthUser(d.user)
  return {
    user,
    accessToken: String(d.accessToken).trim(),
    refreshToken: String(d.refreshToken).trim(),
    tokenType: d.tokenType ?? 'Bearer',
    expiresIn: Number(d.expiresIn) || 0,
    refreshExpiresIn: Number(d.refreshExpiresIn) || 0,
  }
}

function parseAuthSession(data: unknown): AuthSession {
  return parseLoginResponseData(data)
}

export async function loginWithPassword(
  email: string,
  password: string,
): Promise<AuthSession> {
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
  const r = await post<AuthSession>('/api/auth/login', {
    email: email.trim(),
    password,
    timezone: tz,
  })
  ensureOk(r)
  return parseAuthSession(r.data)
}

export async function sendEmailLoginCode(email: string): Promise<void> {
  const r = await post<unknown>('/api/auth/send-verify-email', {
    email: email.trim(),
  })
  ensureOk(r)
}

export async function loginWithEmailCode(
  email: string,
  code: string,
): Promise<AuthSession> {
  const digits = code.replace(/\D/g, '')
  const r = await post<AuthSession>('/api/auth/verify-email-login', {
    email: email.trim(),
    code: digits,
  })
  ensureOk(r)
  return parseAuthSession(r.data)
}

export type RegisterPayload = {
  email: string
  password: string
  displayName?: string
  source?: string
}

export async function registerAccount(payload: RegisterPayload): Promise<AuthSession> {
  const r = await post<AuthSession>('/api/auth/register', {
    email: payload.email.trim(),
    password: payload.password,
    displayName: payload.displayName?.trim(),
    source: payload.source || 'SYSTEM',
  })
  ensureOk(r)
  return parseAuthSession(r.data)
}

export async function refreshAuthSession(refreshToken: string): Promise<AuthSession> {
  const r = await post<AuthSession>('/api/auth/refresh', {
    refreshToken: refreshToken.trim(),
  })
  ensureOk(r)
  return parseAuthSession(r.data)
}

export async function logoutSession(): Promise<void> {
  const r = await post<unknown>('/api/auth/logout', {})
  if (r.code !== 200) {
    throw new Error(r.msg || '退出失败')
  }
}

export type FetchAuthMeResult =
  | { kind: 'ok'; user: AuthUser }
  | { kind: 'unauthorized' }
  | { kind: 'unavailable'; message?: string }

/**
 * 拉取当前登录用户信息。仅 `unauthorized` 表示服务端认定未登录，应清空本地会话；
 * `unavailable` 表示网络或非 401 业务错误，应保留本地已缓存的会话。
 */
export async function fetchAuthMe(): Promise<FetchAuthMeResult> {
  const r = await get<{ user: unknown }>('/api/auth/me')
  if (r.code === 401) return { kind: 'unauthorized' }
  if (r.code !== 200) {
    return { kind: 'unavailable', message: r.msg }
  }
  const data = r.data as { user?: unknown }
  if (!data?.user) {
    return { kind: 'unavailable', message: '响应缺少 user' }
  }
  try {
    return { kind: 'ok', user: normalizeAuthUser(data.user) }
  } catch {
    return { kind: 'unavailable', message: '用户信息格式无效' }
  }
}
