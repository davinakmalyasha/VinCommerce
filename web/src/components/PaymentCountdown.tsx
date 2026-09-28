import { paymentDeadline } from '../lib/format'
import { Countdown } from './Countdown'

/**
 * Ticking countdown to the payment deadline (placed_at + reservation window).
 *
 * The `setInterval` now lives inside <Countdown>, so only this leaf re-renders
 * once per second. It used to live in a `usePaymentDeadline` hook called from
 * the page component, which made OrderDetailPage re-render its address block,
 * items list, timeline and totals every second — and it did so TWICE, once
 * for the page-level hook and once for this component.
 */
export function PaymentCountdown({ placedAt }: { placedAt: string }) {
  return (
    <Countdown to={paymentDeadline(placedAt)}>
      {({ expired, clock }) =>
        expired ? (
          <span className="inline-flex items-center gap-1.5 font-mono text-sm font-bold text-red-600">
            ⏰ Waktu pembayaran habis
          </span>
        ) : (
          <span className="inline-flex items-center gap-1.5 text-sm text-amber-800">
            Selesaikan pembayaran dalam
            <span className="font-mono font-bold text-red-600">{clock}</span>
            <span className="text-xs text-gray-500">(stok ditahan)</span>
          </span>
        )
      }
    </Countdown>
  )
}
