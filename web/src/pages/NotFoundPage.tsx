import { Button, Card, Typography } from '@arco-design/web-react'
import { Compass, Home, User } from 'lucide-react'
import { useNavigate } from 'react-router-dom'

const { Title, Paragraph, Text } = Typography

export function NotFoundPage() {
  const navigate = useNavigate()

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col items-center justify-center overflow-auto px-4 py-10">
      <div className="flex w-full max-w-[920px] min-w-0 flex-col gap-8 md:flex-row md:items-stretch md:gap-10">
        <div className="flex flex-1 flex-col items-center justify-center md:items-start md:justify-center">
          <div
            className="relative mb-4 flex h-36 w-36 shrink-0 items-center justify-center rounded-3xl border border-[var(--color-border-2)] bg-[var(--color-bg-2)] shadow-sm md:h-44 md:w-44"
            aria-hidden
          >
            <div className="absolute inset-3 rounded-2xl bg-[var(--color-fill-2)]" />
            <span className="relative text-[56px] font-bold leading-none tracking-tight text-[var(--color-text-1)] opacity-[0.92] md:text-[64px]">
              404
            </span>
            <Compass
              className="absolute -right-1 -top-1 h-9 w-9 text-[rgb(var(--primary-6))] opacity-90 drop-shadow-sm"
              strokeWidth={1.5}
            />
          </div>
          <Title heading={4} className="!mb-2 text-center md:!text-left">
            页面走丢了
          </Title>
          <Paragraph type="secondary" className="!mb-0 max-w-md text-center text-[14px] leading-relaxed md:!text-left">
            链接可能已过期、地址有误，或该页面已被移动。你可以返回首页，或从下方入口继续浏览。
          </Paragraph>
        </div>

        <Card
          bordered={false}
          className="flex min-h-0 w-full flex-1 flex-col justify-center shadow-sm md:max-w-[400px]"
        >
          <Text className="mb-4 block text-[13px] font-medium text-[var(--color-text-1)]">
            接下来做什么
          </Text>
          <div className="flex flex-col gap-2">
            <Button type="primary" long size="large" onClick={() => navigate('/', { replace: true })}>
              <span className="inline-flex items-center justify-center gap-2">
                <Home size={18} strokeWidth={1.75} />
                返回首页
              </span>
            </Button>
            <Button
              type="secondary"
              long
              size="large"
              onClick={() => navigate('/profile', { replace: true })}
            >
              <span className="inline-flex items-center justify-center gap-2">
                <User size={18} strokeWidth={1.75} />
                个人中心
              </span>
            </Button>
            <Button type="text" long className="!text-[13px]" onClick={() => navigate(-1)}>
              返回上一页
            </Button>
          </div>
          <div className="mt-6 rounded-lg bg-[var(--color-fill-2)] px-3 py-2.5">
            <Text type="secondary" className="text-[12px] leading-relaxed">
              若你认为这是站点问题，请联系管理员并提供当前地址栏中的完整 URL。
            </Text>
          </div>
        </Card>
      </div>
    </div>
  )
}
