import { create } from 'zustand'

const STORAGE_KEY = 'lingvoice-color-mode'

export type ColorMode = 'light' | 'dark'

function readStored(): ColorMode {
  if (typeof window === 'undefined') return 'light'
  return window.localStorage.getItem(STORAGE_KEY) === 'dark' ? 'dark' : 'light'
}

function applyToDocument(mode: ColorMode) {
  if (typeof document === 'undefined') return
  if (mode === 'dark') document.body.setAttribute('arco-theme', 'dark')
  else document.body.removeAttribute('arco-theme')
}

const initialMode = readStored()
applyToDocument(initialMode)

type ColorModeState = {
  mode: ColorMode
  setMode: (mode: ColorMode) => void
  toggleMode: () => void
}

export const useColorModeStore = create<ColorModeState>((set, get) => ({
  mode: initialMode,
  setMode: (mode) => {
    if (typeof window !== 'undefined') {
      window.localStorage.setItem(STORAGE_KEY, mode)
    }
    applyToDocument(mode)
    set({ mode })
  },
  toggleMode: () => {
    const next: ColorMode = get().mode === 'dark' ? 'light' : 'dark'
    get().setMode(next)
  },
}))
