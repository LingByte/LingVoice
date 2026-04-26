import type { ApiResponse } from '@/utils/request'
import { del, get, post, put } from '@/utils/request'

export function assertOk<T>(r: ApiResponse<T>): T {
  if (r.code !== 200) {
    throw new Error(r.msg || '请求失败')
  }
  return r.data as T
}

export type Paginated<T> = {
  list: T[]
  total: number
  page: number
  pageSize: number
  totalPage: number
}

/** Built-in codes + default HTML from GET /api/mail-templates/presets */
export interface MailTemplatePreset {
  code: string
  name: string
  description: string
  htmlBody: string
  variables: string
}

export interface MailTemplateRow {
  id: number
  createAt?: string
  updateAt?: string
  createBy?: string
  updateBy?: string
  remark?: string
  code: string
  name: string
  htmlBody?: string
  textBody?: string
  description?: string
  variables?: string
  locale?: string
  enabled: boolean
}

export interface NotificationChannelRow {
  id: number
  createAt?: string
  updateAt?: string
  createBy?: string
  updateBy?: string
  remark?: string
  type: string
  code?: string
  name: string
  sortOrder: number
  enabled: boolean
  configJson?: string
}

export interface EmailChannelFormView {
  driver: 'smtp' | 'sendcloud' | string
  smtpHost?: string
  smtpPort?: number
  smtpUsername?: string
  smtpFrom?: string
  /** 收件人看到的发件人名称，如「解忧造物」；对应配置 from_name */
  fromDisplayName?: string
  smtpPasswordSet?: boolean
  sendcloudApiUser?: string
  sendcloudApiKeySet?: boolean
  sendcloudFrom?: string
}

export interface NotificationChannelDetail {
  channel: NotificationChannelRow
  emailForm?: EmailChannelFormView
}

export interface MailLogRow {
  /** 雪花 id，JSON 可能为字符串 */
  id: string | number
  user_id: number
  provider: string
  channel_name?: string
  to_email: string
  subject: string
  status: string
  html_body?: string
  error_msg?: string
  message_id?: string
  ip_address?: string
  retry_count: number
  sent_at?: string
  created_at?: string
  updated_at?: string
}

const api = {
  mailTemplates: '/api/mail-templates',
  notificationChannels: '/api/notification-channels',
  mailLogs: '/api/mail-logs',
}

export async function listMailTemplates(page: number, pageSize: number) {
  const r = await get<Paginated<MailTemplateRow>>(api.mailTemplates, {
    params: { page, pageSize },
  })
  return assertOk(r)
}

export async function listMailTemplatePresets() {
  const r = await get<MailTemplatePreset[]>(`${api.mailTemplates}/presets`)
  return assertOk(r)
}

export async function getMailTemplate(id: number) {
  const r = await get<MailTemplateRow>(`${api.mailTemplates}/${id}`)
  return assertOk(r)
}

export async function createMailTemplate(body: {
  code: string
  name: string
  htmlBody: string
  description?: string
  locale?: string
  enabled?: boolean
}) {
  const r = await post<MailTemplateRow>(api.mailTemplates, body)
  return assertOk(r)
}

export async function updateMailTemplate(
  id: number,
  body: {
    name: string
    htmlBody: string
    description?: string
    locale?: string
    enabled?: boolean
  },
) {
  const r = await put<MailTemplateRow>(`${api.mailTemplates}/${id}`, body)
  return assertOk(r)
}

export async function deleteMailTemplate(id: number) {
  const r = await del<{ id: number }>(`${api.mailTemplates}/${id}`)
  return assertOk(r)
}

export type TranslateMailTemplateBody = {
  fromLocale: string
  toLocale: string
  name: string
  htmlBody: string
  description: string
}

export type TranslateMailTemplateResult = {
  name: string
  htmlBody: string
  textBody: string
  description: string
}

/** Uses backend MyMemory integration; set MYMEMORY_EMAIL on server for higher quota. */
export async function translateMailTemplate(body: TranslateMailTemplateBody) {
  const r = await post<TranslateMailTemplateResult>(`${api.mailTemplates}/translate`, body)
  return assertOk(r)
}

export type EmailChannelUpsertBody = {
  channelType: 'email'
  driver: 'smtp' | 'sendcloud'
  name: string
  smtpHost?: string
  smtpPort?: number
  smtpUsername?: string
  smtpPassword?: string
  smtpFrom?: string
  /** 收件人看到的名称，写入 from_name */
  fromDisplayName?: string
  sendcloudApiUser?: string
  sendcloudApiKey?: string
  sendcloudFrom?: string
  sortOrder?: number
  enabled?: boolean
  remark?: string
}

export async function listNotificationChannels(page: number, pageSize: number, type?: string) {
  const r = await get<Paginated<NotificationChannelRow>>(api.notificationChannels, {
    params: { page, pageSize, ...(type ? { type } : {}) },
  })
  return assertOk(r)
}

export async function getNotificationChannelDetail(id: number) {
  const r = await get<NotificationChannelDetail>(`${api.notificationChannels}/${id}`)
  return assertOk(r)
}

export async function createNotificationChannel(body: EmailChannelUpsertBody) {
  const r = await post<NotificationChannelRow>(api.notificationChannels, body)
  return assertOk(r)
}

export async function updateNotificationChannel(id: number, body: EmailChannelUpsertBody) {
  const r = await put<NotificationChannelRow>(`${api.notificationChannels}/${id}`, body)
  return assertOk(r)
}

export async function deleteNotificationChannel(id: number) {
  const r = await del<{ id: number }>(`${api.notificationChannels}/${id}`)
  return assertOk(r)
}

/** 统一从接口对象取出 HTML（兼容 snake_case / camelCase）。 */
export function mailLogHtmlBody(row: Record<string, unknown> | MailLogRow): string {
  const r = row as Record<string, unknown>
  const v = r.html_body ?? r.htmlBody
  return typeof v === 'string' ? v : ''
}

export async function listMailLogs(
  page: number,
  pageSize: number,
  filters?: { user_id?: number; status?: string; provider?: string; channel_name?: string },
) {
  const r = await get<Paginated<MailLogRow>>(api.mailLogs, {
    params: { page, pageSize, ...filters },
  })
  const data = assertOk(r)
  return {
    ...data,
    list: data.list.map((row) => ({
      ...row,
      html_body: mailLogHtmlBody(row as unknown as Record<string, unknown>),
    })),
  }
}

export async function getMailLog(id: string | number): Promise<MailLogRow> {
  const r = await get<MailLogRow>(`${api.mailLogs}/${encodeURIComponent(String(id))}`)
  const row = assertOk(r) as unknown as Record<string, unknown>
  const html = mailLogHtmlBody(row)
  return { ...(row as unknown as MailLogRow), html_body: html }
}

export type MailLogCreateBody = {
  user_id?: number
  provider: string
  channel_name?: string
  to_email: string
  subject: string
  status: string
  html_body?: string
  error_msg?: string
  message_id?: string
  ip_address?: string
  retry_count?: number
}

export async function createMailLog(body: MailLogCreateBody) {
  const r = await post<MailLogRow>(api.mailLogs, body)
  return assertOk(r)
}

export async function updateMailLog(
  id: string | number,
  body: {
    subject?: string
    status: string
    error_msg?: string
    message_id?: string
    channel_name?: string
    html_body?: string
    to_email?: string
    provider?: string
  },
) {
  const r = await put<MailLogRow>(`${api.mailLogs}/${encodeURIComponent(String(id))}`, body)
  return assertOk(r)
}

export async function deleteMailLog(id: string | number) {
  const r = await del<{ id: number }>(`${api.mailLogs}/${encodeURIComponent(String(id))}`)
  return assertOk(r)
}
