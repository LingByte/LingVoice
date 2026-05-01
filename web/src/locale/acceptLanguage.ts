// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import type { AppLocale } from '@/locale/storage'

/** Value for `Accept-Language`; backend `i18n.ParseAcceptLanguage` reads the first tag. */
export function acceptLanguageHeader(locale: AppLocale): string {
  switch (locale) {
    case 'zh-CN':
      return 'zh-CN,zh;q=0.9,en;q=0.8,ja;q=0.5'
    case 'ja':
      return 'ja,en;q=0.85,zh-CN;q=0.7'
    default:
      return 'en,zh-CN;q=0.85,ja;q=0.6'
  }
}
