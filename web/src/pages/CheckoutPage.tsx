import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import { formatIDR } from '../lib/format'
import type { Address } from '../types'

interface Quote {
  subtotal: number
  discount_amount: number
  coupon_code?: string
  shipping: { code: string; name: string; fee: number; min_days: number; max_days: number }[]
  total: number
}

interface PlacedOrder {
  orders: { id: string; order_number: string; status: string; total_amount: number; seller_id: string }[]
  grand_total: number
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

  const { data: addresses } = useQuery({
    queryKey: ['addresses'],
    queryFn: async () => (await api.get<{ addresses: Address[] }>('/account/addresses')).data.addresses,
    enabled: !!user,
  })

  const quoteEnabled = !!user && !paidOrder

  const { data: quote } = useQuery({
    queryKey: ['quote', coupon, shippingMethod],
    queryFn: async () =>
      (
        await api.post<Quote>('/checkout/quote', {
          coupon_code: coupon || undefined,
          shipping_method_code: shippingMethod,
        })
      ).data,
    enabled: quoteEnabled,
    retry: false,
  })

  const placeOrder = useMutation({
    mutationFn: async () =>
      (
        await api.post<PlacedOrder>('/checkout/place', {
          coupon_code: coupon || undefined,
          shipping_method_code: shippingMethod,
          address_id: selectedAddressId || undefined,
          address: selectedAddressId ? undefined : form,
        })
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

  const useAddress = (a: Address) => {
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
                  onClick={() => useAddress(a)}
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
        </section>

        <section className="bg-white border border-gray-200 rounded-xl p-5">
          <h2 className="font-bold mb-3">3. Pembayaran</h2>
          <p className="text-sm text-gray-600">
            Pembayaran dilakukan <b>di luar aplikasi</b> — setelah pesanan dibuat, kamu membayar lewat
            transfer bank / e-wallet pilihanmu dan mengisi bukti pembayaran (no. referensi, jumlah, tanggal).
            Penjual memproses pesanan setelah pembayaran tercatat.
          </p>
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
        <div className="flex justify-between text-sm">
          <span className="text-gray-500">Ongkir</span>
          <span>{formatIDR((quote?.shipping ?? []).reduce((s, m) => s + m.fee, 0))}</span>
        </div>
        <hr />
        <div className="flex justify-between font-bold">
          <span>Total</span>
          <span className="text-amber-600">{formatIDR(quote?.total ?? 0)}</span>
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <button
          onClick={() => placeOrder.mutate()}
          disabled={placeOrder.isPending}
          className="w-full py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
        >
          {placeOrder.isPending ? 'Memproses...' : 'Buat Pesanan'}
        </button>
      </aside>
    </div>
  )
}

function PayCard({ order }: { order: { id: string; order_number: string; status: string; total_amount: number; seller_id: string } }) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState({ reference: '', amount: '', paid_at: '' })
  const [done, setDone] = useState(false)
  const [err, setErr] = useState('')

  const confirm = useMutation({
    mutationFn: async () =>
      api.post(`/orders/${order.id}/external-payment`, {
        reference: form.reference,
        amount: Number(form.amount),
        paid_at: form.paid_at ? new Date(form.paid_at).toISOString() : undefined,
      }),
    onSuccess: () => {
      setDone(true)
      queryClient.invalidateQueries({ queryKey: ['cart'] })
    },
    onError: (e: Error) => setErr(e.message),
  })

  return (
    <div className="bg-gray-50 rounded-xl p-4 border border-gray-100">
      <div className="flex items-center justify-between mb-2">
        <p className="font-medium text-sm">{order.order_number}</p>
        <p className="font-bold text-sm text-amber-600">{formatIDR(order.total_amount)}</p>
      </div>
      {done ? (
        <p className="text-sm text-green-700">✅ Pembayaran tercatat. Penjual akan memproses pesananmu.</p>
      ) : (
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
          {err && <p className="text-xs text-red-600 mt-1">{err}</p>}
          <button
            onClick={() => confirm.mutate()}
            disabled={confirm.isPending || !form.reference.trim() || !form.amount}
            className="mt-2 px-4 py-2 rounded-lg bg-gray-900 text-white text-xs hover:bg-gray-800 disabled:opacity-50"
          >
            {confirm.isPending ? 'Mencatat...' : 'Saya Sudah Bayar'}
          </button>
        </>
      )}
    </div>
  )
}
