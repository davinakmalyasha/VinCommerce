import { create } from 'zustand'
import { api, setAccessToken, guestSessionKey, refreshAccessToken } from '../lib/api'
import type { User } from '../types'

const IMPERSONATION_KEY = 'vc_impersonating'

// Best-effort: drop any service-worker Cache Storage entries. The SW never
// caches /api/ or /uploads/ (Cache Storage keys ignore the Authorization and
// X-Session-Key headers, so caching user-scoped responses would replay one
// account's data to the next), but this is defence in depth: it guarantees no
// user-scoped response survives an account switch or logout.
function clearServiceWorkerCache() {
  try {
    if (navigator.serviceWorker?.controller) {
      navigator.serviceWorker.controller.postMessage({ type: 'CLEAR_USER_CACHE' })
    }
  } catch {
    // ignore — the SW may not be registered at all
  }
}

// Wired by App.tsx so login/logout can purge cached per-user react-query data
// (orders, wallet, admin tables) - otherwise the next account could briefly
// render the previous user's data.
let clearQueryCache: () => void = () => {}
export function bindQueryCacheClear(fn: () => void) {
  clearQueryCache = fn
}

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
    // Routed through the shared single-flight refresh so concurrent callers
    // (streams, boot restore) join the same rotation instead of racing it.
    try {
      const token = await refreshAccessToken()
      if (!token) throw new Error('refresh failed')
      const me = await api.get<{ user: User }>('/auth/me')
      set({ user: me.data.user, accessToken: token })
    } catch {
      setAccessToken(null)
      set({ user: null, accessToken: null })
    }
  },

  restore: async () => {
    // Access token lives in memory only; the refresh token is an httpOnly
    // cookie, so restore = rotate it and fetch the profile. Uses the shared
    // single-flight helper so StrictMode double-mounts and NotificationBell's
    // stream share ONE rotation instead of invalidating each other's cookies.
    const token = await refreshAccessToken()
    if (token) {
      try {
        const me = await api.get<{ user: User }>('/auth/me')
        // Silent-revert detection: mid-impersonation reloads rotate via the
        // ADMIN's cookie — the banner would then lie about who is acting.
        const imp = readImpersonation()
        if (imp && me.data.user.email !== imp.email) {
          sessionStorage.removeItem(IMPERSONATION_KEY)
          set({ user: me.data.user, accessToken: token, loading: false, impersonating: null })
          return
        }
        set({ user: me.data.user, accessToken: token, loading: false })
        return
      } catch {
        // fall through to signed-out state
      }
    }
    setAccessToken(null)
    sessionStorage.removeItem(IMPERSONATION_KEY)
    set({ user: null, accessToken: null, loading: false, impersonating: null })
  },

  login: async (email, password, totpCode) => {
    const res = await api.post<{ access_token: string; user: User }>('/auth/login', {
      email,
      password,
      totp_code: totpCode,
    })
    clearQueryCache() // purge any previous account's cached data
    clearServiceWorkerCache()
    setAccessToken(res.data.access_token)
    set({ user: res.data.user, accessToken: res.data.access_token })
    await mergeGuestCart()
  },

  register: async (data) => {
    const res = await api.post<{ access_token: string; user: User }>('/auth/register', data)
    clearQueryCache()
    clearServiceWorkerCache()
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
    clearQueryCache()
    clearServiceWorkerCache()
    set({ user: null, accessToken: null })
  },
}))
