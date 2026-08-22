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

async function ensureSnap(): Promise<Window['snap'] | null> {
  if (!CLIENT_KEY) return null
  if (window.snap) return window.snap
  await new Promise<void>((resolve, reject) => {
    const script = document.createElement('script')
    script.src = SNAP_URL
    script.dataset.clientKey = CLIENT_KEY
    script.onload = () => resolve()
    script.onerror = () => reject(new Error('Gagal memuat Midtrans Snap'))
    document.body.appendChild(script)
  })
  return window.snap ?? null
}

/**
 * Initiates a Midtrans Snap payment for an order and opens the checkout popup.
 * Falls back to opening the redirect URL in a new tab when snap.js cannot load.
 * Returns "paid" | "pending" | "redirected".
 */
export async function payWithSnap(orderId: string, cb: SnapCallbacks = {}): Promise<string> {
  const { data } = await api.post<IntentResponse>(`/payments/orders/${orderId}/intent`, {
    method: 'midtrans_snap',
    idempotency_key: `snap-${orderId}-${Date.now()}`,
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
      await new Promise<void>((resolve) => {
        snap.pay(data.snap_token!, {
          onSuccess: () => {
            finish('success')
            resolve()
          },
          onPending: () => {
            finish('pending')
            resolve()
          },
          onError: () => {
            finish('error')
            resolve()
          },
          onClose: () => {
            resolve()
          },
        })
      })
      return 'paid'
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
