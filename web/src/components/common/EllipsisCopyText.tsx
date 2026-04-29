import { Message, Tooltip, Typography } from '@arco-design/web-react'
import type { ReactNode } from 'react'

type Props = {
  text?: string | number | null
  /** tooltip里是否允许换行展示完整内容 */
  wrapInTooltip?: boolean
  /** 空值占位 */
  emptyText?: ReactNode
  /** 点击复制成功提示 */
  copiedTip?: string
  /** 是否启用点击复制 */
  copyable?: boolean
  /** cell 最大宽度（配合 Table column width 使用） */
  maxWidth?: number | string
  className?: string
}

async function copyToClipboard(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    try {
      const el = document.createElement('textarea')
      el.value = text
      el.style.position = 'fixed'
      el.style.left = '-9999px'
      document.body.appendChild(el)
      el.focus()
      el.select()
      const ok = document.execCommand('copy')
      document.body.removeChild(el)
      return ok
    } catch {
      return false
    }
  }
}

export function EllipsisCopyText(props: Props) {
  const {
    text,
    wrapInTooltip = true,
    emptyText = '—',
    copiedTip = '已复制',
    copyable = true,
    maxWidth,
    className,
  } = props

  const s = text === null || text === undefined ? '' : String(text)
  if (!s.trim()) return <span className={className}>{emptyText}</span>

  const content = (
    <Typography.Text
      className={className}
      style={{
        maxWidth,
        display: 'inline-block',
        whiteSpace: 'nowrap',
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        verticalAlign: 'bottom',
        cursor: copyable ? 'pointer' : undefined,
      }}
      onClick={async () => {
        if (!copyable) return
        const ok = await copyToClipboard(s)
        if (ok) Message.success(copiedTip)
        else Message.error('复制失败')
      }}
    >
      {s}
    </Typography.Text>
  )

  if (!wrapInTooltip) return content

  return (
    <Tooltip
      position="top"
      content={
        <div
          style={{
            maxWidth: 520,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            lineHeight: 1.5,
          }}
        >
          {s}
        </div>
      }
    >
      {content}
    </Tooltip>
  )
}

