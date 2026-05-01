// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import { ConfigProvider } from '@arco-design/web-react'
import enUS from '@arco-design/web-react/es/locale/en-US'
import jaJP from '@arco-design/web-react/es/locale/ja-JP'
import zhCN from '@arco-design/web-react/es/locale/zh-CN'
import type { ReactNode } from 'react'
import type { AppLocale } from '@/locale/storage'
import { useLocaleStore } from '@/locale/store'

function arcoLocaleFor(loc: AppLocale) {
  switch (loc) {
    case 'en':
      return enUS
    case 'ja':
      return jaJP
    default:
      return zhCN
  }
}

export function AppLocaleRoot(props: { children: ReactNode }) {
  const locale = useLocaleStore((s) => s.locale)
  return <ConfigProvider locale={arcoLocaleFor(locale)}>{props.children}</ConfigProvider>
}
