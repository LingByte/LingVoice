// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

export const LOCALE_STORAGE_KEY = 'lingvoice.ui.locale'

/** UI / Accept-Language tags aligned with server `i18n` (zh-CN, en, ja). */
export type AppLocale = 'zh-CN' | 'en' | 'ja'

export function coerceAppLocale(raw?: string | null): AppLocale | null {
  if (raw == null) return null
  const s = String(raw).trim().toLowerCase()
  if (!s) return null
  if (s === 'zh-cn' || s === 'zh_cn' || s === 'zh') return 'zh-CN'
  if (s.startsWith('zh')) return 'zh-CN'
  if (s.startsWith('ja')) return 'ja'
  if (s.startsWith('en')) return 'en'
  return null
}

export function readStoredLocale(): AppLocale {
  if (typeof window === 'undefined') return 'zh-CN'
  try {
    const c = coerceAppLocale(window.localStorage.getItem(LOCALE_STORAGE_KEY))
    if (c) return c
  } catch {
    /* ignore */
  }
  return 'zh-CN'
}

export function writeStoredLocale(l: AppLocale) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, l)
  } catch {
    /* ignore */
  }
}
