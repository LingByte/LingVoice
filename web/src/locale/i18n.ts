// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import { readStoredLocale } from '@/locale/storage'
import { resources } from '@/locale/resources'

void i18n.use(initReactI18next).init({
  resources,
  lng: readStoredLocale(),
  fallbackLng: 'zh-CN',
  supportedLngs: ['zh-CN', 'en', 'ja'],
  interpolation: { escapeValue: false },
})

export default i18n
