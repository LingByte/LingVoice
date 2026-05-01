// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

export { AppLocaleRoot } from '@/locale/AppLocaleRoot'
export { acceptLanguageHeader } from '@/locale/acceptLanguage'
export { default as i18n } from '@/locale/i18n'
export type { AppLocale } from '@/locale/storage'
export { coerceAppLocale, readStoredLocale, writeStoredLocale, LOCALE_STORAGE_KEY } from '@/locale/storage'
export { syncLocaleFromAuthUser } from '@/locale/sync'
export { useLocaleStore } from '@/locale/store'
