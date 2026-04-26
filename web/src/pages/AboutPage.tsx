import { Typography } from '@arco-design/web-react'

const { Title, Paragraph } = Typography

export function AboutPage() {
  return (
    <div className="h-full overflow-auto px-6 py-6">
      <div className="mx-auto max-w-[720px]">
        <div className="mb-6 flex items-center gap-4">
          <img src="/logo.png" alt="LingVoice" className="h-16 w-16 shrink-0 rounded-2xl object-contain" />
          <div>
            <Title heading={4} className="!mb-1">
              LingVoice
            </Title>
            <Paragraph type="secondary" className="!mb-0 !text-[14px]">
              多模态与 OpenAI 兼容网关的一体化控制台
            </Paragraph>
          </div>
        </div>

        <Paragraph className="!mb-4 !text-[14px] !leading-relaxed">
          LingVoice 面向团队提供聊天、LLM/语音渠道管理、凭证与额度、用量统计以及邮件通知等能力；设计上参考了
          new-api 等成熟网关产品在路由、倍率与 OpenAPI 形态上的经验，并针对站内运营（公告、通知渠道）做了扩展。
        </Paragraph>

        <Title heading={6} className="!mb-2">
          许可
        </Title>
        <Paragraph className="!mb-6 !text-[14px] !leading-relaxed">
          本项目以 <strong>AGPL-3.0</strong> 授权发布。若你在组织内部署或二次开发，请遵守许可证义务（源码提供、网络使用等条款）。
        </Paragraph>

        <Title heading={6} className="!mb-2">
          版本与构建
        </Title>
        <Paragraph className="!text-[14px] !leading-relaxed text-[var(--color-text-3)]">
          具体版本号与变更记录以仓库发布说明为准；遇到问题可先查看「文档」与「公告」中的维护说明。
        </Paragraph>
      </div>
    </div>
  )
}
