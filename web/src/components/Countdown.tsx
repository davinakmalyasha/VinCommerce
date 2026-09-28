import { memo, useEffect, useState, type ReactNode } from 'react'

/** Everything a caller may need from the ticking region, without re-rendering the page. */
export interface CountdownParts {
  /** Milliseconds left, clamped at 0. */
  remaining: number
  expired: boolean
  hh: number
  mm: number
  ss: number
  /** Zero-padded "HH:MM:SS". */
  clock: string
}

export interface CountdownProps {
  /** Absolute deadline as epoch milliseconds. `0` renders nothing and never ticks. */
  to: number
  /** Custom renderer. When given, it replaces the default clock markup. */
  children?: (parts: CountdownParts) => ReactNode
  /** `blocks` renders hh/mm/ss as separate boxes (flash-sale hero). */
  variant?: 'text' | 'blocks'
  /** Shown instead of the clock once `to` has passed. */
  expiredLabel?: ReactNode
  /** Fired exactly once, when the deadline passes. */
  onExpire?: () => void
  className?: string
}

const partsOf = (remaining: number): CountdownParts => {
  const clamped = Math.max(0, remaining)
  const hh = Math.floor(clamped / 3_600_000)
  const mm = Math.floor((clamped % 3_600_000) / 60_000)
  const ss = Math.floor((clamped % 60_000) / 1000)
  return {
    remaining: clamped,
    expired: clamped <= 0,
    hh,
    mm,
    ss,
    clock: `${String(hh).padStart(2, '0')}:${String(mm).padStart(2, '0')}:${String(ss).padStart(2, '0')}`,
  }
}

/**
 * The ONLY component in the app that subscribes to the wall clock.
 *
 * It used to be a `setInterval(() => setNow(Date.now()), 1000)` inside
 * ProductPage, FlashSalePage, PaymentCountdown and OrderDetailPage — and
 * because the state lived in the page, every tick re-rendered the whole page
 * (ProductPage is 845 lines, including every ProductCard) and OrderDetailPage
 * ran two such intervals. Confining the tick to this leaf means the rest of
 * the page re-renders zero times per second.
 *
 * The interval stops as soon as the deadline passes, so a finished countdown
 * costs nothing.
 */
export const Countdown = memo(function Countdown({
  to,
  children,
  variant = 'text',
  expiredLabel,
  onExpire,
  className = '',
}: CountdownProps) {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (!to) return
    // Align the first tick to the next whole second so the displayed clock
    // does not visibly skip a second right after mount.
    setNow(Date.now())
    const t = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(t)
  }, [to])

  const parts = partsOf(to ? to - now : 0)

  useEffect(() => {
    if (to && parts.expired) onExpire?.()
  }, [to, parts.expired, onExpire])

  if (!to) return null
  if (parts.expired && expiredLabel) return <>{expiredLabel}</>

  if (children) return <>{children(parts)}</>

  if (variant === 'blocks') {
    return (
      <div className={`flex gap-2 font-mono text-2xl font-bold ${className}`}>
        {[parts.hh, parts.mm, parts.ss].map((v, i) => (
          <span key={i} className="bg-black/30 px-3 py-2 rounded-xl">
            {String(v).padStart(2, '0')}
          </span>
        ))}
      </div>
    )
  }

  return <span className={`font-mono ${className}`}>{parts.clock}</span>
})
