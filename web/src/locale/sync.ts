// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

import type { AuthUser } from '@/api/auth'
import { coerceAppLocale } from '@/locale/storage'
import { useLocaleStore } from '@/locale/store'

/** After login/register or profile save: adopt server `locale` when it maps to a UI language. */
export function syncLocaleFromAuthUser(user: AuthUser | null | undefined) {
  const loc = coerceAppLocale(user?.locale)
  if (!loc) return
  useLocaleStore.getState().setLocale(loc)
}
