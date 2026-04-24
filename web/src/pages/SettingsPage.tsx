import { Card, Switch, Typography } from '@arco-design/web-react'
import { useColorModeStore } from '@/stores/colorMode'

const { Title, Paragraph, Text } = Typography

export function SettingsPage() {
  const mode = useColorModeStore((s) => s.mode)
  const setMode = useColorModeStore((s) => s.setMode)

  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-auto bg-[var(--color-fill-1)] px-5 py-5">
      <Title heading={5} className="!mb-1 !mt-0 shrink-0">
        设置
      </Title>
      <Paragraph type="secondary" className="!mb-6 !mt-0 text-[13px]">
        管理外观与通用偏好。主题也可在侧栏底部快速切换。
      </Paragraph>

      <div className="mx-auto flex w-full max-w-[640px] min-w-0 flex-col gap-4">
        <Card title="外观" bordered={false} className="shadow-sm">
          <div className="flex flex-wrap items-center justify-between gap-4">
            <div className="min-w-0 flex-1">
              <Text className="block text-[13px] font-medium text-[var(--color-text-1)]">
                主题模式
              </Text>
              <Text type="secondary" className="mt-1 block text-[12px]">
                在亮色与暗色之间切换，设置会保存在本机。
              </Text>
            </div>
            <div className="flex shrink-0 items-center gap-3">
              <Text type="secondary" className="text-[12px]">
                {mode === 'dark' ? '暗色' : '亮色'}
              </Text>
              <Switch
                checked={mode === 'dark'}
                onChange={(checked) => setMode(checked ? 'dark' : 'light')}
              />
            </div>
          </div>
        </Card>

        <Card title="关于" bordered={false} className="shadow-sm">
          <Text type="secondary" className="text-[13px]">
            LingVoice — 更多设置项将在后续版本接入。
          </Text>
        </Card>
      </div>
    </div>
  )
}
