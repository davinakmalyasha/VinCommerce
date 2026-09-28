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
 * Opens a blank tab SYNCHRONOUSLY from the click handler and hands back a
 * function that navigates it later.
 *
 * `window.open` after an `await` is no longer in the user-gesture task, so
 * every popup blocker refused it and the buyer was left with a silent no-op
 * after paying nothing. Pre-opening keeps the gesture; the tab is closed
 * again if the payment turns out not to need it.
 */
function preopenTab(): { navigate: (href: string) => void; discard: () => void } {
  const win = window.open('', '_blank')
  if (!win) return { navigate: () => {}, discard: () => {} }
  try {
    win.document.write('<!doctype html><title>Memuat pembayaran…</title><body style="font-family:system-ui;padding:2rem">Memuat pembayaran…</body>')
    win.document.close()
  } catch {
    // Cross-origin restrictions on a reused tab: harmless, the navigation
    // below still works.
  }
  let used = false
  return {
    navigate: (href) => {
      used = true
      win.location.href = href
    },
    discard: () => {
      if (used) return
      try {
        win.close()
      } catch {
        // A tab script opened may refuse to close; nothing to do about it.
      }
    },
  }
}

/**
 * Initiates a Midtrans Snap payment for an order and opens the checkout popup.
 * Falls back to opening the redirect URL in a new tab when snap.js cannot load.
 * Returns the ACTUAL popup outcome — callers must not assume "paid".
 * The idempotency key is derived from orderId only, so retrying a failed
 * attempt reuses the same intent instead of creating duplicates.
 */
export async function payWithSnap(orderId: string, cb: SnapCallbacks = {}): Promise<SnapResult> {
  // Must run before the first await so it inherits the click's user gesture.
  const tab = preopenTab()

  let intent: IntentResponse
  try {
    const { data } = await api.post<IntentResponse>(`/payments/orders/${orderId}/intent`, {
      method: 'midtrans_snap',
      idempotency_key: `snap-${orderId}`,
    })
    intent = data
  } catch (e) {
    tab.discard()
    throw e
  }

  const finish = (kind: 'success' | 'pending' | 'error') => {
    api.get(`/orders/${orderId}`).catch(() => {})
    if (kind === 'success') cb.onSuccess?.()
    else if (kind === 'pending') cb.onPending?.()
    else cb.onError?.()
  }

  try {
    const snap = await ensureSnap()
    if (intent.snap_token && snap) {
      // Snap renders its own modal in this tab; the pre-opened tab is dead
      // weight, so close it before handing over.
      tab.discard()
      const outcome = await new Promise<SnapResult>((resolve) => {
        snap.pay(intent.snap_token!, {
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
  if (intent.payment_url) {
    tab.navigate(intent.payment_url)
    return 'redirected'
  }
  tab.discard()
  throw new Error('Pembayaran Midtrans tidak tersedia')
}
