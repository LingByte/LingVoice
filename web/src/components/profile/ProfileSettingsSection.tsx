import { Card, Select, Switch, Typography } from '@arco-design/web-react'
import { useTranslation } from 'react-i18next'
import { useColorModeStore } from '@/stores/colorMode'
import { useLocaleStore } from '@/locale/store'
import type { AppLocale } from '@/locale/storage'

const { Paragraph, Text } = Typography

/** 原「设置」页内容，嵌入个人中心「偏好设置」。 */
export function ProfileSettingsSection() {
  const { t } = useTranslation()
  const mode = useColorModeStore((s) => s.mode)
  const setMode = useColorModeStore((s) => s.setMode)
  const uiLocale = useLocaleStore((s) => s.locale)
  const setUiLocale = useLocaleStore((s) => s.setLocale)

  return (
    <div className="mx-auto flex w-full max-w-[640px] min-w-0 flex-col gap-4">
      <Card title={t('profilePrefs.language')} bordered={false} className="shadow-sm">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="min-w-0 flex-1">
            <Text type="secondary" className="block text-[12px] leading-relaxed">
              {t('profilePrefs.languageHint')}
            </Text>
          </div>
          <Select<AppLocale>
            value={uiLocale}
            onChange={(v) => setUiLocale(v as AppLocale)}
            className="w-44 shrink-0"
            options={[
              { label: t('locale.zhCN'), value: 'zh-CN' },
              { label: t('locale.en'), value: 'en' },
              { label: t('locale.ja'), value: 'ja' },
            ]}
          />
        </div>
      </Card>

      <Card title={t('profilePrefs.appearance')} bordered={false} className="shadow-sm">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="min-w-0 flex-1">
            <Text className="block text-[13px] font-medium text-[var(--color-text-1)]">
              {t('profilePrefs.themeMode')}
            </Text>
            <Text type="secondary" className="mt-1 block text-[12px]">
              {t('profilePrefs.themeHint')}
            </Text>
          </div>
          <div className="flex shrink-0 items-center gap-3">
            <Text type="secondary" className="text-[12px]">
              {mode === 'dark' ? t('profilePrefs.themeDark') : t('profilePrefs.themeLight')}
            </Text>
            <Switch checked={mode === 'dark'} onChange={(checked) => setMode(checked ? 'dark' : 'light')} />
          </div>
        </div>
      </Card>

      <Card title={t('profilePrefs.aboutCard')} bordered={false} className="shadow-sm">
        <Paragraph type="secondary" className="!mb-0 !text-[13px]">
          {t('profilePrefs.aboutText')}
        </Paragraph>
      </Card>
    </div>
  )
}
