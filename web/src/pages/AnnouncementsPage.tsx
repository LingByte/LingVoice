import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { AnnouncementsModal } from '@/components/announcements/AnnouncementsModal'

/** 独立路由：以弹窗形式展示公告，关闭后回到首页。 */
export function AnnouncementsPage() {
  const navigate = useNavigate()
  const [open, setOpen] = useState(true)

  const close = () => {
    setOpen(false)
    navigate('/', { replace: true })
  }

  return (
    <div className="h-full min-h-0 flex-1 bg-[var(--color-fill-1)]" aria-hidden>
      <AnnouncementsModal visible={open} onClose={close} />
    </div>
  )
}
