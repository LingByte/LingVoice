import { Alert, Button, Input, Message, Select, Space, Typography } from '@arco-design/web-react'
import { useCallback, useMemo, useState } from 'react'
import { getApiBaseURL } from '@/config/apiConfig'
import { DocCodeEditor } from '@/components/docs/DocCodeEditor'

const { Text } = Typography

export type TryAuthMode = 'email' | 'llm' | 'asr' | 'tts'

function randomNonce(): string {
  const a = new Uint8Array(16)
  crypto.getRandomValues(a)
  return Array.from(a, (b) => b.toString(16).padStart(2, '0')).join('')
}

function joinUrl(base: string, path: string): string {
  const b = base.replace(/\/$/, '')
  const p = path.startsWith('/') ? path : `/${path}`
  return `${b}${p}`
}

function useAnthropicStyleKeyHeader(requestPath: string): boolean {
  const p = requestPath.trim().split('?')[0] ?? requestPath.trim()
  return p === '/v1/messages' || p.endsWith('/v1/messages')
}

export interface ApiTryItPanelProps {
  initialMethod?: string
  /** 相对 API 根的路径，如 /v1/chat/completions */
  initialPath?: string
  initialBody?: string
  /** 折叠为紧凑条（嵌入文档流） */
  compact?: boolean
}

export function ApiTryItPanel({
  initialMethod = 'GET',
  initialPath = '/v1/models',
  initialBody = '',
  compact = false,
}: ApiTryItPanelProps) {
  const [authMode, setAuthMode] = useState<TryAuthMode>('llm')
  const [apiKey, setApiKey] = useState('')
  const [method, setMethod] = useState(initialMethod.toUpperCase())
  const [path, setPath] = useState(initialPath)
  const [body, setBody] = useState(initialBody)
  const [sending, setSending] = useState(false)
  const [status, setStatus] = useState<number | null>(null)
  const [responseText, setResponseText] = useState('')
  const [durationMs, setDurationMs] = useState<number | null>(null)

  const methodOptions = useMemo(
    () =>
      ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map((m) => ({
        label: m,
        value: m,
      })),
    [],
  )

  const applyInitial = useCallback(() => {
    setMethod(initialMethod.toUpperCase())
    setPath(initialPath)
    setBody(initialBody)
  }, [initialMethod, initialPath, initialBody])

  const send = async () => {
    const key = apiKey.trim()
    if (!key) {
      const hint =
        authMode === 'llm'
          ? '请填写 LLM 代理 API 密钥（kind=llm）'
          : authMode === 'asr'
            ? '请填写 ASR 密钥（kind=asr）'
            : authMode === 'tts'
              ? '请填写 TTS 密钥（kind=tts）'
              : '请填写邮件 API 密钥（kind=email）'
      Message.warning(hint)
      return
    }
    const p = path.trim()
    if (!p) {
      Message.warning('请填写路径')
      return
    }
    setSending(true)
    setStatus(null)
    setResponseText('')
    setDurationMs(null)
    const t0 = performance.now()
    try {
      const base = getApiBaseURL()
      const url = joinUrl(base, p)
      const headers: Record<string, string> = {}
      if (authMode === 'email') {
        const ts = Math.floor(Date.now() / 1000)
        const nonce = randomNonce()
        headers.LAuthorization = `Bearer ${key}`
        headers['L-Timestamp'] = String(ts)
        headers['L-Nonce'] = nonce
      } else {
        const pth = p.split('?')[0] ?? p
        if (authMode === 'llm' && useAnthropicStyleKeyHeader(pth)) {
          headers['x-api-key'] = key
        } else {
          headers.Authorization = `Bearer ${key}`
        }
      }
      let init: RequestInit = { method, headers, credentials: 'omit' }
      const writeMethods = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])
      if (writeMethods.has(method)) {
        headers['Content-Type'] = 'application/json'
        init = { ...init, body: body.trim() || '{}' }
      }
      const res = await fetch(url, init)
      const text = await res.text()
      setStatus(res.status)
      try {
        const j = JSON.parse(text) as unknown
        setResponseText(JSON.stringify(j, null, 2))
      } catch {
        setResponseText(text)
      }
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '请求失败')
      setResponseText(e instanceof Error ? e.message : String(e))
    } finally {
      setSending(false)
      setDurationMs(Math.round(performance.now() - t0))
    }
  }

  const header = (
    <div className="flex flex-wrap items-center justify-between gap-2">
      <Text className="text-[13px] font-medium text-[var(--color-text-1)]">在线调试</Text>
      <Button type="text" size="mini" onClick={applyInitial}>
        重置为接口预设
      </Button>
    </div>
  )

  const controls = (
    <div className="space-y-3">
      <Alert
        type="warning"
        content="请勿在公共环境粘贴生产密钥；请求使用 fetch、credentials: omit，不附带 Cookie。"
      />
      <div className="grid gap-3 sm:grid-cols-2">
        <div>
          <Text className="mb-1 block text-[12px] text-[var(--color-text-2)]">认证方式</Text>
          <Select
            size="small"
            style={{ width: '100%' }}
            value={authMode}
            onChange={(v) => setAuthMode((v as TryAuthMode) || 'llm')}
            options={[
              { label: 'LLM（Bearer / x-api-key，kind=llm）', value: 'llm' },
              { label: '邮件（L-* 头，kind=email）', value: 'email' },
              { label: 'ASR（Bearer，kind=asr）', value: 'asr' },
              { label: 'TTS（Bearer，kind=tts）', value: 'tts' },
            ]}
          />
        </div>
        <div>
          <Text className="mb-1 block text-[12px] text-[var(--color-text-2)]">API Key</Text>
          <Input.Password
            size="small"
            placeholder={
              authMode === 'llm'
                ? 'kind=llm 的密钥'
                : authMode === 'asr'
                  ? 'kind=asr 的密钥'
                  : authMode === 'tts'
                    ? 'kind=tts 的密钥'
                    : 'kind=email 的密钥'
            }
            value={apiKey}
            onChange={setApiKey}
          />
        </div>
      </div>
      <div>
        <Text className="mb-1 block text-[12px] text-[var(--color-text-2)]">API 根地址</Text>
        <Input size="small" readOnly value={getApiBaseURL()} />
      </div>
      <Space size="small" wrap>
        <Select
          size="small"
          style={{ width: 108 }}
          value={method}
          onChange={(v) => setMethod(String(v).toUpperCase())}
          options={methodOptions}
        />
        <Button type="primary" size="small" loading={sending} onClick={() => void send()}>
          发送
        </Button>
        {status != null ? (
          <Text className="text-[12px] text-[var(--color-text-2)]">
            HTTP {status}
            {durationMs != null ? ` · ${durationMs} ms` : ''}
          </Text>
        ) : null}
      </Space>
      <div>
        <Text className="mb-1 block text-[12px] text-[var(--color-text-2)]">路径（含 /v1 前缀）</Text>
        <Input size="small" value={path} onChange={setPath} placeholder="/v1/chat/completions" />
      </div>
      {['POST', 'PUT', 'PATCH', 'DELETE'].includes(method) ? (
        <div>
          <Text className="mb-1 block text-[12px] text-[var(--color-text-2)]">请求体 JSON</Text>
          <DocCodeEditor value={body} onChange={setBody} language="json" readOnly={false} minHeight="200px" height="220px" />
        </div>
      ) : null}
    </div>
  )

  const responsePane = (
    <div className="min-h-0 flex-1">
      <Text className="mb-1 block text-[12px] text-[var(--color-text-2)]">响应</Text>
      <DocCodeEditor value={responseText} language="json" readOnly minHeight={compact ? '200px' : '280px'} height={compact ? '220px' : '320px'} />
    </div>
  )

  if (compact) {
    return (
      <div className="rounded-lg border border-[var(--color-border-2)] bg-[var(--color-bg-2)] p-4">
        {header}
        <div className="mt-3">{controls}</div>
        <div className="mt-4">{responsePane}</div>
      </div>
    )
  }

  return (
    <div className="rounded-xl border border-[var(--color-border-2)] bg-[var(--color-fill-1)] p-5 shadow-sm">
      {header}
      <div className="mt-4">{controls}</div>
      <div className="mt-5">{responsePane}</div>
    </div>
  )
}
