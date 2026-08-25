import { api } from './api'

interface SnapCallbacks {
  onSuccess?: () => void
  onPending?: () => void
  onError?: () => void
  onClose?: () => void
}

interface IntentResponse {
  snap_token?: string
  payment_url?: string
  gateway_ref?: string
}

declare global {
  interface Window {
    snap?: {
      pay: (token: string, cb?: Record<string, (result: unknown) => void>) => void
    }
  }
}

const CLIENT_KEY = import.meta.env.VITE_MIDTRANS_CLIENT_KEY as string | undefined
const SNAP_URL =
  (import.meta.env.VITE_MIDTRANS_SNAP_URL as string | undefined) ??
  'https://app.sandbox.midtrans.com/snap/snap.js'

export function midtransEnabled(): boolean {
  return !!CLIENT_KEY
}

// Deduplicate concurrent script injections: two parallel calls otherwise
// append two <script> tags and race each other.
let snapLoader: Promise<Window['snap'] | null> | null = null

async function ensureSnap(): Promise<Window['snap'] | null> {
  if (!CLIENT_KEY) return null
  if (window.snap) return window.snap
  if (!snapLoader) {
    snapLoader = new Promise<void>((resolve, reject) => {
      const script = document.createElement('script')
      script.src = SNAP_URL
      script.dataset.clientKey = CLIENT_KEY
      script.onload = () => resolve()
      script.onerror = () => {
        snapLoader = null // allow a later retry after a load failure
        reject(new Error('Gagal memuat Midtrans Snap'))
      }
      document.body.appendChild(script)
    }).then(() => window.snap ?? null)
  }
  return snapLoader
}

export type SnapResult = 'paid' | 'pending' | 'error' | 'closed' | 'redirected'

/**
 * Initiates a Midtrans Snap payment for an order and opens the checkout popup.
 * Falls back to opening the redirect URL in a new tab when snap.js cannot load.
 * Returns the ACTUAL popup outcome — callers must not assume "paid".
 * The idempotency key is derived from orderId only, so retrying a failed
 * attempt reuses the same intent instead of creating duplicates.
 */
export async function payWithSnap(orderId: string, cb: SnapCallbacks = {}): Promise<SnapResult> {
  const { data } = await api.post<IntentResponse>(`/payments/orders/${orderId}/intent`, {
    method: 'midtrans_snap',
    idempotency_key: `snap-${orderId}`,
  })

  const finish = (kind: 'success' | 'pending' | 'error') => {
    api.get(`/orders/${orderId}`).catch(() => {})
    if (kind === 'success') cb.onSuccess?.()
    else if (kind === 'pending') cb.onPending?.()
    else cb.onError?.()
  }

  try {
    const snap = await ensureSnap()
    if (data.snap_token && snap) {
      const outcome = await new Promise<SnapResult>((resolve) => {
        snap.pay(data.snap_token!, {
          onSuccess: () => {
            finish('success')
            resolve('paid')
          },
          onPending: () => {
            finish('pending')
            resolve('pending')
          },
          onError: () => {
            finish('error')
            resolve('error')
          },
          onClose: () => resolve('closed'),
        })
      })
      return outcome
    }
  } catch {
    // fall through to hosted-page redirect below
  }
  if (data.payment_url) {
    window.open(data.payment_url, '_blank', 'noopener')
    return 'redirected'
  }
  throw new Error('Pembayaran Midtrans tidak tersedia')
}
