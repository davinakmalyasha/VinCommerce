import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api } from '../lib/api'
import { formatIDR, formatDate, orderStatusColors, orderStatusLabels } from '../lib/format'

interface TrackingEvent {
  id: number
  from_status?: string
  to_status: string
  note?: string
  created_at: string
}

export function TrackingPage() {
  const { number } = useParams()
  const navigate = useNavigate()
  const [lookup, setLookup] = useState('')

  const { data, isLoading, isError } = useQuery({
    queryKey: ['tracking', number],
    queryFn: async () =>
      (await api.get<{ order: { order_number: string; status: string; total_amount: number; shipping_method?: string; tracking_number?: string; carrier?: string; seller?: { name: string } }; events: TrackingEvent[] }>(`/orders/tracking/${number}`))
        .data,
    retry: false,
    enabled: !!number,
  })

  if (!number || isError) {
    return (
      <div className="mx-auto max-w-2xl px-4 py-10">
        <Link to="/" className="text-sm text-gray-400 hover:text-gray-700">← Beranda</Link>
        <h1 className="text-2xl font-extrabold mt-3 mb-1">Lacak Pesanan</h1>
        <p className="text-gray-500 text-sm mb-6">
          Masukkan nomor pesanan yang diterima lewat email. Lacak status secara publik.
        </p>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (lookup.trim()) navigate(`/tracking/${encodeURIComponent(lookup.trim().toUpperCase())}`)
          }}
          className="flex gap-2"
        >
          <input
            value={lookup}
            onChange={(e) => setLookup(e.target.value)}
            placeholder="Contoh: VC-20260824-0001"
            className="flex-1 px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <button
            type="submit"
            disabled={!lookup.trim()}
            className="px-6 py-3 rounded-xl bg-amber-500 text-white text-sm font-medium disabled:opacity-50"
          >
            Lacak
          </button>
        </form>
        {isError && <p className="text-sm text-red-600 mt-3">Pesanan tidak ditemukan. Periksa kembali nomornya.</p>}
      </div>
    )
  }

  if (!data || isLoading) {
    return <div className="mx-auto max-w-2xl px-4 py-16 text-center text-gray-500">Melacak pesanan...</div>
  }

  const { order, events } = data

  return (
    <div className="mx-auto max-w-2xl px-4 py-10">
      <Link to="/" className="text-sm text-gray-400 hover:text-gray-700">← Beranda</Link>
      <h1 className="text-2xl font-extrabold mt-3 mb-1">Lacak Pesanan</h1>
      <p className="text-gray-500 text-sm mb-6">
        Masukkan nomor pesanan yang diterima lewat email. Lacak status secara publik.
      </p>

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-6 mb-6">
        <div className="flex items-center justify-between">
          <div>
            <p className="font-mono text-lg font-bold">{order.order_number}</p>
            <p className="text-sm text-gray-500 mt-1">dari {order.seller?.name}</p>
            <p className="text-sm text-gray-500">{order.shipping_method}</p>
            {order.tracking_number && (
              <p className="text-xs text-blue-600 mt-1">
                📦 Resi ({order.carrier}): {order.tracking_number}
              </p>
            )}
          </div>
          <div className="text-right">
            <span className={`inline-block px-3 py-1.5 rounded-full text-sm font-medium ${orderStatusColors[order.status]}`}>
              {orderStatusLabels[order.status]}
            </span>
            <p className="font-bold mt-2">{formatIDR(order.total_amount)}</p>
          </div>
        </div>
      </div>

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-6">
        <h2 className="font-bold mb-4">Riwayat Pengiriman</h2>
        <div className="space-y-0">
          {events.map((e, i) => (
            <div key={e.id} className="flex gap-3">
              <div className="flex flex-col items-center">
                <div className={`w-3 h-3 rounded-full mt-1 ${i === events.length - 1 ? 'bg-amber-500' : 'bg-gray-300'}`} />
                {i < events.length - 1 && <div className="w-px flex-1 bg-gray-200" />}
              </div>
              <div className="pb-4">
                <p className="text-sm font-medium capitalize">{e.to_status.replace('_', ' ')}</p>
                <p className="text-xs text-gray-400">{formatDate(e.created_at)}</p>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
