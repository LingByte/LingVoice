// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import { create } from 'zustand'
import i18n from '@/locale/i18n'
import type { AppLocale } from '@/locale/storage'
import { readStoredLocale, writeStoredLocale } from '@/locale/storage'

type LocaleState = {
  locale: AppLocale
  setLocale: (next: AppLocale) => void
}

export const useLocaleStore = create<LocaleState>((set) => ({
  locale: readStoredLocale(),
  setLocale: (next) => {
    writeStoredLocale(next)
    void i18n.changeLanguage(next)
    set({ locale: next })
  },
}))
