import { create } from 'zustand'
import { api, setAccessToken } from '../lib/api'
import type { User } from '../types'

interface SessionState {
  user: User | null
  accessToken: string | null
  loading: boolean
  login: (email: string, password: string, totpCode?: string) => Promise<void>
  register: (data: { email: string; password: string; full_name: string }) => Promise<void>
  logout: () => Promise<void>
  restore: () => Promise<void>
}

export const useSession = create<SessionState>((set) => ({
  user: null,
  accessToken: null,
  loading: true,

  restore: async () => {
    // Access token lives in memory only; the refresh token is an httpOnly
    // cookie, so restore = rotate it and fetch the profile.
    try {
      const res = await api.post<{ access_token: string }>('/auth/refresh')
      setAccessToken(res.data.access_token)
      const me = await api.get<{ user: User }>('/auth/me')
      set({ user: me.data.user, accessToken: res.data.access_token, loading: false })
    } catch {
      setAccessToken(null)
      set({ user: null, accessToken: null, loading: false })
    }
  },

  login: async (email, password, totpCode) => {
    const res = await api.post<{ access_token: string; user: User }>('/auth/login', {
      email,
      password,
      totp_code: totpCode,
    })
    setAccessToken(res.data.access_token)
    set({ user: res.data.user, accessToken: res.data.access_token })
  },

  register: async (data) => {
    const res = await api.post<{ access_token: string; user: User }>('/auth/register', data)
    setAccessToken(res.data.access_token)
    set({ user: res.data.user, accessToken: res.data.access_token })
  },

  logout: async () => {
    try {
      await api.post('/auth/logout')
    } catch {
      // ignore
    }
    setAccessToken(null)
    set({ user: null, accessToken: null })
  },
}))
