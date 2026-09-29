import axios, { AxiosError } from 'axios'

export class ApiError extends Error {
  status: number
  code: string
  details?: unknown

  constructor(status: number, code: string, message: string, details?: unknown) {
    super(message)
    this.status = status
    this.code = code
    this.details = details
  }
}

/**
 * The API base URL.
 *
 * VITE_API_URL was documented in .env.example as "API base URL the app calls"
 * while this line hardcoded '/api/v1', so setting it changed nothing about
 * where the app actually sent its requests. An operator following the
 * documentation to point the SPA at a cross-origin API had silently configured
 * nothing, and the only symptom was every request hitting the SPA's own origin
 * and 404ing.
 *
 * Empty means same-origin, which is the correct default behind nginx: it
 * forwards /api to the API container, so a relative base is both simpler and
 * immune to a wrong origin in configuration.
 */
const apiBase = import.meta.env.VITE_API_URL ?? ''

export const API_V1 = `${apiBase.replace(/\/+$/, '')}/api/v1`

export const api = axios.create({
  baseURL: API_V1,
  timeout: 15_000,
  headers: { 'Content-Type': 'application/json' },
  withCredentials: true,
})

let accessToken: string | null = null
let refreshPromise: Promise<string | null> | null = null

export function setAccessToken(token: string | null) {
  accessToken = token
  if (token) {
    api.defaults.headers.common.Authorization = `Bearer ${token}`
  } else {
    delete api.defaults.headers.common.Authorization
  }
}

export function getAccessToken(): string | null {
  return accessToken
}

// Rotates the access token via the httpOnly refresh cookie (single-flight).
export async function refreshAccessToken(): Promise<string | null> {
  if (!refreshPromise) {
    refreshPromise = api
      .post<{ access_token: string }>('/auth/refresh')
      .then((res) => {
        setAccessToken(res.data.access_token)
        return res.data.access_token
      })
      .catch(() => {
        setAccessToken(null)
        return null
      })
      .finally(() => {
        refreshPromise = null
      })
  }
  return refreshPromise
}

api.interceptors.request.use((config) => {
  if (accessToken) {
    config.headers.Authorization = `Bearer ${accessToken}`
  }
  // Guest cart identity: stable per browser so anonymous carts persist.
  config.headers['X-Session-Key'] = guestSessionKey()
  return config
})

const GUEST_KEY_STORAGE = 'vc_guest_session'

export function guestSessionKey(): string {
  let key = localStorage.getItem(GUEST_KEY_STORAGE)
  if (!key) {
    key = crypto.randomUUID()
    localStorage.setItem(GUEST_KEY_STORAGE, key)
  }
  return key
}

// Firefox and Safari cancel an in-flight download when its object URL is
// revoked in the same task as `click()`, so the file never arrives. Give the
// navigation a task to start before releasing the blob.
const REVOKE_DELAY_MS = 1000

function revokeSoon(blobUrl: string) {
  window.setTimeout(() => URL.revokeObjectURL(blobUrl), REVOKE_DELAY_MS)
}

// Downloads an authenticated file (e.g. CSV exports) as a blob — plain <a href>
// links cannot send the Bearer header and would 401.
export async function downloadFile(url: string, filename: string) {
  const res = await api.get(url, { responseType: 'blob' })
  const blobUrl = URL.createObjectURL(res.data as Blob)
  const link = document.createElement('a')
  link.href = blobUrl
  link.download = filename
  link.click()
  revokeSoon(blobUrl)
}

// Opens an authenticated HTML document (invoice, packing slip) in a new tab.
export async function openDocument(url: string) {
  const res = await api.get(url, { responseType: 'blob', timeout: 30_000 })
  const blobUrl = URL.createObjectURL(
    new Blob([res.data as Blob], { type: 'text/html' }),
  )
  window.open(blobUrl, '_blank')
  // This one leaked a blob per invoice open — nothing ever revoked it.
  revokeSoon(blobUrl)
}

api.interceptors.response.use(
  (response) => response,
  async (error: AxiosError<{ error: { code?: string; message?: string; details?: unknown } }>) => {
    const status = error.response?.status ?? 0
    const body = error.response?.data
    const apiError = new ApiError(
      status,
      body?.error?.code ?? 'NETWORK_ERROR',
      body?.error?.message ?? error.message,
      body?.error?.details,
    )

    const url = error.config?.url ?? ''
    const isAuthCall = /^\/auth\/(login|register|refresh|logout)/.test(url)
    const alreadyRetried = (error.config as { _retried?: boolean } | undefined)?._retried
    if (status === 401 && !isAuthCall && !alreadyRetried && error.config) {
      const fresh = await refreshAccessToken()
      if (fresh) {
        const config = error.config
        ;(config as { _retried?: boolean })._retried = true
        config.headers = config.headers ?? {}
        config.headers.Authorization = `Bearer ${fresh}`
        return api.request(config)
      }
    }

    return Promise.reject(apiError)  },
)
