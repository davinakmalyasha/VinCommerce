import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useDebouncedValue } from '../lib/useDebouncedValue'
import { useSession } from '../stores/session'
import { formatIDR } from '../lib/format'
import type { Address } from '../types'
import { payWithSnap, midtransEnabled } from '../lib/midtrans'
import { PaymentCountdown } from '../components/PaymentCountdown'

interface Quote {
  subtotal: number
  discount_amount: number
  coupon_code?: string
  shipping: { code: string; name: string; fee: number; min_days: number; max_days: number }[]
  total: number
  insurance_available?: boolean
  insurance_selected?: boolean
  insurance_fee?: number
  points_discount?: number
  points_redeemed?: number
}

interface PlacedOrder {
  orders: { id: string; order_number: string; status: string; total_amount: number; seller_id: string; placed_at?: string }[]
  grand_total: number
}

interface VoucherLite {
  id: string
  code: string
  type: 'percent' | 'fixed'
  value: number
  min_subtotal: number
  store_name?: string
}

export function CheckoutPage() {
  const { user } = useSession()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [coupon, setCoupon] = useState('')
  const [shippingMethod, setShippingMethod] = useState('regular')
  const [selectedAddressId, setSelectedAddressId] = useState('')
  const [form, setForm] = useState({
    recipient: user?.full_name ?? '',
    phone: '',
    address_line1: '',
    city: '',
    province: '',
    postal_code: '',
  })
  const [error, setError] = useState('')
  const [paidOrder, setPaidOrder] = useState<PlacedOrder | null>(null)
  const [saveAddress, setSaveAddress] = useState(false)
  const [notes, setNotes] = useState('')
  const [insurance, setInsurance] = useState(false)
  const [pointsInput, setPointsInput] = useState('')
  const [idemKey] = useState(() => crypto.randomUUID())

  const { data: addresses } = useQuery({
    queryKey: ['addresses'],
    queryFn: async () => (await api.get<{ addresses: Address[] }>('/account/addresses')).data.addresses,
    enabled: !!user,
  })

  const { data: vouchers } = useQuery({
    queryKey: ['vouchers'],
    queryFn: async () => (await api.get<{ vouchers: VoucherLite[] }>('/vouchers')).data.vouchers,
  })

  const { data: loyaltyBalance } = useQuery({
    queryKey: ['loyalty'],
    queryFn: async () => (await api.get<{ balance: number }>('/loyalty')).data.balance,
  })

  // Best-coupon suggestion: estimate each voucher's discount against the current quote subtotal.
  const applicableVouchers = (vouchers ?? []).filter((v) => !quote || quote.subtotal >= v.min_subtotal)
  const bestVoucher = applicableVouchers.length
    ? [...applicableVouchers].sort((a, b) => {
        const est = (v: VoucherLite) =>
          v.type === 'percent'
            ? Math.round(((quote?.subtotal ?? 0) * v.value) / 100)
            : v.value
        return est(b) - est(a)
      })[0]
    : null

  const quoteEnabled = !!user && !paidOrder

  // Debounce free-text inputs: without it every keystroke fires a
  // POST /checkout/quote and thrashes the pricing engine.
  const debCoupon = useDebouncedValue(coupon, 450)
  const debPoints = useDebouncedValue(pointsInput, 450)

  const {
    data: quote,
    isError: quoteError,
  } = useQuery({
    queryKey: ['quote', debCoupon, shippingMethod, insurance, debPoints],
    queryFn: async () =>
      (
        await api.post<Quote>('/checkout/quote', {
          coupon_code: debCoupon || undefined,
          shipping_method_code: shippingMethod,
          insurance,
          points_to_redeem: debPoints || undefined,
        })
      ).data,
    enabled: quoteEnabled,
    retry: false,
    placeholderData: (prev) => prev, // keep last good totals while refetching
  })

  const placeOrder = useMutation({
    mutationFn: async () =>
      (
        await api.post<PlacedOrder>(
          '/checkout/place',
          {
            coupon_code: coupon || undefined,
            shipping_method_code: shippingMethod,
            address_id: selectedAddressId || undefined,
            address: selectedAddressId ? undefined : form,
            notes: notes || undefined,
            insurance,
            points_to_redeem: Number(pointsInput) > 0 ? Number(pointsInput) : undefined,
          },
          { headers: { 'X-Idempotency-Key': idemKey } },
        )
      ).data,
    onSuccess: async (placed) => {
      if (saveAddress && !selectedAddressId) {
        try {
          await api.post('/account/addresses', { ...form, label: 'Checkout', country: 'Indonesia' })
          queryClient.invalidateQueries({ queryKey: ['addresses'] })
        } catch {
          // non-blocking: address book save is best-effort
        }
      }
      setPaidOrder(placed)
      queryClient.invalidateQueries({ queryKey: ['cart'] })
    },
    onError: (e: Error) => setError(e.message),
  })

  const applyAddress = (a: Address) => {
    setSelectedAddressId(a.id)
    setForm({
      recipient: a.recipient,
      phone: a.phone,
      address_line1: a.address_line1,
      city: a.city,
      province: a.province,
      postal_code: a.postal_code,
    })
  }

  if (!user) return <Navigate to="/login" replace />

  if (paidOrder) {
    return (
      <div className="mx-auto max-w-3xl px-4 py-16">
        <div className="bg-white border border-gray-200 rounded-2xl p-8">
          <div className="text-center">
            <p className="text-5xl mb-4">✅</p>
            <h1 className="text-2xl font-bold mb-2">Pesanan dibuat!</h1>
            <p className="text-gray-500 mb-8">
              {paidOrder.orders.length} pesanan terpisah per penjual, total {formatIDR(paidOrder.grand_total)}.
              Pembayaran dilakukan <b>di luar aplikasi</b> (transfer bank / e-wallet pilihanmu) — isi bukti
              pembayaran untuk setiap pesanan di bawah.
            </p>
          </div>
          <div className="space-y-3 mb-8">
            {paidOrder.orders.map((o) => (
              <PayCard key={o.id} order={o} />
            ))}
          </div>
          <button
            onClick={() => navigate('/orders')}
            className="w-full py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600"
          >
            Lihat Pesanan
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-7xl px-4 py-6 grid grid-cols-1 lg:grid-cols-3 gap-6">
      <div className="lg:col-span-2 space-y-6">
        <section className="bg-white border border-gray-200 rounded-xl p-5">
          <h2 className="font-bold mb-3">1. Alamat Pengiriman</h2>
          {addresses && addresses.length > 0 && (
            <div className="flex flex-wrap gap-2 mb-3">
              {addresses.map((a) => (
                <button
                  key={a.id}
                  onClick={() => applyAddress(a)}
                  className="px-3 py-2 rounded-lg border text-xs hover:border-amber-400"
                >
                  {a.label}: {a.recipient}, {a.city}
                </button>
              ))}
            </div>
          )}
          <div className="grid grid-cols-2 gap-3">
            <input
              placeholder="Nama penerima"
              value={form.recipient}
              onChange={(e) => setForm({ ...form, recipient: e.target.value })}
              className="px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
            />
            <input
              placeholder="No. HP"
              value={form.phone}
              onChange={(e) => setForm({ ...form, phone: e.target.value })}
              className="px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
            />
            <input
              placeholder="Alamat (jalan, no. rumah)"
              value={form.address_line1}
              onChange={(e) => setForm({ ...form, address_line1: e.target.value })}
              className="col-span-2 px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
            />
            <input
              placeholder="Kota/Kabupaten"
              value={form.city}
              onChange={(e) => setForm({ ...form, city: e.target.value })}
              className="px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
            />
            <input
              placeholder="Provinsi"
              value={form.province}
              onChange={(e) => setForm({ ...form, province: e.target.value })}
              className="px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
            />
            <input
              placeholder="Kode pos"
              value={form.postal_code}
              onChange={(e) => setForm({ ...form, postal_code: e.target.value })}
              className="px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
            />
          </div>
          {!selectedAddressId && (
            <label className="flex items-center gap-2 mt-3 text-sm cursor-pointer select-none">
              <input
                type="checkbox"
                checked={saveAddress}
                onChange={(e) => setSaveAddress(e.target.checked)}
                className="accent-amber-500"
              />
              Simpan alamat ini ke buku alamat untuk checkout berikutnya
            </label>
          )}
        </section>

        <section className="bg-white border border-gray-200 rounded-xl p-5">
          <h2 className="font-bold mb-3">2. Metode Pengiriman</h2>
          <div className="space-y-2">
            {(quote?.shipping.length ? quote.shipping : [{ code: 'regular', name: 'Regular (Standard)', fee: 0, min_days: 3, max_days: 7 }]).map((m) => (
              <label
                key={m.code}
                className={`flex items-center justify-between p-3 rounded-xl border cursor-pointer ${
                  shippingMethod === m.code ? 'border-amber-500 bg-amber-50' : 'border-gray-200'
                }`}
              >
                <div className="flex items-center gap-3">
                  <input
                    type="radio"
                    checked={shippingMethod === m.code}
                    onChange={() => setShippingMethod(m.code)}
                  />
                  <div>
                    <p className="text-sm font-medium">{m.name}</p>
                    <p className="text-xs text-gray-500">Estimasi {m.min_days}-{m.max_days} hari</p>
                  </div>
                </div>
                <span className="text-sm font-medium">{formatIDR(m.fee)}</span>
              </label>
            ))}
          </div>
          {quote?.insurance_available && (
            <label className="flex items-center justify-between mt-3 p-3 rounded-xl border border-blue-200 bg-blue-50 cursor-pointer">
              <div className="flex items-center gap-3">
                <input
                  type="checkbox"
                  checked={insurance}
                  onChange={(e) => setInsurance(e.target.checked)}
                  className="accent-blue-600"
                />
                <div>
                  <p className="text-sm font-medium">🛡️ Asuransi Pengiriman</p>
                  <p className="text-xs text-gray-500">
                    Ganti rugi jika paket hilang/rusak ({formatIDR(quote.insurance_fee ?? 0)})
                  </p>
                </div>
              </div>
            </label>
          )}
        </section>

        <section className="bg-white border border-gray-200 rounded-xl p-5">
          <h2 className="font-bold mb-3">3. Pembayaran</h2>
          <p className="text-sm text-gray-600">
            Bayar instan via <b>Midtrans</b> (QRIS / e-wallet / VA / kartu) atau transfer di luar aplikasi —
            setelah pesanan dibuat, isi bukti pembayaran (no. referensi, jumlah, tanggal) untuk setiap pesanan.
            Penjual memproses pesanan setelah pembayaran tercatat.
          </p>
          <textarea
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder="Catatan untuk penjual (opsional): warna, ukuran, titik kirim..."
            rows={2}
            className="mt-3 w-full px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
          />
        </section>

        <section className="bg-white border border-gray-200 dark:border-gray-700 rounded-xl p-5">
          <h2 className="font-bold mb-3">4. Kupon</h2>
          <div className="flex gap-2">
            <input
              value={coupon}
              onChange={(e) => setCoupon(e.target.value.toUpperCase())}
              placeholder="WELCOME10 atau FLAT50K"
              className="flex-1 px-3 py-2 border rounded-lg text-sm uppercase outline-none focus:border-amber-400"
            />
          </div>
          <p className="text-xs text-gray-400 mt-2">Demo kupon: WELCOME10 (10%, min Rp50.000) · FLAT50K (Rp50.000, min Rp200.000)</p>
          {applicableVouchers.length > 0 && (
            <div className="mt-4 space-y-2">
              <p className="text-xs font-semibold text-gray-500">
                Kupon tersedia untuk pesananmu{bestVoucher ? ` — terbaik: ${bestVoucher.code}` : ''}:
              </p>
              <div className="flex flex-wrap gap-2">
                {applicableVouchers.map((v) => (
                  <button
                    key={v.id}
                    onClick={() => setCoupon(v.code)}
                    className={`px-3 py-1.5 rounded-full border text-xs ${
                      coupon === v.code ? 'border-amber-500 bg-amber-50 text-amber-700' : 'hover:border-amber-400'
                    }`}
                  >
                    🎟️ <b>{v.code}</b> —{' '}
                    {v.type === 'percent' ? `${v.value}% off` : `${formatIDR(v.value)} off`}
                    {v.min_subtotal > 0 ? ` (min ${formatIDR(v.min_subtotal)})` : ''}
                    {v.store_name ? ` · ${v.store_name}` : ''}
                  </button>
                ))}
              </div>
            </div>
          )}
        </section>
      </div>

      <aside className="h-fit bg-white border border-gray-200 rounded-xl p-5 space-y-3">
        <h2 className="font-bold">Ringkasan</h2>
        <div className="flex justify-between text-sm">
          <span className="text-gray-500">Subtotal</span>
          <span>{formatIDR(quote?.subtotal ?? 0)}</span>
        </div>
        {quote && quote.discount_amount > 0 && (
          <div className="flex justify-between text-sm text-green-600">
            <span>Diskon {quote.coupon_code}</span>
            <span>−{formatIDR(quote.discount_amount)}</span>
          </div>
        )}
        {quote && (quote.points_discount ?? 0) > 0 && (
          <div className="flex justify-between text-sm text-green-600">
            <span>Poin ({formatIDR(quote.points_redeemed ?? 0)} pts)</span>
            <span>−{formatIDR(quote.points_discount!)}</span>
          </div>
        )}
        <div className="flex justify-between text-sm">
          <span className="text-gray-500">Ongkir</span>
          <span>{formatIDR((quote?.shipping ?? []).reduce((s, m) => s + m.fee, 0))}</span>
        </div>
        <hr />
        <div className="flex justify-between font-bold">
          <span>Total</span>
          <span className="text-amber-600">{formatIDR(quote?.total ?? 0)}</span>
        </div>
        {quoteError && (
          <p className="text-xs text-red-600 bg-red-50 dark:bg-red-950/40 rounded-lg p-2.5">
            Gagal memuat ringkasan pembayaran. Periksa koneksi lalu coba lagi sebelum memesan.
          </p>
        )}
        {quote?.insurance_available && (
          <label className="flex items-center justify-between text-xs bg-blue-50 dark:bg-blue-900/20 rounded-lg p-2.5 cursor-pointer">
            <span className="flex items-center gap-2">
              <input type="checkbox" checked={insurance} onChange={(e) => setInsurance(e.target.checked)} className="accent-blue-600" />
              🛡️ Asuransi pengiriman
            </span>
            <b>+{formatIDR(quote.insurance_fee ?? 0)}</b>
          </label>
        )}
        {(loyaltyBalance ?? 0) >= 100 && (
          <label className="block text-xs space-y-1">
            <span className="flex items-center justify-between">
              <span className="text-gray-500">Tukar poin (1 pt = Rp100)</span>
              <button onClick={() => setPointsInput(String(loyaltyBalance))} className="text-amber-600 hover:underline">
                Pakai semua ({loyaltyBalance})
              </button>
            </span>
            <input
              type="number"
              min={0}
              max={loyaltyBalance ?? 0}
              value={pointsInput}
              onChange={(e) => setPointsInput(e.target.value)}
              placeholder="Jumlah poin"
              className="w-full px-3 py-2 border rounded-lg outline-none focus:border-amber-400"
            />
          </label>
        )}
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button
          onClick={() => placeOrder.mutate()}
          // Never place an order against a failed/stale quote — the displayed
          // total must be real. quoteFetching keeps the button live during
          // background refreshes (placeholderData holds the previous quote).
          disabled={placeOrder.isPending || !quote || quoteError}
          className="w-full py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
        >
          {placeOrder.isPending ? 'Memproses...' : 'Buat Pesanan'}
        </button>
      </aside>
    </div>
  )
}

function PayCard({ order }: { order: { id: string; order_number: string; status: string; total_amount: number; seller_id: string; placed_at?: string } }) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState({ reference: '', amount: '', paid_at: '' })
  const [done, setDone] = useState(false)
  const [err, setErr] = useState('')
  const [showExternal, setShowExternal] = useState(false)

  const refreshOrder = () => {
    queryClient.invalidateQueries({ queryKey: ['order', order.id] })
    queryClient.invalidateQueries({ queryKey: ['orders'] })
    queryClient.invalidateQueries({ queryKey: ['cart'] })
  }

  const confirm = useMutation({
    mutationFn: async () =>
      api.post(`/orders/${order.id}/external-payment`, {
        reference: form.reference,
        amount: Number(form.amount),
        paid_at: form.paid_at ? new Date(form.paid_at).toISOString() : undefined,
      }),
    onSuccess: () => {
      setDone(true)
      refreshOrder()
    },
    onError: (e: Error) => setErr(e.message),
  })

  const snapPay = useMutation({
    mutationFn: async () => payWithSnap(order.id, { onSuccess: () => setDone(true) }),
    onSuccess: () => refreshOrder(),
    onError: (e: Error) => setErr(e.message),
  })

  // Wallet balance payment: instant capture from stored saldo.
  const walletPay = useMutation({
    mutationFn: async () =>
      api.post(`/payments/orders/${order.id}/intent`, { method: 'wallet' }),
    onSuccess: () => {
      setDone(true)
      refreshOrder()
    },
    onError: (e: Error) => setErr(e.message),
  })

  // COD: pay cash on delivery — seller fulfills immediately.
  const codPay = useMutation({
    mutationFn: async () =>
      api.post(`/payments/orders/${order.id}/intent`, { method: 'cod' }),
    onSuccess: () => {
      setDone(true)
      refreshOrder()
    },
    onError: (e: Error) => setErr(e.message),
  })

  const busy = walletPay.isPending || codPay.isPending || snapPay.isPending

  return (
    <div className="bg-gray-50 rounded-xl p-4 border border-gray-100">
      <div className="flex items-center justify-between mb-2">
        <p className="font-medium text-sm">{order.order_number}</p>
        <p className="font-bold text-sm text-amber-600">{formatIDR(order.total_amount)}</p>
      </div>
      {done ? (
        <p className="text-sm text-green-700">✅ Metode pembayaran tercatat. Penjual akan memproses pesananmu.</p>
      ) : (
        <>
          {order.placed_at && (
            <div className="mb-2">
              <PaymentCountdown placedAt={order.placed_at} />
            </div>
          )}
          {/* Payment method picker — every backend method is reachable */}
          <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 mb-2">
            {midtransEnabled() && (
              <button
                onClick={() => snapPay.mutate()}
                disabled={busy}
                className="px-3 py-2 rounded-lg bg-blue-600 text-white text-xs font-medium hover:bg-blue-700 disabled:opacity-50"
              >
                {snapPay.isPending ? 'Membuka...' : '⚡ Midtrans'}
              </button>
            )}
            <button
              onClick={() => walletPay.mutate()}
              disabled={busy}
              className="px-3 py-2 rounded-lg bg-emerald-600 text-white text-xs font-medium hover:bg-emerald-700 disabled:opacity-50"
            >
              {walletPay.isPending ? 'Memproses...' : '💰 Saldo Wallet'}
            </button>
            <button
              onClick={() => codPay.mutate()}
              disabled={busy}
              className="px-3 py-2 rounded-lg bg-violet-600 text-white text-xs font-medium hover:bg-violet-700 disabled:opacity-50"
            >
              {codPay.isPending ? 'Memproses...' : '💵 COD'}
            </button>
            <button
              onClick={() => setShowExternal(!showExternal)}
              className={`px-3 py-2 rounded-lg text-xs font-medium border ${
                showExternal ? 'bg-gray-900 text-white border-gray-900' : 'border-gray-300 hover:bg-white'
              }`}
            >
              🏦 Transfer Manual
            </button>
          </div>
          {showExternal && (
            <>
              <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
                <input
                  value={form.reference}
                  onChange={(e) => setForm({ ...form, reference: e.target.value })}
                  placeholder="No. referensi / bukti transfer"
                  className="px-3 py-2 border rounded-lg text-xs outline-none bg-white"
                />
                <input
                  type="number"
                  value={form.amount}
                  onChange={(e) => setForm({ ...form, amount: e.target.value })}
                  placeholder={`Jumlah (Rp ${Math.round(order.total_amount).toLocaleString('id-ID')})`}
                  className="px-3 py-2 border rounded-lg text-xs outline-none bg-white"
                />
                <input
                  type="date"
                  value={form.paid_at}
                  onChange={(e) => setForm({ ...form, paid_at: e.target.value })}
                  className="px-3 py-2 border rounded-lg text-xs outline-none bg-white"
                />
              </div>
              <button
                onClick={() => confirm.mutate()}
                disabled={confirm.isPending || !form.reference.trim() || !form.amount}
                className="mt-2 px-4 py-2 rounded-lg bg-gray-900 text-white text-xs hover:bg-gray-800 disabled:opacity-50"
              >
                {confirm.isPending ? 'Mencatat...' : 'Saya Sudah Bayar'}
              </button>
            </>
          )}
          {err && <p className="text-xs text-red-600 mt-1">{err}</p>}
        </>
      )}
    </div>
  )
}
