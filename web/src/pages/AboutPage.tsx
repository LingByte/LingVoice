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

        <Title heading={6} className="!mb-2">
          许可
        </Title>
        <Paragraph className="!mb-6 !text-[14px] !leading-relaxed">
          本项目以 <strong>AGPL-3.0</strong> 授权发布。若你在组织内部署或二次开发，请遵守许可证义务（源码提供、网络使用等条款）。
        </Paragraph>

        <Title heading={6} className="!mb-2">
          数据收集声明
        </Title>
        <Paragraph className="!mb-4 !text-[14px] !leading-relaxed">
          为维护服务质量、保障网络安全及防止滥用，本站将记录访问者在使用服务时的以下技术信息：
        </Paragraph>
        <ul className="!mb-6 ml-6 list-disc !text-[14px] !leading-relaxed text-[var(--color-text-2)]">
          <li>访问者 IP 地址</li>
          <li>请求次数与频率</li>
          <li>请求消耗的额度</li>
          <li>系统日志与错误信息</li>
        </ul>
        <Paragraph className="!mb-6 !text-[14px] !leading-relaxed">
          <strong>隐私承诺：</strong>除上述必要的技术信息外，本站不会主动收集您的任何个人身份信息、设备指纹或其他敏感数据。我们郑重承诺不会保存或记录您的具体请求内容（包括但不限于对话内容、API 调用参数等），您的隐私权益将得到充分尊重与保护。
        </Paragraph>

        <Title heading={6} className="!mb-2">
          免责声明与使用条款
        </Title>
        <Paragraph className="!mb-4 !text-[14px] !leading-relaxed">
          使用本服务即表示您同意遵守以下条款：
        </Paragraph>
        <ul className="!mb-4 ml-6 list-disc !text-[14px] !leading-relaxed text-[var(--color-text-2)]">
          <li><strong>合法合规使用：</strong>使用者必须在遵循 <a href="https://openai.com/zh-Hans-CN/policies/terms-of-use/" target="_blank" rel="noopener noreferrer" className="text-[rgb(var(--primary-6))] hover:underline">OpenAI 使用条款</a>及所在国家/地区法律法规的前提下使用本服务，严禁用于任何非法用途。</li>
          <li><strong>内容禁止：</strong>严禁利用本服务生成、传播违法违规内容，包括但不限于暴力、色情、仇恨言论、虚假信息等。一经发现，将立即封禁账号并保留追究法律责任的权利。</li>
          <li><strong>风险提示：</strong>本站不对生成内容的准确性、完整性或适用性承担任何责任。使用者应自行评估并承担使用生成内容的风险。</li>
          <li><strong>地域限制：</strong>根据 <a href="https://www.cac.gov.cn/2023-07/13/c_1690898327029107.htm" target="_blank" rel="noopener noreferrer" className="text-[rgb(var(--primary-6))] hover:underline">《生成式人工智能服务管理暂行办法》</a>及相关法规，本站严禁中国大陆地区居民访问和使用本服务。请遵守当地法律法规，切勿向中国大陆地区公众提供未经备案的生成式人工智能服务。</li>
          <li><strong>个人学习用途：</strong>本网站仅供个人学习、研究及开发测试使用，不得用于商业用途或其他超出授权范围的用途。</li>
        </ul>
        <Paragraph className="!mb-6 !text-[14px] !leading-relaxed">
          本站保留随时修改本声明的权利，修改后的声明将在网站上公布。继续使用本服务即表示您同意接受修改后的声明。
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
