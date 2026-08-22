import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { useSession } from '../../stores/session'
import type { Order } from '../../types'
import { formatIDR, formatDate, orderStatusColors, orderStatusLabels } from '../../lib/format'

export function SellerOrders() {
  const queryClient = useQueryClient()
  const { user } = useSession()
  const [trackingFor, setTrackingFor] = useState<string | null>(null)
  const [tracking, setTracking] = useState({ number: '', carrier: 'JNE' })

  const { data } = useQuery({
    queryKey: ['seller-orders'],
    queryFn: async () => (await api.get<{ orders: Order[] }>('/orders')).data.orders,
  })

  const mine = (data ?? []).filter((o) => o.seller_id === user?.id)

  const transition = useMutation({
    mutationFn: async ({ id, to }: { id: string; to: string }) =>
      api.post(`/seller/orders/${id}/transition`, { to }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['seller-orders'] }),
  })

  const shipWithTracking = useMutation({
    mutationFn: async ({ id }: { id: string }) =>
      api.post(`/seller/orders/${id}/transition`, { to: 'shipped', tracking_number: tracking.number, carrier: tracking.carrier }),
    onSuccess: () => {
      setTrackingFor(null)
      queryClient.invalidateQueries({ queryKey: ['seller-orders'] })
    },
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Pesanan Masuk</h1>
        <a href="/api/v1/seller/orders/export.csv" className="text-sm text-amber-600 hover:underline">
          ⬇ Ekspor CSV
        </a>
      </div>
      <div className="space-y-3">
        {mine.length === 0 && <p className="text-gray-500 text-sm">Belum ada pesanan untuk toko ini.</p>}
        {mine.map((o) => (
          <div key={o.id} className="bg-white border border-gray-200 rounded-xl p-5">
            <div className="flex justify-between items-start mb-3">
              <div>
                <p className="font-medium text-sm">{o.order_number}</p>
                <p className="text-xs text-gray-500">{formatDate(o.placed_at)}</p>
              </div>
              <span className={`px-3 py-1 rounded-full text-xs font-medium ${orderStatusColors[o.status]}`}>
                {orderStatusLabels[o.status]}
              </span>
            </div>
            <div className="space-y-2">
              {o.items.map((it) => (
                <div key={it.id} className="flex items-center justify-between text-sm">
                  <p className="line-clamp-1">
                    {it.product_name} <span className="text-gray-400">× {it.quantity}</span>
                  </p>
                  <p>{formatIDR(it.total)}</p>
                </div>
              ))}
            </div>
            <div className="flex items-center justify-between border-t mt-3 pt-3">
              <p className="text-sm text-gray-500">
                {o.shipping_method} · {o.payment_status} · <span className="font-bold text-gray-900">{formatIDR(o.total_amount)}</span>
              </p>
              <div className="flex gap-2">
                {o.status === 'paid' && (
                  <button
                    onClick={() => transition.mutate({ id: o.id, to: 'packed' })}
                    disabled={transition.isPending}
                    className="px-4 py-2 rounded-lg bg-indigo-600 text-white text-sm hover:bg-indigo-700 disabled:opacity-50"
                  >
                    Kemas Pesanan
                  </button>
                )}
                {o.status === 'packed' && (
                  <div className="flex gap-2 items-center">
                    {trackingFor === o.id ? (
                      <>
                        <select
                          value={tracking.carrier}
                          onChange={(e) => setTracking({ ...tracking, carrier: e.target.value })}
                          className="px-2 py-2 border rounded-lg text-xs"
                        >
                          <option>JNE</option>
                          <option>J&T</option>
                          <option>SiCepat</option>
                          <option>Pos Indonesia</option>
                          <option>GoSend</option>
                        </select>
                        <input
                          placeholder="Nomor resi"
                          value={tracking.number}
                          onChange={(e) => setTracking({ ...tracking, number: e.target.value })}
                          className="px-2 py-2 border rounded-lg text-xs w-36"
                        />
                        <button
                          onClick={() => shipWithTracking.mutate({ id: o.id })}
                          disabled={shipWithTracking.isPending || !tracking.number.trim()}
                          className="px-4 py-2 rounded-lg bg-purple-600 text-white text-sm hover:bg-purple-700 disabled:opacity-50"
                        >
                          Kirim
                        </button>
                        <button onClick={() => setTrackingFor(null)} className="text-xs text-gray-400">
                          Batal
                        </button>
                      </>
                    ) : (
                      <button
                        onClick={() => setTrackingFor(o.id)}
                        className="px-4 py-2 rounded-lg bg-purple-600 text-white text-sm hover:bg-purple-700"
                      >
                        Kirim (isi resi)
                      </button>
                    )}
                  </div>
                )}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
