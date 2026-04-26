import { Button, Message, Modal, Spin, Tag, Typography } from '@arco-design/web-react'
import { Bell, Megaphone } from 'lucide-react'
import type { ReactNode } from 'react'
import { useCallback, useEffect, useState } from 'react'
import { listPublicAnnouncements, type SiteAnnouncement } from '@/api/announcements'
import {
  listMyInternalNotifications,
  markInternalNotificationRead,
  type InternalNotificationRow,
} from '@/api/internalNotifications'
import { cn } from '@/lib/cn'
import { useAuthStore } from '@/stores/authStore'
import { dismissMessageCenterForToday } from '@/utils/messageCenterDismiss'

const { Paragraph, Text } = Typography

type TabKey = 'notifications' | 'announcements'

function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message
  const o = e as { msg?: string }
  if (o && typeof o.msg === 'string') return o.msg
  return '加载失败'
}

function fmtTime(iso: string): string {
  if (!iso) return '—'
  try {
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return iso
    return d.toLocaleString()
  } catch {
    return iso
  }
}

function fmtRelative(iso: string): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return ''
  const diff = Date.now() - t
  if (diff < 45_000) return '刚刚'
  const min = Math.floor(diff / 60_000)
  if (min < 60) return `${min} 分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr} 小时前`
  const day = Math.floor(hr / 24)
  if (day < 30) return `${day} 天前`
  const mon = Math.floor(day / 30)
  if (mon < 12) return `${mon} 个月前`
  return `${Math.floor(day / 365)} 年前`
}

function TabPill(props: {
  active: boolean
  icon: ReactNode
  label: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={props.onClick}
      className={cn(
        'relative inline-flex min-h-[32px] items-center gap-1.5 rounded-[10px] px-3.5 py-1.5 text-[13px] font-medium transition-all duration-200',
        'border-0 outline-none focus-visible:ring-2 focus-visible:ring-[rgba(var(--primary-5),0.45)] focus-visible:ring-offset-1 focus-visible:ring-offset-[var(--color-fill-3)]',
        props.active
          ? 'bg-[var(--color-bg-1)] text-[rgb(var(--primary-6))] shadow-[0_1px_4px_rgba(0,0,0,0.07)]'
          : 'text-[var(--color-text-2)] hover:bg-[var(--color-fill-2)] hover:text-[var(--color-text-1)]',
      )}
    >
      {props.icon}
      {props.label}
    </button>
  )
}

function TimelineList(props: { children: ReactNode }) {
  return (
    <div className="relative pl-0">
      <div
        className="pointer-events-none absolute left-[9px] top-3 bottom-3 w-px bg-[var(--color-border-2)]"
        aria-hidden
      />
      {props.children}
    </div>
  )
}

function TimelineRow(props: { dotClass: string; children: ReactNode }) {
  return (
    <div className="relative flex gap-3 pb-8 last:pb-2">
      <div
        className={cn(
          'relative z-[1] mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full border-2 border-[var(--color-bg-1)]',
          props.dotClass,
        )}
        aria-hidden
      />
      <div className="min-w-0 flex-1">{props.children}</div>
    </div>
  )
}

function EmptyAnnouncements() {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
      <div className="mb-5 flex h-[88px] w-[88px] items-center justify-center rounded-2xl bg-[var(--color-fill-2)] text-[var(--color-text-3)]">
        <Megaphone size={44} strokeWidth={1.35} />
      </div>
      <Text bold className="mb-2 block text-[16px] text-[var(--color-text-1)]">
        暂无公告
      </Text>
      <Paragraph type="secondary" className="!mb-0 max-w-[380px] !text-[13px] !leading-relaxed">
        管理员在后台发布并启用站点公告后，将在此以时间线展示。内容可包含维护窗口、功能更新与使用说明等。
      </Paragraph>
    </div>
  )
}

function EmptyNotificationsGuest() {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
      <div className="mb-5 flex h-[88px] w-[88px] items-center justify-center rounded-2xl bg-[var(--color-fill-2)] text-[var(--color-text-3)]">
        <Bell size={40} strokeWidth={1.35} />
      </div>
      <Text bold className="mb-2 block text-[16px] text-[var(--color-text-1)]">
        登录后查看个人通知
      </Text>
      <Paragraph type="secondary" className="!mb-0 max-w-[380px] !text-[13px] !leading-relaxed">
        站内通知由系统或管理员下发至你的账号，与全站公告不同。请先登录后再打开本页签。
      </Paragraph>
    </div>
  )
}

function EmptyNotificationsInbox() {
  return (
    <div className="flex flex-col items-center justify-center px-6 py-16 text-center">
      <div className="mb-5 flex h-[88px] w-[88px] items-center justify-center rounded-2xl bg-[var(--color-fill-2)] text-[var(--color-text-3)]">
        <Bell size={40} strokeWidth={1.35} />
      </div>
      <Text bold className="mb-2 block text-[16px] text-[var(--color-text-1)]">
        暂无通知
      </Text>
      <Paragraph type="secondary" className="!mb-0 max-w-[380px] !text-[13px] !leading-relaxed">
        当前没有新的站内通知。有新消息时会出现在此时间线中。
      </Paragraph>
    </div>
  )
}

type Props = {
  visible: boolean
  onClose: () => void
}

/** 系统消息中心：通知（站内信，需登录）+ 系统公告（公开）。与 /announcements 路由共用。 */
export function AnnouncementsModal(props: Props) {
  const { visible, onClose } = props
  const authUser = useAuthStore((s) => s.user)
  const isLoggedIn = Boolean(authUser?.id)

  const [tab, setTab] = useState<TabKey>('announcements')
  const [loadingAnn, setLoadingAnn] = useState(false)
  const [loadingNotif, setLoadingNotif] = useState(false)
  const [annList, setAnnList] = useState<SiteAnnouncement[]>([])
  const [notifList, setNotifList] = useState<InternalNotificationRow[]>([])

  const loadAnnouncements = useCallback(async () => {
    setLoadingAnn(true)
    try {
      const rows = await listPublicAnnouncements()
      setAnnList(Array.isArray(rows) ? rows : [])
    } catch (e) {
      Message.error(errMsg(e))
      setAnnList([])
    } finally {
      setLoadingAnn(false)
    }
  }, [])

  const loadNotifications = useCallback(async () => {
    if (!isLoggedIn) {
      setNotifList([])
      return
    }
    setLoadingNotif(true)
    try {
      const data = await listMyInternalNotifications(1, 50)
      setNotifList(Array.isArray(data.list) ? data.list : [])
    } catch (e) {
      Message.error(errMsg(e))
      setNotifList([])
    } finally {
      setLoadingNotif(false)
    }
  }, [isLoggedIn])

  // 打开时预拉两路数据，切换页签不再触发加载，避免 Spin 闪屏
  useEffect(() => {
    if (!visible) return
    void loadAnnouncements()
    if (isLoggedIn) void loadNotifications()
    else setNotifList([])
  }, [visible, isLoggedIn, loadAnnouncements, loadNotifications])

  useEffect(() => {
    if (!visible) setTab('announcements')
  }, [visible])

  const onDismissToday = () => {
    dismissMessageCenterForToday()
    Message.success('今日内将不再提示（若已接入自动弹出）')
    onClose()
  }

  const onNotifRowClick = async (row: InternalNotificationRow) => {
    if (row.read) return
    try {
      await markInternalNotificationRead(row.id, true)
      setNotifList((prev) => prev.map((n) => (n.id === row.id ? { ...n, read: true } : n)))
    } catch (e) {
      Message.error(errMsg(e))
    }
  }

  const modalTitle = (
    <div className="flex w-full min-w-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4 sm:pr-1">
      <Text bold className="shrink-0 text-[16px] tracking-tight text-[var(--color-text-1)]">
        系统消息
      </Text>
      <div
        className="inline-flex w-fit max-w-full shrink-0 items-center gap-0.5 rounded-2xl bg-[var(--color-fill-3)] p-1 shadow-[inset_0_1px_2px_rgba(0,0,0,0.04)] dark:shadow-[inset_0_1px_2px_rgba(0,0,0,0.2)]"
        role="tablist"
        aria-label="消息类型"
      >
        <TabPill
          active={tab === 'notifications'}
          icon={<Bell size={14} strokeWidth={2} className={tab === 'notifications' ? '' : 'opacity-70'} />}
          label="通知"
          onClick={() => setTab('notifications')}
        />
        <TabPill
          active={tab === 'announcements'}
          icon={<Megaphone size={14} strokeWidth={2} className={tab === 'announcements' ? '' : 'opacity-70'} />}
          label="系统公告"
          onClick={() => setTab('announcements')}
        />
      </div>
    </div>
  )

  const footer = (
    <div className="flex w-full flex-wrap items-center justify-end gap-2">
      <Button onClick={onDismissToday}>今日关闭</Button>
      <Button type="primary" onClick={onClose}>
        关闭公告
      </Button>
    </div>
  )

  return (
    <Modal
      title={modalTitle}
      visible={visible}
      onCancel={onClose}
      footer={footer}
      maskClosable
      closable={false}
      unmountOnExit
      style={{ width: 'min(880px, 94vw)' }}
      className="message-center-modal"
    >
      <div className="max-h-[min(560px,68vh)] overflow-y-auto overflow-x-hidden pr-1">
        {/* 同格叠层 + 透明度过渡，避免切换时整段 DOM 挂载/卸载造成闪屏 */}
        <div className="relative grid grid-cols-1">
          <div
            className={cn(
              'col-start-1 row-start-1 min-w-0 transition-[opacity,visibility] duration-300 ease-in-out motion-reduce:transition-none',
              tab === 'announcements'
                ? 'visible z-[1] opacity-100'
                : 'invisible z-0 opacity-0 pointer-events-none',
            )}
            aria-hidden={tab !== 'announcements'}
          >
            <Spin loading={loadingAnn && annList.length === 0} className="block min-h-[120px]">
              {!loadingAnn && annList.length === 0 ? (
                <EmptyAnnouncements />
              ) : annList.length > 0 ? (
                <TimelineList>
                  {annList.map((a) => (
                    <TimelineRow
                      key={a.id}
                      dotClass="bg-[rgb(var(--success-6))] shadow-[0_0_0_3px_rgba(var(--success-2),0.35)]"
                    >
                      <div className="rounded-lg border border-[var(--color-border-2)] bg-[var(--color-fill-1)] p-4">
                        <div className="mb-1 flex flex-wrap items-center gap-2">
                          <Text bold className="text-[15px]">
                            {a.title}
                          </Text>
                          {a.pinned ? (
                            <Tag size="small" color="orangered">
                              置顶
                            </Tag>
                          ) : null}
                        </div>
                        <Text type="secondary" className="mb-2 block text-[12px]">
                          <span className="text-[var(--color-text-3)]">{fmtRelative(a.updated_at || a.created_at)}</span>
                          <span className="mx-1.5 text-[var(--color-border-3)]">|</span>
                          {fmtTime(a.updated_at || a.created_at)}
                        </Text>
                        {a.body?.trim() ? (
                          <Paragraph className="!mb-0 whitespace-pre-wrap !text-[13px] !leading-relaxed">
                            {a.body}
                          </Paragraph>
                        ) : (
                          <Paragraph type="secondary" className="!mb-0">
                            （无正文）
                          </Paragraph>
                        )}
                      </div>
                    </TimelineRow>
                  ))}
                </TimelineList>
              ) : null}
            </Spin>
          </div>

          <div
            className={cn(
              'col-start-1 row-start-1 min-w-0 transition-[opacity,visibility] duration-300 ease-in-out motion-reduce:transition-none',
              tab === 'notifications'
                ? 'visible z-[1] opacity-100'
                : 'invisible z-0 opacity-0 pointer-events-none',
            )}
            aria-hidden={tab !== 'notifications'}
          >
            {!isLoggedIn ? (
              <EmptyNotificationsGuest />
            ) : (
              <Spin loading={loadingNotif && notifList.length === 0} className="block min-h-[120px]">
                {!loadingNotif && notifList.length === 0 ? (
                  <EmptyNotificationsInbox />
                ) : notifList.length > 0 ? (
                  <TimelineList>
                    {notifList.map((n) => (
                      <TimelineRow
                        key={n.id}
                        dotClass={
                          n.read
                            ? 'bg-[var(--color-fill-4)]'
                            : 'bg-[rgb(var(--primary-6))] shadow-[0_0_0_3px_rgba(var(--primary-2),0.45)]'
                        }
                      >
                        <button
                          type="button"
                          className={cn(
                            'w-full rounded-lg border border-[var(--color-border-2)] bg-[var(--color-fill-1)] p-4 text-left transition-colors',
                            !n.read && 'cursor-pointer hover:border-[rgb(var(--primary-4))] hover:bg-[var(--color-fill-2)]',
                          )}
                          onClick={() => void onNotifRowClick(n)}
                        >
                          <div className="mb-1 flex flex-wrap items-center gap-2">
                            <Text bold className="text-[15px]">
                              {n.title}
                            </Text>
                            {!n.read ? (
                              <Tag size="small" color="arcoblue">
                                未读
                              </Tag>
                            ) : null}
                          </div>
                          <Text type="secondary" className="mb-2 block text-[12px]">
                            <span className="text-[var(--color-text-3)]">{fmtRelative(n.createdAt)}</span>
                            <span className="mx-1.5 text-[var(--color-border-3)]">|</span>
                            {fmtTime(n.createdAt)}
                          </Text>
                          <Paragraph className="!mb-0 whitespace-pre-wrap !text-[13px] !leading-relaxed">
                            {n.content}
                          </Paragraph>
                        </button>
                      </TimelineRow>
                    ))}
                  </TimelineList>
                ) : null}
              </Spin>
            )}
          </div>
        </div>
      </div>
    </Modal>
  )
}
