export function formatIDR(value: number): string {
  return new Intl.NumberFormat('id-ID', {
    style: 'currency',
    currency: 'IDR',
    maximumFractionDigits: 0,
  }).format(value)
}

export function formatDate(iso: string): string {
  return new Intl.DateTimeFormat('id-ID', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(iso))
}

export const orderStatusLabels: Record<string, string> = {
  pending: 'Menunggu Pembayaran',
  paid: 'Dibayar',
  packed: 'Dikemas',
  shipped: 'Dikirim',
  delivered: 'Terkirim',
  completed: 'Selesai',
  cancelled: 'Dibatalkan',
  return_requested: 'Retur Diajukan',
  returned: 'Dikembalikan',
}

export const orderStatusColors: Record<string, string> = {
  pending: 'bg-amber-100 text-amber-800',
  paid: 'bg-blue-100 text-blue-800',
  packed: 'bg-indigo-100 text-indigo-800',
  shipped: 'bg-purple-100 text-purple-800',
  delivered: 'bg-teal-100 text-teal-800',
  completed: 'bg-green-100 text-green-800',
  cancelled: 'bg-red-100 text-red-700',
  return_requested: 'bg-orange-100 text-orange-800',
  returned: 'bg-gray-200 text-gray-700',
}

export function slugify(s: string): string {
  return s.toLowerCase().replace(/[']/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
}

// Concrete gateway channel labels (Midtrans payment_type values + internal methods).
export const paymentMethodLabels: Record<string, string> = {
  gopay: 'GoPay',
  qris: 'QRIS',
  shopeepay: 'ShopeePay',
  dana: 'DANA',
  astrapay: 'AstraPay',
  bank_transfer: 'Transfer Bank (VA)',
  bank_bca_va: 'VA BCA',
  bank_bni_va: 'VA BNI',
  bank_bri_va: 'VA BRI',
  bank_mandiri_va: 'VA Mandiri',
  permata_va: 'VA Permata',
  bca_va: 'VA BCA',
  bni_va: 'VA BNI',
  bri_va: 'VA BRI',
  credit_card: 'Kartu Kredit/Debit',
  kredivo: 'Kredivo PayLater',
  akulaku: 'Akulaku PayLater',
  spaylater: 'ShopeePay Later',
  indomaret: 'Indomaret',
  alfamart: 'Alfamart',
  // internal
  wallet: 'Saldo Dompet',
  cod: 'Bayar di Tempat (COD)',
  midtrans_snap: 'Midtrans',
  e_wallet: 'E-Wallet',
}

export function paymentMethodLabel(m?: string | null): string {
  if (!m) return ''
  return paymentMethodLabels[m] ?? m
}

// Must stay in sync with backend order_service.ReservationHold.
export const RESERVATION_MINUTES = 30

export function paymentDeadlineMs(placedAt: string): number {
  return new Date(placedAt).getTime() + RESERVATION_MINUTES * 60_000
}

/** Absolute payment deadline as epoch ms, or 0 when there is no order yet. */
export function paymentDeadline(placedAt: string | undefined): number {
  return placedAt ? paymentDeadlineMs(placedAt) : 0
}

export type DeadlinePhase = 'ok' | 'soon' | 'expired'

/**
 * Where a deadline sits right now, without subscribing to a clock. Callers
 * that only need "is it too late to pay?" compute this once on mount and then
 * let <Countdown> tell them when it flips.
 */
export function deadlinePhase(deadline: number, at: number = Date.now()): DeadlinePhase {
  if (!deadline) return 'ok'
  const remain = deadline - at
  if (remain <= 0) return 'expired'
  return remain < 2 * 60_000 ? 'soon' : 'ok'
}

const etaFmt = new Intl.DateTimeFormat('id-ID', { day: 'numeric', month: 'short' })

// "Estimasi tiba 12–16 Agu" from a min/max day range starting today.
export function etaLabel(minDays: number, maxDays: number): string {
  const from = new Date()
  from.setDate(from.getDate() + (minDays || 3))
  const to = new Date()
  to.setDate(to.getDate() + (maxDays || 7))
  return `${etaFmt.format(from)} – ${etaFmt.format(to)}`
}
