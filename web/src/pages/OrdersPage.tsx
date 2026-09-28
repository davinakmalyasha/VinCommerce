import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Order } from '../types'
import { formatIDR, formatDate, orderStatusColors, orderStatusLabels } from '../lib/format'
import { QueryState } from '../components/QueryState'

const TABS = [
  { key: 'all', label: 'Semua' },
  { key: 'pending', label: 'Menunggu Bayar' },
  { key: 'paid', label: 'Dibayar' },
  { key: 'packed', label: 'Dikemas' },
  { key: 'shipped', label: 'Dikirim' },
  { key: 'delivered', label: 'Terkirim' },
  { key: 'completed', label: 'Selesai' },
  { key: 'cancelled', label: 'Dibatalkan' },
]

export function OrdersPage() {
  const [tab, setTab] = useState('all')

  const ordersQuery = useQuery({
    queryKey: ['orders'],
    queryFn: async () => (await api.get<{ orders: Order[] }>('/orders')).data.orders,
  })
  const data = ordersQuery.data

  const filtered = (data ?? []).filter((o) => tab === 'all' || o.status === tab)

  return (
    <div className="mx-auto max-w-4xl px-4 py-6">
      <h1 className="text-xl font-bold mb-4">Pesanan Saya</h1>
      <div className="flex gap-2 overflow-x-auto pb-2 mb-5">
        {TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => setTab(t.key)}
            className={`px-4 py-2 rounded-full text-sm whitespace-nowrap ${
              tab === t.key ? 'bg-amber-500 text-white' : 'bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>
      <QueryState query={ordersQuery} label="pesanan" className="!py-10 !text-left">
        <div className="space-y-4">
          {filtered.length === 0 && <p className="text-gray-500 text-sm">Belum ada pesanan.</p>}
          {filtered.map((o) => (
            <div key={o.id} className="bg-white border border-gray-200 rounded-xl p-5">
              <div className="flex items-center justify-between mb-3">
                <div>
                  <p className="font-medium text-sm">{o.order_number}</p>
                  <p className="text-xs text-gray-500">{formatDate(o.placed_at)}</p>
                </div>
                <div className="text-right">
                  <span className={`inline-block px-3 py-1 rounded-full text-xs font-medium ${orderStatusColors[o.status]}`}>
                    {orderStatusLabels[o.status]}
                  </span>
                  <p className="text-xs text-gray-500 mt-1">dari {o.seller?.name}</p>
                </div>
              </div>
              <div className="space-y-2">
                {o.items.map((it) => (
                  <div key={it.id} className="flex items-center gap-3">
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
                ))}
              </div>
              <div className="flex items-center justify-between mt-4 pt-3 border-t">
                <Link to={`/orders/${o.id}`} className="text-sm text-amber-600 hover:underline">
                  Detail & Lacak
                </Link>
                <div className="flex items-center gap-2">
                  <span className="text-xs text-gray-500">Total</span>
                  <span className="font-bold">{formatIDR(o.total_amount)}</span>
                </div>
              </div>
            </div>
          ))}
        </div>
      </QueryState>
    </div>
  )
}
