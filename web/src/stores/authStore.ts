import { create } from 'zustand'

const TOKEN_KEY = 'auth_token'

type AuthState = {
  token: string | null
  setToken: (token: string | null) => void
  clearUser: () => void
}

function readStoredToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY)
  } catch {
    return null
  }
}

export const useAuthStore = create<AuthState>((set) => ({
  token: readStoredToken(),
  setToken: (token) => {
    try {
      if (token) localStorage.setItem(TOKEN_KEY, token)
      else localStorage.removeItem(TOKEN_KEY)
    } catch {
      /* ignore */
    }
    set({ token })
  },
  clearUser: () => {
    try {
      localStorage.removeItem(TOKEN_KEY)
    } catch {
      /* ignore */
    }
    set({ token: null })
  },
}))
