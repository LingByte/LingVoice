import { Empty, Typography } from '@arco-design/web-react'

const { Title, Paragraph } = Typography

export function BlankPage() {
  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col items-center justify-center px-6 py-16">
      <Title heading={5} className="!mb-2">
        工作区
      </Title>
      <Paragraph type="secondary" className="!mb-10 max-w-xl text-center">
        页面骨架已就绪，后续在此接入业务模块即可。
      </Paragraph>
      <Empty description="暂无内容" />
    </div>
  )
}
