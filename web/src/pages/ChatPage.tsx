import type { KeyboardEvent } from 'react'
import { useMemo, useState } from 'react'
import {
  Avatar,
  Button,
  Card,
  Divider,
  Empty,
  Input,
  Layout,
  Popconfirm,
  Select,
  Space,
  Typography,
} from '@arco-design/web-react'
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
}

type ChatMessage = { id: string; role: 'user' | 'assistant'; content: string }

const MODEL_OPTIONS = [
  { label: 'qwen2.5-plus', value: 'qwen2.5-plus' },
  { label: 'gpt-4o', value: 'gpt-4o' },
]

function formatTimeLabel() {
  return new Date().toLocaleTimeString('zh-CN', {
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
  return (
    <div
      className={cn(
        'chat-msg-row',
        isUser ? 'chat-msg-row--user' : 'chat-msg-row--assistant',
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

export function ChatPage() {
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [activeId, setActiveId] = useState<string | null>(null)
  const [sessionPanelOpen, setSessionPanelOpen] = useState(true)
  const [model, setModel] = useState('qwen2.5-plus')
  const [draft, setDraft] = useState('')
  const [messagesBySession, setMessagesBySession] = useState<Record<string, ChatMessage[]>>({})

  const activeSession = useMemo(
    () => (activeId ? sessions.find((s) => s.id === activeId) : undefined),
    [sessions, activeId],
  )
  const messages = activeId ? (messagesBySession[activeId] ?? []) : []

  const handleNewChat = () => {
    const s = newSession()
    setSessions((prev) => [s, ...prev])
    setActiveId(s.id)
    setDraft('')
  }

  const handleClearSessions = () => {
    setSessions([])
    setActiveId(null)
    setMessagesBySession({})
    setDraft('')
  }

  const ensureActiveSession = (): string => {
    if (activeId) return activeId
    const s = newSession()
    setSessions((prev) => [s, ...prev])
    setActiveId(s.id)
    return s.id
  }

  const handleSend = () => {
    const text = draft.trim()
    if (!text) return
    const sid = ensureActiveSession()
    const userMsg: ChatMessage = {
      id: `m-${Date.now()}-u`,
      role: 'user',
      content: text,
    }
    setMessagesBySession((prev) => ({
      ...prev,
      [sid]: [...(prev[sid] ?? []), userMsg],
    }))
    setSessions((prev) =>
      prev.map((s) => {
        if (s.id !== sid) return s
        if (s.title !== '新对话') return s
        return { ...s, title: text.length > 28 ? `${text.slice(0, 28)}…` : text }
      }),
    )
    setDraft('')
  }

  const onTextareaKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== 'Enter') return
    if (e.shiftKey || e.altKey || e.ctrlKey || e.metaKey) return
    e.preventDefault()
    handleSend()
  }

  const headerTitle = activeSession?.title ?? 'LingVoice'

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
                content="本地会话与消息将被清除。"
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
              {sessions.length === 0 ? (
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
                        onClick={() => setActiveId(item.id)}
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

            <Footer className="!h-auto !flex-none !border-t !border-[var(--color-border-2)] !bg-[var(--color-bg-1)] !px-3 !py-2">
              <Text type="secondary" className="!block !text-center !text-[11px]">
                已加载全部
              </Text>
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
              <Space align="center" className="!w-full" style={{ justifyContent: 'space-between' }}>
                <Select
                  size="small"
                  value={model}
                  onChange={(v) => setModel(String(v))}
                  options={MODEL_OPTIONS}
                  className="chat-model-select min-w-0"
                  style={{ width: 160 }}
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
                    onClick={handleSend}
                  />
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
