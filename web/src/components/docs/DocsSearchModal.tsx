import { Empty, Input, List, Modal, Space, Typography } from '@arco-design/web-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { CornerDownLeft, Search } from 'lucide-react'
import type { DocsSearchEntry } from '@/components/docs/types'
import { cn } from '@/lib/cn'

const { Text } = Typography

export interface DocsSearchModalProps {
  visible: boolean
  onClose: () => void
  entries: DocsSearchEntry[]
  onPick: (id: string) => void
}

export function DocsSearchModal({ visible, onClose, entries, onPick }: DocsSearchModalProps) {
  const [q, setQ] = useState('')
  const [active, setActive] = useState(0)

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase()
    if (!s) return entries.slice(0, 60)
    return entries
      .filter((e) => {
        const hay = `${e.label} ${e.subtitle} ${e.keywords}`.toLowerCase()
        return hay.includes(s)
      })
      .slice(0, 60)
  }, [entries, q])

  useEffect(() => {
    if (visible) {
      setQ('')
      setActive(0)
    }
  }, [visible])

  useEffect(() => {
    setActive((i) => Math.min(i, Math.max(0, filtered.length - 1)))
  }, [filtered.length, q])

  const pick = useCallback(
    (idx: number) => {
      if (filtered.length === 0) return
      const e = filtered[Math.min(idx, filtered.length - 1)]
      if (!e) return
      onPick(e.id)
      onClose()
    },
    [filtered, onPick, onClose],
  )

  useEffect(() => {
    if (!visible) return
    const onKey = (ev: KeyboardEvent) => {
      if ((ev.metaKey || ev.ctrlKey) && ev.key.toLowerCase() === 'k') {
        ev.preventDefault()
        onClose()
      }
      if (ev.key === 'Escape') {
        ev.preventDefault()
        onClose()
      }
      if (ev.key === 'ArrowDown') {
        ev.preventDefault()
        setActive((i) => Math.min(i + 1, Math.max(0, filtered.length - 1)))
      }
      if (ev.key === 'ArrowUp') {
        ev.preventDefault()
        setActive((i) => Math.max(i - 1, 0))
      }
      if (ev.key === 'Enter') {
        ev.preventDefault()
        pick(active)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [visible, filtered.length, active, pick, onClose])

  return (
    <Modal
      title={null}
      footer={null}
      visible={visible}
      onCancel={onClose}
      wrapClassName="docs-command-palette"
      style={{ width: 520 }}
      maskClosable
      closable={false}
      alignCenter
    >
      <div className="flex flex-col bg-[var(--color-bg-2)]">
        <div className="border-b border-[var(--color-border-2)] p-3">
          <Input
            value={q}
            onChange={setQ}
            placeholder="搜索页面标题、分组、接口路径…"
            prefix={<Search size={18} className="text-[var(--color-text-3)]" />}
            allowClear
            size="large"
            autoFocus={visible}
          />
        </div>

        <div className="flex items-center justify-between gap-3 border-b border-[var(--color-border-2)] px-3 py-2">
          <Text type="secondary" className="!m-0 !text-[11px]">
            共 {filtered.length} 条
          </Text>
          <Space size={4} className="!text-[11px] text-[var(--color-text-3)]">
            <Text type="secondary" className="!m-0 !rounded !bg-[var(--color-fill-3)] !px-1.5 !py-0.5 !font-mono !text-[10px]">
              ↑↓
            </Text>
            <Text type="secondary" className="!m-0">
              移动
            </Text>
            <CornerDownLeft size={14} className="opacity-70" />
            <Text type="secondary" className="!m-0">
              打开
            </Text>
            <Text type="secondary" className="!m-0 !rounded !bg-[var(--color-fill-3)] !px-1.5 !py-0.5 !font-mono !text-[10px]">
              esc
            </Text>
            <Text type="secondary" className="!m-0">
              关闭
            </Text>
          </Space>
        </div>

        <div className="max-h-[min(380px,48vh)] overflow-y-auto p-2">
          {filtered.length === 0 ? (
            <Empty description="无匹配结果" className="!py-10" />
          ) : (
            <List
              size="small"
              split={false}
              dataSource={filtered}
              className="docs-command-list !border-0 !bg-transparent"
              render={(e, idx) => (
                <List.Item
                  key={e.id}
                  className={cn(
                    '!cursor-pointer !rounded-lg !border-0 !px-2 !py-1 transition-colors',
                    idx === active ? '!bg-[var(--color-fill-2)]' : 'hover:!bg-[var(--color-fill-1)]',
                  )}
                  onMouseEnter={() => setActive(idx)}
                  onClick={() => pick(idx)}
                >
                  <List.Item.Meta
                    title={<span className="text-[14px] font-medium text-[var(--color-text-1)]">{e.label}</span>}
                    description={
                      <Text type="secondary" className="!m-0 !text-[12px]">
                        {e.subtitle}
                      </Text>
                    }
                  />
                </List.Item>
              )}
            />
          )}
        </div>
      </div>
    </Modal>
  )
}
