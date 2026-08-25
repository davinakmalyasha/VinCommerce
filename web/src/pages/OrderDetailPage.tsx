import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useParams, Link } from 'react-router-dom'
import { api, openDocument } from '../lib/api'
import { formatIDR, formatDate, orderStatusColors, orderStatusLabels } from '../lib/format'
import { ReturnModal } from './ReturnModal'
import { payWithSnap, midtransEnabled } from '../lib/midtrans'
import { FileUpload } from '../components/FileUpload'
import { OrderChat } from '../components/OrderChat'
import { PaymentCountdown, usePaymentDeadline } from '../components/PaymentCountdown'
import { paymentMethodLabel } from '../lib/format'

interface OrderEvent {
  id: number
  from_status?: string
  to_status: string
  note?: string
  created_at: string
}

interface OrderDetail extends Record<string, unknown> {
  id: string
  order_number: string
  status: string
  payment_status: string
  total_amount: number
  subtotal: number
  discount_amount: number
  shipping_fee: number
  placed_at: string
  shipping_address: Record<string, string>
  shipping_method?: string
  tracking_number?: string
  carrier?: string
  external_payment_ref?: string
  external_paid_at?: string
  seller?: { name: string }
  items: { id: string; product_name: string; variant_name: string; quantity: number; total: number; image_url?: string }[]
}

export function OrderDetailPage() {
  const { id } = useParams()
  const queryClient = useQueryClient()
  const [reviewFor, setReviewFor] = useState<string | null>(null)
  const [reviewForm, setReviewForm] = useState({ rating: 5, title: '', content: '' })
  const [reviewImages, setReviewImages] = useState<string[]>([])
  const [returnFor, setReturnFor] = useState<string | null>(null)
  const [payForm, setPayForm] = useState({ reference: '', amount: '', paid_at: '' })
  const [payDone, setPayDone] = useState(false)

  const { data } = useQuery({
    queryKey: ['order', id],
    queryFn: async () => (await api.get<{ order: OrderDetail }>(`/orders/${id}`)).data.order,
  })

  const { data: events } = useQuery({
    queryKey: ['order-events', id],
    queryFn: async () => (await api.get<{ events: OrderEvent[] }>(`/orders/${id}/events`)).data.events,
  })

  // Concrete payment channel used (gopay/qris/kredivo/...) once paid.
  const { data: intent } = useQuery({
    queryKey: ['order-intent', id],
    queryFn: async () => (await api.get<{ intent: { method?: string } }>(`/payments/orders/${id}/intent`)).data.intent,
    enabled: !!id,
    retry: false,
  })
  const paidVia = data?.payment_status === 'paid' ? paymentMethodLabel(intent?.method) : ''

  const submitReview = useMutation({
    mutationFn: async (itemId: string) =>
      api.post(`/orders/${id}/reviews`, {
        order_item_id: itemId,
        rating: reviewForm.rating,
        title: reviewForm.title,
        content: reviewForm.content,
        images: reviewImages.filter(Boolean),
      }),
    onSuccess: () => {
      setReviewFor(null)
      setReviewForm({ rating: 5, title: '', content: '' })
      setReviewImages([])
    },
  })

  const cancel = useMutation({
    mutationFn: async () => api.post(`/orders/${id}/cancel`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['order', id] }),
  })

  const confirmDelivery = useMutation({
    mutationFn: async () => api.post(`/orders/${id}/confirm-delivery`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['order', id] }),
  })

  const complete = useMutation({
    mutationFn: async () => api.post(`/orders/${id}/complete`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['order', id] }),
  })

  const reorder = useMutation({
    mutationFn: async () => api.post(`/orders/${id}/reorder`),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cart'] })
      alert('Item ditambahkan ke keranjang!')
    },
  })

  const pay = useMutation({
    mutationFn: async () =>
      api.post(`/orders/${id}/external-payment`, {
        reference: payForm.reference,
        amount: Number(payForm.amount),
        paid_at: payForm.paid_at ? new Date(payForm.paid_at).toISOString() : undefined,
      }),
    onSuccess: () => {
      setPayDone(true)
      queryClient.invalidateQueries({ queryKey: ['order', id] })
      queryClient.invalidateQueries({ queryKey: ['order-events', id] })
    },
  })

  const snapPay = useMutation({
    mutationFn: async () =>
      payWithSnap(id!, {
        onSuccess: () => setPayDone(true),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['order', id] })
      queryClient.invalidateQueries({ queryKey: ['order-events', id] })
      queryClient.invalidateQueries({ queryKey: ['orders'] })
    },
  })

  // Payment deadline ticker — must run before any early return.
  const deadline = usePaymentDeadline(data?.placed_at)

  if (!data) return <div className="mx-auto max-w-4xl px-4 py-16 text-center text-gray-500">Memuat...</div>

  const addr = data.shipping_address
  const returnItem = returnFor ? data.items.find((i) => i.id === returnFor) : null
  const payBlocked = deadline.expired

  return (
    <div className="mx-auto max-w-4xl px-4 py-6 space-y-5">
      {returnItem && (
        <ReturnModal
          orderId={id!}
          itemId={returnItem.id}
          itemName={returnItem.product_name}
          onClose={() => setReturnFor(null)}
        />
      )}
      <div className="flex items-center justify-between">
        <div>
          <Link to="/orders" className="text-sm text-gray-400 hover:text-gray-700">← Pesanan</Link>
          <h1 className="text-xl font-bold mt-1">{data.order_number}</h1>
        </div>
        <div className="flex items-center gap-2">
          <span className={`px-3 py-1.5 rounded-full text-sm font-medium ${orderStatusColors[data.status]}`}>
            {orderStatusLabels[data.status]}
          </span>
          {(data.status === 'completed' || data.status === 'delivered' || data.status === 'cancelled') && (
            <button
              onClick={() => reorder.mutate()}
              disabled={reorder.isPending}
              className="px-3 py-1.5 rounded-full bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white text-sm disabled:opacity-50"
            >
              🔁 Beli Lagi
            </button>
          )}
        </div>
      </div>

      {data.status === 'pending' && (
        <div className="bg-amber-50 border border-amber-200 rounded-xl p-4">
          <div className="mb-2 space-y-1">
            <p className="text-sm text-amber-800">
              Bayar via Midtrans (QRIS, e-wallet, VA, kartu) atau transfer di luar aplikasi lalu isi bukti
              pembayaran.
            </p>
            {!payDone && !deadline.expired && <PaymentCountdown placedAt={data.placed_at} />}
          </div>
          {payDone ? (
            <p className="text-sm text-green-700 font-medium">Pembayaran tercatat! Penjual akan segera memproses pesanan. ✅</p>
          ) : deadline.expired ? (
            <div className="flex items-center gap-3">
              <p className="text-sm text-red-700">
                Batas pembayaran lewat — pesanan akan dibatalkan otomatis dan stok dilepas.
              </p>
              <button
                onClick={() => cancel.mutate()}
                disabled={cancel.isPending}
                className="px-4 py-2 rounded-lg bg-gray-900 text-white text-sm hover:bg-gray-800 disabled:opacity-50"
              >
                Batalkan Sekarang
              </button>
            </div>
          ) : (
            <>
              {midtransEnabled() && (
                <button
                  onClick={() => snapPay.mutate()}
                  disabled={snapPay.isPending || deadline.expiringSoon}
                  className="mb-3 px-4 py-2 rounded-lg bg-blue-600 text-white text-sm font-medium hover:bg-blue-700 disabled:opacity-50"
                >
                  {snapPay.isPending ? 'Membuka Midtrans...' : '⚡ Bayar Sekarang (Midtrans)'}
                </button>
              )}
              <div className="grid grid-cols-1 sm:grid-cols-3 gap-2">
                <input
                  value={payForm.reference}
                  onChange={(e) => setPayForm({ ...payForm, reference: e.target.value })}
                  placeholder="No. referensi / bukti transfer"
                  className="px-3 py-2 border rounded-lg text-sm outline-none"
                />
                <input
                  type="number"
                  value={payForm.amount}
                  onChange={(e) => setPayForm({ ...payForm, amount: e.target.value })}
                  placeholder={`Jumlah (Rp ${Math.round(data.total_amount).toLocaleString('id-ID')})`}
                  className="px-3 py-2 border rounded-lg text-sm outline-none"
                />
                <input
                  type="date"
                  value={payForm.paid_at}
                  onChange={(e) => setPayForm({ ...payForm, paid_at: e.target.value })}
                  className="px-3 py-2 border rounded-lg text-sm outline-none"
                />
              </div>
            </>
          )}
          {!payDone && (
            <div className="flex gap-2 mt-3">
              <button
                onClick={() => pay.mutate()}
                disabled={payBlocked || pay.isPending || !payForm.reference.trim() || !payForm.amount}
                className="px-4 py-2 rounded-lg bg-gray-900 text-white text-sm hover:bg-gray-800 disabled:opacity-50"
              >
                {pay.isPending ? 'Mencatat...' : 'Saya Sudah Bayar'}
              </button>
              <button
                onClick={() => cancel.mutate()}
                disabled={cancel.isPending}
                className="px-4 py-2 rounded-lg border border-gray-300 text-sm hover:bg-gray-50"
              >
                Batalkan
              </button>
            </div>
          )}
        </div>
      )}

      {data.status === 'shipped' && (
        <div className="bg-teal-50 border border-teal-200 rounded-xl p-4 flex items-center justify-between">
          <p className="text-sm text-teal-800">Pesanan sudah dikirim oleh penjual. Konfirmasi jika sudah diterima.</p>
          <button
            onClick={() => confirmDelivery.mutate()}
            disabled={confirmDelivery.isPending}
            className="px-4 py-2 rounded-lg bg-teal-600 text-white text-sm hover:bg-teal-700 disabled:opacity-50"
          >
            Pesanan Diterima
          </button>
        </div>
      )}

      {data.status === 'delivered' && (
        <div className="bg-green-50 border border-green-200 rounded-xl p-4 flex items-center justify-between">
          <p className="text-sm text-green-800">
            Konfirmasi selesai untuk melepas dana escrow ke penjual.
          </p>
          <button
            onClick={() => complete.mutate()}
            disabled={complete.isPending}
            className="px-4 py-2 rounded-lg bg-green-600 text-white text-sm hover:bg-green-700 disabled:opacity-50"
          >
            Selesaikan Pesanan
          </button>
        </div>
      )}

      <div className="bg-white border border-gray-200 rounded-xl p-5">
        <h2 className="font-bold text-sm mb-3">Alamat Pengiriman</h2>
        <p className="text-sm">{addr.recipient} · {addr.phone}</p>
        <p className="text-sm text-gray-600">
          {addr.address_line1}, {addr.city}, {addr.province} {addr.postal_code}
        </p>
        {data.shipping_method && (
          <p className="text-xs text-gray-500 mt-2">Kurir: {data.shipping_method}</p>
        )}
        {data.tracking_number && (
          <p className="text-xs text-blue-600 mt-1">
            📦 Resi ({data.carrier || 'kurir'}): <span className="font-mono font-semibold">{data.tracking_number}</span>
          </p>
        )}
      </div>

      {events && events.length > 0 && (
        <div className="bg-white border border-gray-200 rounded-xl p-5">
          <OrderChat orderId={data.id} status={data.status} />

          <h2 className="font-bold text-sm mb-4">Riwayat Pesanan</h2>
          <div className="space-y-0">
            {events.map((e, i) => (
              <div key={e.id} className="flex gap-3">
                <div className="flex flex-col items-center">
                  <div className={`w-3 h-3 rounded-full mt-1 ${i === events.length - 1 ? 'bg-amber-500' : 'bg-gray-300'}`} />
                  {i < events.length - 1 && <div className="w-px flex-1 bg-gray-200" />}
                </div>
                <div className="pb-4">
                  <p className="text-sm font-medium capitalize">
                    {e.to_status.replace('_', ' ')}
                    {e.note && <span className="text-gray-400 font-normal"> — {e.note}</span>}
                  </p>
                  <p className="text-xs text-gray-400">{formatDate(e.created_at)}</p>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="bg-white border border-gray-200 rounded-xl p-5 space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="font-bold text-sm">Item ({data.items.length})</h2>
          <button onClick={() => openDocument(`/orders/${id}/invoice`)} className="text-xs text-amber-600 hover:underline">
            🧾 Unduh Invoice
          </button>
        </div>
        {data.items.map((it) => (
          <div key={it.id}>
            <div className="flex items-center gap-3">
              {it.image_url ? (
                <img src={it.image_url} alt="" className="w-12 h-12 rounded-lg object-cover bg-gray-100" />
              ) : (
                <div className="w-12 h-12 rounded-lg bg-gray-100" />
              )}
              <div className="flex-1">
                <p className="text-sm line-clamp-1">{it.product_name}</p>
                <p className="text-xs text-gray-500">{it.variant_name} × {it.quantity}</p>
              </div>
              <p className="text-sm font-medium">{formatIDR(it.total)}</p>
            </div>
            {(data.status === 'completed' || data.status === 'delivered') && (
              <div className="mt-2 ml-15 flex gap-3">
                {reviewFor === it.id ? (
                  <div className="bg-amber-50 border border-amber-200 rounded-xl p-4 space-y-2">
                    <div className="flex items-center gap-1">
                      {[1, 2, 3, 4, 5].map((n) => (
                        <button
                          key={n}
                          onClick={() => setReviewForm({ ...reviewForm, rating: n })}
                          className={`text-xl ${n <= reviewForm.rating ? 'text-amber-500' : 'text-gray-300'}`}
                        >
                          ★
                        </button>
                      ))}
                    </div>
                    <input
                      placeholder="Judul (opsional)"
                      value={reviewForm.title}
                      onChange={(e) => setReviewForm({ ...reviewForm, title: e.target.value })}
                      className="w-full px-3 py-2 border rounded-lg text-sm outline-none"
                    />
                    <textarea
                      placeholder="Bagaimana produknya?"
                      rows={2}
                      value={reviewForm.content}
                      onChange={(e) => setReviewForm({ ...reviewForm, content: e.target.value })}
                      className="w-full px-3 py-2 border rounded-lg text-sm outline-none"
                    />
                    {reviewImages.length < 3 && (
                      <FileUpload
                        value=""
                        label="+ Foto produk"
                        onChange={(url) => url && setReviewImages((imgs) => [...imgs, url])}
                      />
                    )}
                    {reviewImages.length > 0 && (
                      <div className="flex gap-2">
                        {reviewImages.map((img, i) => (
                          <div key={img} className="relative">
                            <img src={img} alt="" className="w-14 h-14 rounded-lg object-cover border border-gray-200" />
                            <button
                              onClick={() => setReviewImages((imgs) => imgs.filter((_, j) => j !== i))}
                              className="absolute -top-1.5 -right-1.5 w-5 h-5 rounded-full bg-red-500 text-white text-xs"
                              aria-label="Hapus foto"
                            >
                              ×
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                    <div className="flex gap-2">
                      <button
                        onClick={() => submitReview.mutate(it.id)}
                        disabled={submitReview.isPending || !reviewForm.content.trim()}
                        className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm disabled:opacity-50"
                      >
                        {submitReview.isPending ? 'Mengirim...' : 'Kirim Ulasan'}
                      </button>
                      <button onClick={() => setReviewFor(null)} className="px-4 py-2 rounded-lg border text-sm">
                        Batal
                      </button>
                    </div>
                  </div>
                ) : (
                  <button
                    onClick={() => setReviewFor(it.id)}
                    className="text-xs text-amber-600 hover:underline"
                  >
                    ✍️ Tulis ulasan
                  </button>
                )}
                <button
                  onClick={() => setReturnFor(it.id)}
                  className="text-xs text-red-500 hover:underline"
                >
                  ↩️ Ajukan retur
                </button>
              </div>
            )}
          </div>
        ))}
        <div className="border-t pt-3 space-y-1 text-sm">
          <div className="flex justify-between text-gray-500">
            <span>Subtotal</span><span>{formatIDR(data.subtotal)}</span>
          </div>
          {data.discount_amount > 0 && (
            <div className="flex justify-between text-green-600">
              <span>Diskon</span><span>−{formatIDR(data.discount_amount)}</span>
            </div>
          )}
          <div className="flex justify-between text-gray-500">
            <span>Ongkir</span><span>{formatIDR(data.shipping_fee)}</span>
          </div>
          <div className="flex justify-between font-bold pt-2">
            <span>Total</span><span className="text-amber-600">{formatIDR(data.total_amount)}</span>
          </div>
        </div>
      </div>

      <p className="text-xs text-gray-400 text-center">
        Dibuat {formatDate(data.placed_at)} · Pembayaran: {data.payment_status}
        {data.payment_status === 'paid' && paidVia ? ` via ${paidVia}` : ''} · Seller: {data.seller?.name}
        {data.external_payment_ref && ` · Ref: ${data.external_payment_ref}`}
      </p>
    </div>
  )
}
