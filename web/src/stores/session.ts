import { create } from 'zustand'
import { api, setAccessToken, guestSessionKey } from '../lib/api'
import type { User } from '../types'

const IMPERSONATION_KEY = 'vc_impersonating'

interface ImpersonationInfo {
  name: string
  email: string
}

function readImpersonation(): ImpersonationInfo | null {
  try {
    const raw = sessionStorage.getItem(IMPERSONATION_KEY)
    return raw ? (JSON.parse(raw) as ImpersonationInfo) : null
  } catch {
    return null
  }
}

interface SessionState {
  user: User | null
  accessToken: string | null
  loading: boolean
  impersonating: ImpersonationInfo | null
  startImpersonation: (token: string, target: User) => void
  stopImpersonation: () => Promise<void>
  login: (email: string, password: string, totpCode?: string) => Promise<void>
  register: (data: { email: string; password: string; full_name: string }) => Promise<void>
  logout: () => Promise<void>
  restore: () => Promise<void>
}

// Best-effort: fold the anonymous cart into the user cart after auth.
async function mergeGuestCart() {
  try {
    await api.post('/cart/merge', { session_key: guestSessionKey() })
    localStorage.removeItem('vc_guest_session') // start a fresh guest identity next time
  } catch {
    // non-blocking: merge is opportunistic
  }
}

export const useSession = create<SessionState>((set) => ({
  user: null,
  accessToken: null,
  loading: true,
  impersonating: readImpersonation(),

  startImpersonation: (token, target) => {
    const info = { name: target.full_name, email: target.email }
    sessionStorage.setItem(IMPERSONATION_KEY, JSON.stringify(info))
    // NOTE: deliberately not calling /auth/logout on exit — the admin's real
    // refresh cookie stays untouched and restores their session.
    setAccessToken(token)
    set({ user: target, accessToken: token, impersonating: info })
  },

  stopImpersonation: async () => {
    sessionStorage.removeItem(IMPERSONATION_KEY)
    set({ impersonating: null })
    // Re-mint an admin access token from the untouched refresh cookie.
    try {
      const res = await api.post<{ access_token: string }>('/auth/refresh')
      setAccessToken(res.data.access_token)
      const me = await api.get<{ user: User }>('/auth/me')
      set({ user: me.data.user, accessToken: res.data.access_token })
    } catch {
      setAccessToken(null)
      set({ user: null, accessToken: null })
    }
  },

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
    await mergeGuestCart()
  },

  register: async (data) => {
    const res = await api.post<{ access_token: string; user: User }>('/auth/register', data)
    setAccessToken(res.data.access_token)
    set({ user: res.data.user, accessToken: res.data.access_token })
    await mergeGuestCart()
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
