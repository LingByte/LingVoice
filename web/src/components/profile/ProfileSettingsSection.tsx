import { Card, Switch, Typography } from '@arco-design/web-react'
import { useColorModeStore } from '@/stores/colorMode'

const { Paragraph, Text } = Typography

/** 原「设置」页内容，嵌入个人中心「偏好设置」。 */
export function ProfileSettingsSection() {
  const mode = useColorModeStore((s) => s.mode)
  const setMode = useColorModeStore((s) => s.setMode)

  return (
    <div className="mx-auto flex w-full max-w-[640px] min-w-0 flex-col gap-4">
      <Card title="外观" bordered={false} className="shadow-sm">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="min-w-0 flex-1">
            <Text className="block text-[13px] font-medium text-[var(--color-text-1)]">主题模式</Text>
            <Text type="secondary" className="mt-1 block text-[12px]">
              在亮色与暗色之间切换，设置会保存在本机。侧栏底部也可快速切换主题。
            </Text>
          </div>
          <div className="flex shrink-0 items-center gap-3">
            <Text type="secondary" className="text-[12px]">
              {mode === 'dark' ? '暗色' : '亮色'}
            </Text>
            <Switch checked={mode === 'dark'} onChange={(checked) => setMode(checked ? 'dark' : 'light')} />
          </div>
        </div>
      </Card>

      <Card title="关于" bordered={false} className="shadow-sm">
        <Paragraph type="secondary" className="!mb-0 !text-[13px]">
          LingVoice — 更多偏好项将在后续版本接入。
        </Paragraph>
      </Card>
    </div>
  )
}
