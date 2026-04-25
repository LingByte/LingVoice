import { Alert, Button, Input, Message, Select, Space, Typography } from '@arco-design/web-react'
import { useCallback, useMemo, useState } from 'react'
import { getApiBaseURL } from '@/config/apiConfig'

const { Title, Paragraph, Text } = Typography

type HttpMethod = 'GET' | 'POST'

function randomNonce(): string {
  const a = new Uint8Array(16)
  crypto.getRandomValues(a)
  return Array.from(a, (b) => b.toString(16).padStart(2, '0')).join('')
}

const PRESETS: {
  label: string
  method: HttpMethod
  path: string
  body: string
}[] = [
  {
    label: '列出邮件模版',
    method: 'GET',
    path: '/api/openapi/v1/mail-templates?page=1&pageSize=10',
    body: '',
  },
  {
    label: '创建邮件模版（示例）',
    method: 'POST',
    path: '/api/openapi/v1/mail-templates',
    body: JSON.stringify(
      {
        code: 'debug_tpl_unique',
        name: 'OpenAPI 调试模版',
        htmlBody: '<p>{{.User}} 您好，您的验证码为 <strong>{{.Code}}</strong></p>',
        description: '',
        locale: 'zh-CN',
      },
      null,
      2,
    ),
  },
  {
    label: '邮件日志列表',
    method: 'GET',
    path: '/api/openapi/v1/mail-logs?page=1&pageSize=10',
    body: '',
  },
  {
    label: '发送邮件（按模版 + 参数）',
    method: 'POST',
    path: '/api/openapi/v1/mail/send',
    body: JSON.stringify(
      {
        template_id: 1,
        to: 'you@example.com',
        subject: '验证码 {{.Code}}',
        params: {
          User: '张三',
          Code: '123456',
        },
      },
      null,
      2,
    ),
  },
]

function joinUrl(base: string, path: string): string {
  const b = base.replace(/\/$/, '')
  const p = path.startsWith('/') ? path : `/${path}`
  return `${b}${p}`
}

export function OpenApiDebugPage() {
  const [apiKey, setApiKey] = useState('')
  const [method, setMethod] = useState<HttpMethod>('GET')
  const [path, setPath] = useState(PRESETS[0].path)
  const [body, setBody] = useState('')
  const [sending, setSending] = useState(false)
  const [status, setStatus] = useState<number | null>(null)
  const [responseText, setResponseText] = useState('')

  const presetOptions = useMemo(
    () => PRESETS.map((p, i) => ({ label: p.label, value: String(i) })),
    [],
  )

  const applyPreset = useCallback((idxStr: string) => {
    const i = Number(idxStr)
    const p = PRESETS[i]
    if (!p) return
    setMethod(p.method)
    setPath(p.path)
    if (p.path === '/api/openapi/v1/mail-templates' && p.method === 'POST') {
      try {
        const o = JSON.parse(p.body || '{}') as Record<string, unknown>
        o.code = `debug_tpl_${Date.now()}`
        setBody(JSON.stringify(o, null, 2))
      } catch {
        setBody(p.body)
      }
    } else {
      setBody(p.body)
    }
  }, [])

  const send = async () => {
    const key = apiKey.trim()
    if (!key) {
      Message.warning('请填写邮件 API 密钥（kind=email）')
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
    try {
      const base = getApiBaseURL()
      const url = joinUrl(base, p)
      const ts = Math.floor(Date.now() / 1000)
      const nonce = randomNonce()
      const headers: Record<string, string> = {
        LAuthorization: `Bearer ${key}`,
        'L-Timestamp': String(ts),
        'L-Nonce': nonce,
      }
      let init: RequestInit = { method, headers, credentials: 'omit' }
      if (method === 'POST') {
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
    }
  }

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        OpenAPI 调试
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 max-w-3xl text-[13px]">
        使用 <Text code>LAuthorization</Text>、<Text code>L-Timestamp</Text>、<Text code>L-Nonce</Text>
        调用 <Text code>/api/openapi/v1</Text>，不走浏览器会话 JWT。仅支持 kind 为 email 的密钥。
        发送邮件接口为 <Text code>template_id</Text> + <Text code>params</Text>（与模版 HTML 中{' '}
        <Text code>{'{{.Key}}'}</Text> 对应），可选 <Text code>subject</Text>（可含占位符）。
      </Paragraph>

      <Alert
        type="warning"
        className="!mb-4 max-w-3xl"
        content="请勿在公共环境粘贴生产密钥；本页请求使用 fetch 且 credentials: omit，不附带 Cookie。"
      />

      <div className="mb-4 max-w-3xl space-y-3">
        <div>
          <Text className="mb-1 block text-[13px]">快捷场景</Text>
          <Select
            placeholder="选择预设填充路径与方法"
            style={{ width: '100%' }}
            options={presetOptions}
            onChange={(v) => applyPreset(String(v))}
            allowClear
          />
        </div>
        <div>
          <Text className="mb-1 block text-[13px]">API Key（邮件凭证）</Text>
          <Input.Password placeholder="粘贴完整密钥" value={apiKey} onChange={setApiKey} />
        </div>
        <Space>
          <Select
            style={{ width: 100 }}
            value={method}
            onChange={(v) => setMethod((v as HttpMethod) || 'GET')}
            options={[
              { label: 'GET', value: 'GET' },
              { label: 'POST', value: 'POST' },
            ]}
          />
          <Button type="primary" loading={sending} onClick={() => void send()}>
            发送
          </Button>
        </Space>
        <div>
          <Text className="mb-1 block text-[13px]">路径（相对根，含 /api/openapi/...）</Text>
          <Input value={path} onChange={setPath} placeholder="/api/openapi/v1/..." />
        </div>
        {method === 'POST' ? (
          <div>
            <Text className="mb-1 block text-[13px]">JSON Body</Text>
            <Input.TextArea
              value={body}
              onChange={setBody}
              autoSize={{ minRows: 8, maxRows: 20 }}
              className="font-mono text-[12px]"
            />
          </div>
        ) : null}
      </div>

      <div className="min-h-0 flex-1 min-w-0 max-w-4xl">
        <Text className="mb-1 block text-[13px]">响应</Text>
        {status != null ? (
          <Text type="secondary" className="mb-1 block text-[12px]">
            HTTP {status}
          </Text>
        ) : null}
        <Input.TextArea
          readOnly
          value={responseText}
          placeholder="点击发送后在此显示结果"
          className="font-mono text-[12px]"
          autoSize={{ minRows: 14, maxRows: 36 }}
        />
      </div>
    </div>
  )
}
