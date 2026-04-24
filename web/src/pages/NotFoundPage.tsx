import { Button, Result, Typography } from '@arco-design/web-react'
import { useNavigate } from 'react-router-dom'

const { Paragraph } = Typography

export function NotFoundPage() {
  const navigate = useNavigate()

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col items-center justify-center px-4 py-16">
      <Result
        status="404"
        title="404"
        subTitle={
          <Paragraph type="secondary" className="!mb-0 text-center">
            页面不存在或已被移动
          </Paragraph>
        }
        extra={
          <Button type="primary" onClick={() => navigate('/', { replace: true })}>
            返回首页
          </Button>
        }
      />
    </div>
  )
}
