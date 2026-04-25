import type { KeyboardEvent } from 'react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Alert,
  Avatar,
  Button,
  Card,
  Divider,
  Empty,
  Input,
  Layout,
  Message,
  Popconfirm,
  Select,
  Space,
  Typography,
} from '@arco-design/web-react'
import {
  appendChatMessage,
  createChatSession,
  deleteChatSession,
  listChatMessages,
  listChatSessions,
  patchChatSessionTitle,
} from '@/api/chat'
import { fetchOpenAPIModels, streamOpenAIChatCompletion } from '@/api/openapiLlm'
import { useAuthStore } from '@/stores/authStore'
import {
  Bot,
  ChevronLeft,
  ChevronRight,
  MessageSquarePlus,
  Mic,
  Paperclip,
  Send,
  Trash2,
} from 'lucide-react'
import { cn } from '@/lib/cn'

const { Header, Footer, Content } = Layout
const { Title, Paragraph, Text } = Typography
const TextArea = Input.TextArea

type ChatSession = {
  id: string
  title: string
  timeLabel: string
  /** 云端会话绑定的模型，切换会话时同步到输入区 */
  boundModel?: string
}

type ChatMessage = { id: string; role: 'user' | 'assistant' | 'system'; content: string }

const LS_OPENAPI_KEY = 'lingvoice_openapi_llm_key'
const LS_CHAT_MODEL = 'lingvoice_chat_model'

function readLocal(key: string) {
  try {
    return localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function formatTimeLabel() {
  return new Date().toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

function formatTimeFromMs(ms: number) {
  if (!ms || Number.isNaN(ms)) return formatTimeLabel()
  return new Date(ms).toLocaleTimeString('zh-CN', {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

function newSession(title = '新对话'): ChatSession {
  return {
    id:
      typeof crypto !== 'undefined' && crypto.randomUUID
        ? crypto.randomUUID()
        : `s-${Date.now()}`,
    title,
    timeLabel: formatTimeLabel(),
  }
}

function ChatMessageRow({ message }: { message: ChatMessage }) {
  const isUser = message.role === 'user'
  const isSystem = message.role === 'system'
  return (
    <div
      className={cn(
        'chat-msg-row',
        isUser ? 'chat-msg-row--user' : 'chat-msg-row--assistant',
        isSystem && 'opacity-95',
      )}
    >
      {!isUser && (
        <Avatar
          size={36}
          shape="circle"
          className="chat-msg-avatar chat-msg-avatar--assistant shrink-0 !flex !items-center !justify-center"
        >
          <Bot size={20} strokeWidth={1.85} className="text-[rgb(var(--primary-6))]" />
        </Avatar>
      )}
      <Card
        size="small"
        bordered={false}
        className={cn(
          'chat-msg-bubble min-w-0 max-w-[min(85vw,520px)] transition-[box-shadow,transform] duration-200 ease-out',
          isUser
            ? '!border !border-[var(--color-border-2)] !bg-[var(--color-bg-2)] !shadow-sm'
            : isSystem
              ? '!border !border-[var(--color-border-2)] !bg-[var(--color-fill-1)] !shadow-sm !opacity-95'
              : '!border !border-[var(--color-border-2)] !bg-[var(--color-fill-2)] !shadow-sm',
        )}
        bodyStyle={{ padding: '10px 14px' }}
      >
        <Paragraph className="!m-0 !text-[14px] !leading-relaxed !text-[var(--color-text-1)]">
          {message.content}
        </Paragraph>
      </Card>
      {isUser && (
        <Avatar
          size={36}
          shape="circle"
          className="chat-msg-avatar chat-msg-avatar--user shrink-0"
        >
          <img src="/logo.png" alt="我" className="h-full w-full object-cover" />
        </Avatar>
      )}
    </div>
  )
}

function mapServerMessages(list: { id: string; role: string; content: string }[]): ChatMessage[] {
  return list.map((m) => ({
    id: m.id,
    role: m.role === 'user' || m.role === 'assistant' || m.role === 'system' ? m.role : 'assistant',
    content: m.content,
  }))
}

export function ChatPage() {
  const user = useAuthStore((s) => s.user)
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [sessionsLoading, setSessionsLoading] = useState(false)
  const [activeId, setActiveId] = useState<string | null>(null)
  const [sessionPanelOpen, setSessionPanelOpen] = useState(true)
  const [apiKey, setApiKeyState] = useState(() => readLocal(LS_OPENAPI_KEY))
  const [model, setModelState] = useState(() => readLocal(LS_CHAT_MODEL))

  const setApiKey = (v: string) => {
    setApiKeyState(v)
    try {
      localStorage.setItem(LS_OPENAPI_KEY, v)
    } catch {
      /* 隐私模式等可能失败 */
    }
  }
  const setModel = (v: string) => {
    setModelState(v)
    try {
      localStorage.setItem(LS_CHAT_MODEL, v)
    } catch {
      /* ignore */
    }
  }
  const [modelOptions, setModelOptions] = useState<{ label: string; value: string }[]>([])
  const [modelsLoading, setModelsLoading] = useState(false)
  const [modelsError, setModelsError] = useState<string | null>(null)
  const [sendLoading, setSendLoading] = useState(false)
  const chatAbortRef = useRef<AbortController | null>(null)
  const [draft, setDraft] = useState('')
  const [messagesBySession, setMessagesBySession] = useState<Record<string, ChatMessage[]>>({})

  const refreshCloudSessions = useCallback(async () => {
    if (!user) return
    setSessionsLoading(true)
    try {
      const list = await listChatSessions()
      const mapped: ChatSession[] = list.map((r) => ({
        id: r.id,
        title: r.title?.trim() || '新对话',
        timeLabel: formatTimeFromMs(r.updated_at),
        boundModel: r.model,
      }))
      setSessions(mapped)
      setActiveId((prev) => {
        if (prev && mapped.some((x) => x.id === prev)) return prev
        return mapped[0]?.id ?? null
      })
    } catch (e) {
      Message.error(e instanceof Error ? e.message : '加载会话失败')
      setSessions([])
      setActiveId(null)
    } finally {
      setSessionsLoading(false)
    }
  }, [user])

  useEffect(() => {
    if (!user) {
      setSessions([])
      setActiveId(null)
      setMessagesBySession({})
      return
    }
    void refreshCloudSessions()
  }, [user, refreshCloudSessions])

  useEffect(() => {
    if (!user || !activeId || sendLoading) return
    let cancelled = false
    void (async () => {
      try {
        const { list, session } = await listChatMessages(activeId)
        if (cancelled) return
        setMessagesBySession((prev) => ({
          ...prev,
          [activeId]: mapServerMessages(list),
        }))
        if (session.model) setModel(session.model)
      } catch (e) {
        if (!cancelled) {
          Message.error(e instanceof Error ? e.message : '加载消息失败')
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [user, activeId, sendLoading])

  useEffect(() => {
    const k = apiKey.trim()
    if (!k) {
      setModelOptions([])
      setModelsError(null)
      return
    }
    let cancelled = false
    setModelsLoading(true)
    setModelsError(null)
    void (async () => {
      try {
        const list = await fetchOpenAPIModels(k)
        if (cancelled) return
        const opts = list.map((m) => ({ label: m.id, value: m.id }))
        setModelOptions(opts)
        if (opts.length > 0 && !opts.some((o) => o.value === model)) {
          setModel(opts[0].value)
        }
      } catch (e) {
        if (!cancelled) {
          setModelOptions([])
          setModelsError(e instanceof Error ? e.message : '无法加载模型列表')
        }
      } finally {
        if (!cancelled) setModelsLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [apiKey])

  const activeSession = useMemo(
    () => (activeId ? sessions.find((s) => s.id === activeId) : undefined),
    [sessions, activeId],
  )
  const messages = activeId ? (messagesBySession[activeId] ?? []) : []

  const handleNewChat = () => {
    if (user) {
      void (async () => {
        try {
          const row = await createChatSession({
            title: '新对话',
            model: model.trim() || 'gpt-4o-mini',
            provider: 'openai',
          })
          const next: ChatSession = {
            id: row.id,
            title: row.title || '新对话',
            timeLabel: formatTimeFromMs(row.updated_at),
            boundModel: row.model,
          }
          setSessions((prev) => [next, ...prev.filter((s) => s.id !== next.id)])
          setActiveId(next.id)
          setMessagesBySession((prev) => ({ ...prev, [next.id]: [] }))
          if (row.model) setModel(row.model)
          setDraft('')
        } catch (e) {
          Message.error(e instanceof Error ? e.message : '创建会话失败')
        }
      })()
      return
    }
    const s = newSession()
    setSessions((prev) => [s, ...prev])
    setActiveId(s.id)
    setDraft('')
  }

  const handleClearSessions = () => {
    if (user) {
      void (async () => {
        try {
          await Promise.all(sessions.map((s) => deleteChatSession(s.id)))
          setSessions([])
          setActiveId(null)
          setMessagesBySession({})
          setDraft('')
          Message.success('已清空')
        } catch (e) {
          Message.error(e instanceof Error ? e.message : '清空失败')
        }
      })()
      return
    }
    setSessions([])
    setActiveId(null)
    setMessagesBySession({})
    setDraft('')
  }

  const ensureLocalSession = (): string => {
    if (activeId) return activeId
    const s = newSession()
    setSessions((prev) => [s, ...prev])
    setActiveId(s.id)
    return s.id
  }

  const handleSend = () => {
    const text = draft.trim()
    if (!text) return
    if (!apiKey.trim()) {
      Message.warning('请先在左侧会话栏最下方填写「OpenAPI 密钥」后再发送（密钥保存在本机浏览器）。')
      return
    }
    if (!model.trim()) {
      Message.warning('请选择模型；密钥有效时会自动从 /v1/models 加载')
      return
    }
    void (async () => {
      setSendLoading(true)
      try {
      let sid = activeId
      if (user) {
        if (!sid) {
          try {
            const row = await createChatSession({
              title: '新对话',
              model: model.trim(),
              provider: 'openai',
            })
            sid = row.id
            setSessions((prev) => [
              {
                id: row.id,
                title: row.title || '新对话',
                timeLabel: formatTimeFromMs(row.updated_at),
                boundModel: row.model,
              },
              ...prev.filter((x) => x.id !== row.id),
            ])
            setActiveId(row.id)
            setMessagesBySession((prev) => ({ ...prev, [row.id]: prev[row.id] ?? [] }))
          } catch (e) {
            Message.error(e instanceof Error ? e.message : '创建会话失败')
            return
          }
        }
      } else {
        sid = ensureLocalSession()
      }

      const userMsg: ChatMessage = {
        id: `m-${Date.now()}-u`,
        role: 'user',
        content: text,
      }
      const assistantId = `m-${Date.now()}-a`
      const assistantMsg: ChatMessage = { id: assistantId, role: 'assistant', content: '' }

      const historyBefore = [...(messagesBySession[sid] ?? [])]
      const apiMessages = [...historyBefore, userMsg].map((m) => ({ role: m.role, content: m.content }))

      setMessagesBySession((prev) => ({
        ...prev,
        [sid]: [...(prev[sid] ?? []), userMsg, assistantMsg],
      }))
      const newTitle = text.length > 28 ? `${text.slice(0, 28)}…` : text
      setSessions((prev) =>
        prev.map((s) => {
          if (s.id !== sid) return s
          if (s.title !== '新对话') return s
          return { ...s, title: newTitle, timeLabel: formatTimeLabel() }
        }),
      )
      setDraft('')

      if (user) {
        try {
          await appendChatMessage(sid, { role: 'user', content: text, model: model.trim(), provider: 'openai' })
          if (historyBefore.length === 0) {
            await patchChatSessionTitle(sid, newTitle)
            setSessions((p) => p.map((s) => (s.id === sid ? { ...s, title: newTitle } : s)))
          }
        } catch (e) {
          Message.error(e instanceof Error ? e.message : '保存用户消息失败')
          setMessagesBySession((prev) => {
            const list = prev[sid] ?? []
            return {
              ...prev,
              [sid]: list.filter((m) => m.id !== userMsg.id && m.id !== assistantId),
            }
          })
          return
        }
      }

      chatAbortRef.current?.abort()
      chatAbortRef.current = new AbortController()
      const signal = chatAbortRef.current.signal
      let assistantBody = ''
      try {
        await streamOpenAIChatCompletion({
          apiKey: apiKey.trim(),
          model: model.trim(),
          messages: apiMessages,
          signal,
          onDelta: (chunk) => {
            assistantBody += chunk
            setMessagesBySession((prev) => {
              const list = prev[sid] ?? []
              const next = list.map((m) =>
                m.id === assistantId ? { ...m, content: m.content + chunk } : m,
              )
              return { ...prev, [sid]: next }
            })
          },
        })
        if (user && assistantBody.trim()) {
          await appendChatMessage(sid, {
            role: 'assistant',
            content: assistantBody,
            model: model.trim(),
            provider: 'openai',
          })
          try {
            const { list } = await listChatMessages(sid)
            setMessagesBySession((prev) => ({ ...prev, [sid]: mapServerMessages(list) }))
          } catch {
            /* 仍以本地拼接为准 */
          }
        }
      } catch (e) {
        if (signal.aborted) return
        const msg = e instanceof Error ? e.message : '请求失败'
        Message.error(msg)
        setMessagesBySession((prev) => {
          const list = prev[sid] ?? []
          return { ...prev, [sid]: list.filter((m) => m.id !== assistantId) }
        })
      }
      } finally {
        setSendLoading(false)
      }
    })()
  }

  const onTextareaKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== 'Enter') return
    if (e.shiftKey || e.altKey || e.ctrlKey || e.metaKey) return
    e.preventDefault()
    handleSend()
  }

  const headerTitle = activeSession?.title ?? 'LingVoice'

  const modelDropdownEmpty = useMemo(
    () =>
      modelsLoading ? (
        <div className="chat-model-dropdown-empty chat-model-dropdown-empty--loading">加载模型列表中…</div>
      ) : (
        <div className="chat-model-dropdown-empty">
          <p className="chat-model-dropdown-empty__title">暂无可用模型</p>
          <p className="chat-model-dropdown-empty__hint">
            请确认 OpenAPI 密钥正确，或在控制台为该凭证配置「OpenAPI 模型目录」、并为分组配置 LLM 渠道的模型列表。
          </p>
        </div>
      ),
    [modelsLoading],
  )

  return (
    <Layout className="chat-page-root !h-full !min-h-0 !min-w-0 !flex-1 !bg-[var(--color-bg-1)]">
      <Layout.Sider
        collapsible={false}
        width={sessionPanelOpen ? 272 : 0}
        style={{
          overflow: sessionPanelOpen ? 'visible' : 'hidden',
          transition: 'width 0.22s cubic-bezier(0.34, 0.69, 0.1, 1)',
          borderRight: sessionPanelOpen ? '1px solid var(--color-border-2)' : 'none',
          background: 'var(--color-bg-1)',
        }}
        className="!min-h-0"
      >
        <div
          className={cn(
            'relative h-full w-[272px]',
            !sessionPanelOpen && 'pointer-events-none invisible',
          )}
        >
          <div className="flex h-full flex-col">
            <Header className="!h-auto !leading-none !border-b !border-[var(--color-border-2)] !bg-[var(--color-bg-1)] !px-3 !py-2.5">
              <Title heading={6} className="!m-0 !text-[15px] !font-semibold">
                聊天
              </Title>
              {!user ? (
                <Text type="secondary" className="!mt-1 !block !text-[11px] !leading-snug">
                  <Link to="/login" className="text-[rgb(var(--primary-6))] underline-offset-2 hover:underline">
                    登录
                  </Link>
                  后可把会话与消息同步到云端
                </Text>
              ) : null}
            </Header>

            <div className="flex min-w-0 shrink-0 items-center gap-2 px-3 pb-2 pt-2">
              <Button
                type="primary"
                size="small"
                long
                className="!min-w-[128px] !flex-1"
                icon={<MessageSquarePlus size={16} strokeWidth={1.75} />}
                onClick={handleNewChat}
              >
                新对话
              </Button>
              <Popconfirm
                title="清空全部会话？"
                content={
                  user
                    ? '将删除服务器上的全部会话与消息，不可恢复。'
                    : '本地会话与消息将被清除。'
                }
                okText="清空"
                cancelText="取消"
                onOk={handleClearSessions}
              >
                <Button
                  type="secondary"
                  size="small"
                  status="danger"
                  className="!shrink-0"
                  aria-label="清空会话列表"
                  icon={<Trash2 size={16} strokeWidth={1.75} />}
                />
              </Popconfirm>
            </div>

            <Content className="chat-session-list !min-h-0 !flex-1 !overflow-y-auto !bg-[var(--color-bg-1)] !px-0 !pb-2 !pt-0">
              {sessionsLoading ? (
                <div className="flex justify-center py-10">
                  <Text type="secondary">加载会话…</Text>
                </div>
              ) : sessions.length === 0 ? (
                <Empty
                  className="!py-8"
                  description={
                    <Space direction="vertical" size={4}>
                      <Text type="secondary">暂无会话</Text>
                      <Text type="secondary" className="!text-[12px]">
                        点击「新对话」开始
                      </Text>
                    </Space>
                  }
                />
              ) : (
                <div className="flex flex-col" role="list">
                  {sessions.map((item) => {
                    const active = item.id === activeId
                    return (
                      <button
                        key={item.id}
                        type="button"
                        role="listitem"
                        onClick={() => {
                          setActiveId(item.id)
                          if (item.boundModel) setModel(item.boundModel)
                        }}
                        className={cn(
                          'chat-session-line flex w-full items-baseline justify-between gap-2 border-0 border-b border-solid border-[var(--color-border-2)] bg-transparent px-3 py-2.5 text-left outline-none transition-colors duration-100',
                          active
                            ? 'bg-[var(--color-fill-2)]'
                            : 'hover:bg-[var(--color-fill-1)]',
                        )}
                      >
                        <Text
                          ellipsis
                          className={cn(
                            '!mb-0 min-w-0 flex-1 text-[13px] leading-snug text-[var(--color-text-1)]',
                            active ? '!font-medium' : '!font-normal',
                          )}
                        >
                          {item.title}
                        </Text>
                        <Text
                          type="secondary"
                          className="!mb-0 shrink-0 text-[12px] tabular-nums leading-none"
                        >
                          {item.timeLabel}
                        </Text>
                      </button>
                    )
                  })}
                </div>
              )}
            </Content>

            <Footer className="!h-auto !flex-none !border-t !border-[var(--color-border-2)] !bg-[var(--color-bg-1)] !px-3 !py-2.5">
              <Space direction="vertical" size={6} className="!w-full">
                <Text type="secondary" className="!block !text-[11px] !leading-snug">
                  OpenAPI 密钥（Bearer）仅保存在本机，刷新后仍保留
                </Text>
                <Input.Password
                  size="small"
                  value={apiKey}
                  onChange={setApiKey}
                  placeholder="粘贴 LLM 凭证密钥"
                  className="font-mono text-[11px]"
                  autoComplete="off"
                />
              </Space>
            </Footer>
          </div>

          {/* 贴中缝竖条：hover 显示半圆拉手（收起） */}
          <div
            className="group/chat-collapse-edge pointer-events-auto absolute inset-y-0 z-30 w-14 bg-transparent"
            style={{ right: -12, top: 0, bottom: 0 }}
            aria-hidden
          >
            <button
              type="button"
              className="chat-sider-edge-tab chat-sider-edge-tab--collapse pointer-events-none opacity-0 transition-[opacity,transform] duration-200 ease-out group-hover/chat-collapse-edge:pointer-events-auto group-hover/chat-collapse-edge:opacity-100"
              aria-label="收起会话列表"
              onClick={() => setSessionPanelOpen(false)}
            >
              <ChevronLeft size={18} strokeWidth={2.25} className="text-white" aria-hidden />
            </button>
          </div>
        </div>
      </Layout.Sider>

      <Layout className="relative !min-h-0 !min-w-0 !flex-1 !bg-[var(--color-fill-1)]">
        {/* 会话栏收起后：主区左缘窄条 hover 才出现展开 */}
        {!sessionPanelOpen && (
          <div
            className="group/chat-expand-edge pointer-events-auto absolute z-40 h-full w-14 bg-transparent"
            style={{ left: -12, top: 0 }}
            aria-hidden
          >
            <button
              type="button"
              className="chat-sider-edge-tab chat-sider-edge-tab--expand pointer-events-none opacity-0 transition-[opacity,transform] duration-200 ease-out group-hover/chat-expand-edge:pointer-events-auto group-hover/chat-expand-edge:opacity-100"
              aria-label="展开会话列表"
              onClick={() => setSessionPanelOpen(true)}
            >
              <ChevronRight size={18} strokeWidth={2.25} className="text-white" aria-hidden />
            </button>
          </div>
        )}

        <Header className="!h-auto !border-b !border-[var(--color-border-2)] !bg-[var(--color-bg-1)] !px-4 !py-3 !text-center">
          <Title heading={6} className="!m-0 !text-[14px] !font-semibold">
            {headerTitle}
          </Title>
        </Header>

        <Content className="!flex !min-h-0 !flex-1 !flex-col !overflow-hidden !bg-[var(--color-fill-1)] !p-0">
          {messages.length === 0 ? (
            <div className="flex min-h-0 flex-1 flex-col items-center justify-center px-6 pb-24">
              <Empty
                icon={
                  <img
                    src="/logo.png"
                    alt=""
                    className="mx-auto h-[52px] w-[52px] rounded-2xl object-contain shadow-md"
                  />
                }
                description={
                  <Space direction="vertical" size={12} className="!mt-2">
                    <Title heading={4} className="!m-0">
                      LingVoice
                    </Title>
                  </Space>
                }
              />
            </div>
          ) : (
            <div className="chat-msg-scroll min-h-0 flex-1 overflow-y-auto px-4 py-4">
              <div className="mx-auto flex w-full max-w-[720px] flex-col gap-3">
                {messages.map((m) => (
                  <ChatMessageRow key={m.id} message={m} />
                ))}
              </div>
            </div>
          )}
        </Content>

        <Footer className="!h-auto !flex-none !border-0 !bg-transparent !p-0">
          <div className="px-4 pb-2 pt-1">
            <Card
              bordered
              className="!max-w-[720px] !mx-auto !rounded-[20px] !border-[var(--color-border-2)] !shadow-sm !transition-shadow duration-200 hover:!shadow-md"
              bodyStyle={{ padding: '12px 14px 10px' }}
            >
              <TextArea
                value={draft}
                onChange={setDraft}
                onKeyDown={onTextareaKeyDown}
                placeholder="输入问题，发送 [Enter] / 换行 [Shift + Enter]"
                autoSize={{ minRows: 2, maxRows: 8 }}
                className="!resize-none !border-none !bg-transparent !p-0 !text-[14px] focus:!shadow-none"
              />
              <Divider className="!my-2" />
              {!apiKey.trim() ? (
                <Alert
                  type="warning"
                  className="!mb-2"
                  content="请先在左侧会话栏最底部填写 OpenAPI 密钥后再对话。"
                />
              ) : null}
              <Space direction="vertical" size={6} className="!w-full">
                {modelsError ? (
                  <Text type="error" className="!block !text-[12px]">
                    {modelsError}
                  </Text>
                ) : null}
                <Space align="center" className="!w-full" style={{ justifyContent: 'space-between' }}>
                <Select
                  size="small"
                  value={model || undefined}
                  onChange={(v) => setModel(v == null ? '' : String(v))}
                  options={modelOptions}
                  loading={modelsLoading}
                  placeholder={apiKey.trim() ? '选择模型' : '填写密钥后加载'}
                  notFoundContent={modelDropdownEmpty}
                  triggerProps={{ updateOnScroll: true }}
                  className="chat-model-select min-w-0"
                  style={{ width: 200 }}
                />
                <Space size={4}>
                  <Button
                    type="secondary"
                    size="small"
                    shape="round"
                    aria-label="附件"
                    icon={<Paperclip size={17} strokeWidth={1.75} />}
                  />
                  <Button
                    type="secondary"
                    size="small"
                    shape="round"
                    aria-label="语音输入"
                    icon={<Mic size={17} strokeWidth={1.75} />}
                  />
                  <Button
                    type="primary"
                    shape="circle"
                    size="small"
                    aria-label="发送"
                    icon={<Send size={17} strokeWidth={1.75} />}
                    loading={sendLoading}
                    onClick={handleSend}
                  />
                </Space>
              </Space>
              </Space>
            </Card>
          </div>
          <div className="px-4 pb-3">
            <Text type="secondary" className="!block !text-center !text-[11px] !leading-relaxed">
              内容由 AI 生成，仅供参考，请自行核实重要信息
            </Text>
          </div>
        </Footer>
      </Layout>
    </Layout>
  )
}
