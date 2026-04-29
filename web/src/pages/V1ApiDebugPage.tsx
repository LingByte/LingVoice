import { Alert, Button, Input, Message, Select, Space, Typography } from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { getApiBaseURL } from '@/config/apiConfig'

const { Title, Paragraph, Text } = Typography

type HttpMethod = 'GET' | 'POST'
type V1DebugAuthMode = 'email' | 'llm' | 'asr' | 'tts'

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
  authMode?: V1DebugAuthMode
}[] = [
  {
    label: '列出邮件模版',
    method: 'GET',
    path: '/v1/mail-templates?page=1&pageSize=10',
    body: '',
    authMode: 'email',
  },
  {
    label: '创建邮件模版（示例）',
    method: 'POST',
    path: '/v1/mail-templates',
    body: JSON.stringify(
      {
        code: 'debug_tpl_unique',
        name: 'V1 网关调试模版',
        htmlBody: '<p>{{.User}} 您好，您的验证码为 <strong>{{.Code}}</strong></p>',
        description: '',
        locale: 'zh-CN',
      },
      null,
      2,
    ),
    authMode: 'email',
  },
  {
    label: '邮件日志列表',
    method: 'GET',
    path: '/v1/mail-logs?page=1&pageSize=10',
    body: '',
    authMode: 'email',
  },
  {
    label: '发送邮件（按模版 + 参数）',
    method: 'POST',
    path: '/v1/mail/send',
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
    authMode: 'email',
  },
  {
    label: 'OpenAI chat/completions（凭证 kind=llm）',
    method: 'POST',
    path: '/v1/chat/completions',
    authMode: 'llm',
    body: JSON.stringify(
      {
        model: 'gpt-4o-mini',
        messages: [{ role: 'user', content: 'ping' }],
        max_tokens: 32,
        stream: false,
      },
      null,
      2,
    ),
  },
  {
    label: 'Agent chat/stream（NDJSON，OpenAI 协议渠道）',
    method: 'POST',
    path: '/v1/agent/chat/stream',
    authMode: 'llm',
    body: JSON.stringify(
      {
        model: 'gpt-4o-mini',
        input: '用三步说明如何泡一杯茶',
        max_tasks: 6,
      },
      null,
      2,
    ),
  },
  {
    label: 'Anthropic /v1/messages（凭证 kind=llm）',
    method: 'POST',
    path: '/v1/messages',
    authMode: 'llm',
    body: JSON.stringify(
      {
        model: 'claude-3-5-haiku-20241022',
        max_tokens: 256,
        messages: [
          {
            role: 'user',
            content: [{ type: 'text', text: 'ping' }],
          },
        ],
      },
      null,
      2,
    ),
  },
  {
    label: 'ASR transcribe（JSON：audio_url 与 audio_base64 二选一；推荐本页「上传音频文件」走 multipart）',
    method: 'POST',
    path: '/v1/speech/asr/transcribe',
    authMode: 'asr',
    body: JSON.stringify(
      {
        group: 'default',
        audio_url: '',
        audio_base64: '',
        format: 'mp3',
        language: 'zh-CN',
        extra: {},
      },
      null,
      2,
    ),
  },
  {
    label: 'ASR transcribe（audio_url 示例，需公网可访问 HTTPS）',
    method: 'POST',
    path: '/v1/speech/asr/transcribe',
    authMode: 'asr',
    body: JSON.stringify(
      {
        group: '',
        audio_url: 'https://example.com/sample.wav',
        format: 'wav',
        language: 'zh-CN',
      },
      null,
      2,
    ),
  },
  {
    label: 'TTS synthesize（返回 audio_base64，默认）',
    method: 'POST',
    path: '/v1/speech/tts/synthesize',
    authMode: 'tts',
    body: JSON.stringify(
      {
        group: '',
        text: '你好，这是 V1 网关语音合成调试。',
        voice: '',
        response_type: 'audio_base64',
        extra: {},
      },
      null,
      2,
    ),
  },
  {
    label: 'TTS synthesize（上传 LingStorage，返回 url；需 LINGSTORAGE_*）',
    method: 'POST',
    path: '/v1/speech/tts/synthesize',
    authMode: 'tts',
    body: JSON.stringify(
      {
        group: '',
        text: '你好，这是上传到对象存储的 TTS 示例。',
        voice: '',
        response_type: 'url',
        audio_format: 'mp3',
        sample_rate: 24000,
        upload_bucket: '',
        upload_key: '',
        upload_filename: 'tts-sample.mp3',
        tts_options: {},
        extra: {},
      },
      null,
      2,
    ),
  },
  {
    label: 'TTS synthesize（tts_options 显式指定编码，示例）',
    method: 'POST',
    path: '/v1/speech/tts/synthesize',
    authMode: 'tts',
    body: JSON.stringify(
      {
        group: '',
        text: '使用 tts_options 覆盖厂商参数。',
        voice: '',
        response_type: 'audio_base64',
        tts_options: {
          codec: 'mp3',
        },
        extra: {},
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

/** Anthropic Messages：与官方 SDK 一致优先 x-api-key */
function useAnthropicStyleKeyHeader(requestPath: string): boolean {
  const p = requestPath.trim()
  return p === '/v1/messages' || p.endsWith('/v1/messages')
}

function isHeaderLatin1Safe(v: string): boolean {
  for (let i = 0; i < v.length; i++) {
    if (v.charCodeAt(i) > 0xff) return false
  }
  return true
}

function hasHeaderControlChars(v: string): boolean {
  return /[\u0000-\u001f\u007f]/.test(v)
}

export function V1ApiDebugPage() {
  const [apiKey, setApiKey] = useState('')
  const [authMode, setAuthMode] = useState<V1DebugAuthMode>('email')
  const [method, setMethod] = useState<HttpMethod>('GET')
  const [path, setPath] = useState(PRESETS[0].path)
  const [body, setBody] = useState('')
  const [sending, setSending] = useState(false)
  const [status, setStatus] = useState<number | null>(null)
  const [responseText, setResponseText] = useState('')
  /** 与 JSON 中的 group/format/language/extra 一并用于 multipart ASR */
  const [asrUploadFile, setAsrUploadFile] = useState<File | null>(null)

  const presetOptions = useMemo(
    () => PRESETS.map((p, i) => ({ label: p.label, value: String(i) })),
    [],
  )

  useEffect(() => {
    const p = path.trim().split('?')[0] ?? path.trim()
    const isAsr = p === '/v1/speech/asr/transcribe' || p.endsWith('/v1/speech/asr/transcribe')
    if (!isAsr) setAsrUploadFile(null)
  }, [path])

  const applyPreset = useCallback((idxStr: string) => {
    const i = Number(idxStr)
    const p = PRESETS[i]
    if (!p) return
    setMethod(p.method)
    setPath(p.path)
    setAuthMode(p.authMode ?? 'email')
    setAsrUploadFile(null)
    if (p.path === '/v1/mail-templates' && p.method === 'POST') {
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
    try {
      const base = getApiBaseURL()
      const url = joinUrl(base, p)
      const headers: Record<string, string> = {}
      if (!isHeaderLatin1Safe(key) || hasHeaderControlChars(key)) {
        Message.error('API Key 包含非法字符（需使用可写入 HTTP Header 的 Latin-1/可见字符）')
        setResponseText('API Key 包含非 ISO-8859-1 或控制字符，浏览器会拒绝写入请求头。请确认密钥未混入中文、换行或不可见字符。')
        setSending(false)
        return
      }
      if (authMode === 'email') {
        const ts = Math.floor(Date.now() / 1000)
        const nonce = randomNonce()
        headers.LAuthorization = `Bearer ${key}`
        headers['L-Timestamp'] = String(ts)
        headers['L-Nonce'] = nonce
      } else {
        const pth = path.trim().split('?')[0] ?? path.trim()
        if (authMode === 'llm' && useAnthropicStyleKeyHeader(pth)) {
          headers['x-api-key'] = key
        } else {
          headers.Authorization = `Bearer ${key}`
        }
      }
      let init: RequestInit = { method, headers, credentials: 'omit' }
      if (method === 'POST') {
        const pth = path.trim().split('?')[0] ?? path.trim()
        const isAsrTranscribe = pth === '/v1/speech/asr/transcribe' || pth.endsWith('/v1/speech/asr/transcribe')
        if (isAsrTranscribe && asrUploadFile) {
          let group = ''
          let format = ''
          let language = ''
          let extraStr = ''
          try {
            const o = JSON.parse(body.trim() || '{}') as Record<string, unknown>
            group = typeof o.group === 'string' ? o.group : ''
            format = typeof o.format === 'string' ? o.format : ''
            language = typeof o.language === 'string' ? o.language : ''
            if (o.extra !== undefined && o.extra !== null) {
              extraStr = JSON.stringify(o.extra)
            }
          } catch {
            Message.warning('multipart ASR 需要 JSON Body 可解析，以读取 group、format、language、extra 字段')
            setSending(false)
            return
          }
          const fd = new FormData()
          fd.set('group', group)
          fd.set('format', format)
          fd.set('language', language)
          if (extraStr) fd.set('extra', extraStr)
          fd.set('audio', asrUploadFile)
          init = { ...init, body: fd }
        } else {
          headers['Content-Type'] = 'application/json'
          init = { ...init, body: body.trim() || '{}' }
        }
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
        V1 网关调试
      </Title>
      <Paragraph type="secondary" className="!mb-4 !mt-0 max-w-3xl text-[13px]">
        根路径 <Text code>/v1</Text>（与常见 LLM 网关一致），不经 <Text code>/api</Text>。邮件类接口：{' '}
        <Text code>LAuthorization</Text>、<Text code>L-Timestamp</Text>、<Text code>L-Nonce</Text>，凭证 <Text code>kind=email</Text>
        。LLM：<Text code>POST /v1/chat/completions</Text>（OpenAI 协议，Bearer）与 <Text code>POST /v1/messages</Text>（Anthropic
        协议，推荐 <Text code>x-api-key</Text> 或 Bearer），凭证 <Text code>kind=llm</Text>，按凭证 <Text code>group</Text> 选用同组
        LLM 渠道（协议须为 openai / anthropic）        。语音：<Text code>POST /v1/speech/asr/transcribe</Text>（<Text code>kind=asr</Text>
        ，Bearer）：<Text code>application/json</Text> 时 <Text code>audio_url</Text> 与 <Text code>audio_base64</Text> 二选一；推荐{' '}
        <Text code>multipart/form-data</Text> 上传字段 <Text code>audio</Text>（文件），并附 <Text code>group</Text> / <Text code>format</Text> /{' '}
        <Text code>language</Text> 等；{' '}
        <Text code>POST /v1/speech/tts/synthesize</Text>（<Text code>kind=tts</Text>，Bearer），<Text code>response_type</Text> 为{' '}
        <Text code>audio_base64</Text> 或 <Text code>url</Text>。发送邮件：<Text code>template_id</Text> + <Text code>params</Text>
        ，可选 <Text code>subject</Text>。
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
          <Text className="mb-1 block text-[13px]">认证方式</Text>
          <Select
            style={{ width: '100%' }}
            value={authMode}
            onChange={(v) => setAuthMode((v as V1DebugAuthMode) || 'email')}
            options={[
              { label: '邮件（L-* 头，kind=email）', value: 'email' },
              { label: 'LLM（Bearer / x-api-key，kind=llm）', value: 'llm' },
              { label: 'ASR（Bearer，kind=asr）', value: 'asr' },
              { label: 'TTS（Bearer，kind=tts）', value: 'tts' },
            ]}
          />
        </div>
        <div>
          <Text className="mb-1 block text-[13px]">API Key</Text>
          <Input.Password
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
          <Text className="mb-1 block text-[13px]">路径（相对 API 根，如 /v1/...）</Text>
          <Input value={path} onChange={setPath} placeholder="/v1/models" />
        </div>
        {method === 'POST' ? (
          <div className="space-y-3">
            {(path.trim().split('?')[0] ?? path.trim()) === '/v1/speech/asr/transcribe' ||
            (path.trim().split('?')[0] ?? path.trim()).endsWith('/v1/speech/asr/transcribe') ? (
              <div>
                <Text className="mb-1 block text-[13px]">ASR 音频（multipart，推荐）</Text>
                <input
                  type="file"
                  accept="audio/*,.mp3,.wav,.m4a,.aac,.opus,.pcm"
                  className="block w-full max-w-xl cursor-pointer text-[13px] file:mr-3 file:rounded file:border file:border-[var(--color-border-2)] file:bg-[var(--color-fill-2)] file:px-3 file:py-1.5 file:text-[12px]"
                  onChange={(e) => {
                    const f = e.target.files?.[0] ?? null
                    setAsrUploadFile(f)
                  }}
                />
                {asrUploadFile ? (
                  <Text type="secondary" className="mt-1 block text-[12px]">
                    已选 {asrUploadFile.name}（{asrUploadFile.size < 1024 ? `${asrUploadFile.size} B` : `${Math.round(asrUploadFile.size / 1024)} KiB`}）
                  </Text>
                ) : null}
                <Text type="secondary" className="mt-1 block text-[12px]">
                  选择文件后点击发送将使用 multipart（字段 audio），下方 JSON 中的 group、format、language、extra 会一并提交；勿在 JSON 里再填
                  audio_url / audio_base64。
                </Text>
              </div>
            ) : null}
            <div>
              <Text className="mb-1 block text-[13px]">JSON Body</Text>
              <Input.TextArea
                value={body}
                onChange={setBody}
                autoSize={{ minRows: 8, maxRows: 20 }}
                className="font-mono text-[12px]"
              />
            </div>
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
