import { Typography } from '@arco-design/web-react'

const { Title, Paragraph } = Typography

export function DocsPage() {
  return (
    <div className="h-full overflow-auto px-6 py-6">
      <Title heading={4} className="!mb-4">
        文档
      </Title>

      <section className="mb-8 max-w-[820px]">
        <Title heading={6} className="!mb-2">
          OpenAI 兼容网关
        </Title>
        <Paragraph className="!text-[14px] !leading-relaxed">
          LingVoice 在根路径提供 <code className="rounded bg-[var(--color-fill-2)] px-1">/v1</code>{' '}
          兼容接口（与 new-api 习惯一致）。在「凭证」中创建分组并绑定可用的 LLM
          渠道后，即可使用标准 Chat Completions 等调用方式；具体路径与鉴权方式见「V1 网关调试」页。
        </Paragraph>
      </section>

      <section className="mb-8 max-w-[820px]">
        <Title heading={6} className="!mb-2">
          渠道与模型
        </Title>
        <Paragraph className="!text-[14px] !leading-relaxed">
          <strong>LLM 渠道</strong>配置上游地址与密钥；<strong>LLM 能力</strong>将「分组 + 模型名」映射到可承载的渠道，用于路由与负载。
          <strong>模型元数据</strong>用于广场展示、图标与计费倍率说明；<strong>模型广场</strong>汇总已登记模型与可路由情况。
        </Paragraph>
      </section>

      <section className="mb-8 max-w-[820px]">
        <Title heading={6} className="!mb-2">
          额度与计费
        </Title>
        <Paragraph className="!text-[14px] !leading-relaxed">
          用户剩余额度与用量在「数据面板」查看。控制台可将「额度单位」与美元充值比例关联（例如 0.01 USD /
          额度单位）；模型元数据中的倍率字段用于在网关侧折算各类 token 的扣费权重。语音（ASR/TTS）按渠道配置的计量方式统计用量。
        </Paragraph>
      </section>

      <section className="mb-8 max-w-[820px]">
        <Title heading={6} className="!mb-2">
          通知与邮件
        </Title>
        <Paragraph className="!text-[14px] !leading-relaxed">
          在「通知渠道」中配置邮件（SMTP / SendCloud 等）后，可在「邮件模版」维护多语言模版，并在「邮件日志」中排查投递记录。
        </Paragraph>
      </section>
    </div>
  )
}
