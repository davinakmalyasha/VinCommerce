import { useEffect, useState } from 'react'
import { paymentDeadlineMs, RESERVATION_MINUTES } from '../lib/format'

/**
 * Ticking countdown to the payment deadline (placed_at + reservation window).
 * `onExpire`-style state is exposed via the returned `expired` boolean so
 * callers can disable pay buttons before the gateway token dies.
 */
export function usePaymentDeadline(placedAt: string | undefined) {
  const deadline = placedAt ? paymentDeadlineMs(placedAt) : 0
  const [now, setNow] = useState(Date.now())

  useEffect(() => {
    if (!deadline) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [deadline])

  const remain = deadline ? Math.max(0, deadline - now) : 0
  const expired = !!deadline && remain <= 0
  const expiringSoon = !!deadline && !expired && remain < 2 * 60_000

  const hh = Math.floor(remain / 3_600_000)
  const mm = Math.floor((remain % 3_600_000) / 60_000)
  const ss = Math.floor((remain % 60_000) / 1000)
  const clock = `${String(hh).padStart(2, '0')}:${String(mm).padStart(2, '0')}:${String(ss).padStart(2, '0')}`

  return { remain, expired, expiringSoon, clock, total: RESERVATION_MINUTES * 60 }
}

export function PaymentCountdown({ placedAt }: { placedAt: string }) {
  const { clock, expired } = usePaymentDeadline(placedAt)

  if (expired) {
    return (
      <span className="inline-flex items-center gap-1.5 font-mono text-sm font-bold text-red-600">
        ⏰ Waktu pembayaran habis
      </span>
    )
  }

  return (
    <span className="inline-flex items-center gap-1.5 text-sm text-amber-800">
      Selesaikan pembayaran dalam
      <span className="font-mono font-bold text-red-600">{clock}</span>
      <span className="text-xs text-gray-500">(stok ditahan)</span>
    </span>
  )
}
