import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import { formatDate } from '../lib/format'
import { notificationHref } from '../lib/notifications'
import { QueryState } from '../components/QueryState'

interface Notification {
  id: number
  type: string
  title: string
  body: string
  data: Record<string, unknown>
  read_at: string | null
  created_at: string
}

const TYPE_LABELS: Record<string, string> = {
  order: 'Pesanan',
  payment: 'Pembayaran',
  price_alert: 'Turun harga',
  back_in_stock: 'Stok kembali',
  store_new_product: 'Produk baru toko diikuti',
  low_stock: 'Stok menipis',
  marketing: 'Promo',
  cart_recovery: 'Pengingat keranjang',
  moderation: 'Moderasi',
  ticket: 'Tiket dukungan',
  qa: 'Tanya jawab',
}

export function NotificationsPage() {
  const { user } = useSession()
  const queryClient = useQueryClient()
  const [filter, setFilter] = useState('all')

  const notificationsQuery = useQuery({
    queryKey: ['notifications-all', filter],
    queryFn: async () =>
      (
        await api.get<{ notifications: Notification[] }>(
          `/notifications?limit=50${filter === 'all' ? '' : `&type=${encodeURIComponent(filter)}`}`,
        )
      ).data.notifications,
    enabled: !!user,
  })
  const data = notificationsQuery.data

  const markAll = useMutation({
    mutationFn: async (ids: number[]) => api.post('/notifications/read', { ids }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notifications-all'] })
      queryClient.invalidateQueries({ queryKey: ['notif-unread'] })
    },
    onError: (e: Error) => setError(e.message || 'Gagal menandai notifikasi.'),
  })
  const [error, setError] = useState('')

  if (!user) return <Navigate to="/login" replace />

  const unreadIds = (data ?? []).filter((n) => !n.read_at).map((n) => n.id)
  const groups = (data ?? []).reduce<Record<string, Notification[]>>((acc, n) => {
    const key = n.type || 'other'
    acc[key] = acc[key] ? [...acc[key], n] : [n]
    return acc
  }, {})
  const groupKeys = Object.keys(groups).sort()

  return (
    <div className="mx-auto max-w-3xl px-4 py-6">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-xl font-bold">Notifikasi</h1>
        {unreadIds.length > 0 && (
          <button
            type="button"
            onClick={() => markAll.mutate(unreadIds)}
            disabled={markAll.isPending}
            className="text-sm text-amber-600 hover:underline disabled:opacity-50"
          >
            Tandai semua dibaca
          </button>
        )}
      </div>
      <div className="flex gap-2 mb-6 overflow-x-auto pb-1">
        <button
          type="button"
          onClick={() => setFilter('all')}
          aria-pressed={filter === 'all'}
          className={`px-3 py-1.5 rounded-full text-xs font-medium whitespace-nowrap ${
            filter === 'all' ? 'bg-amber-500 text-white' : 'bg-white border border-gray-200 hover:border-amber-400'
          }`}
        >
          Semua
        </button>
        {Object.keys(TYPE_LABELS).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setFilter(filter === t ? 'all' : t)}
            aria-pressed={filter === t}
            className={`px-3 py-1.5 rounded-full text-xs font-medium whitespace-nowrap ${
              filter === t ? 'bg-amber-500 text-white' : 'bg-white border border-gray-200 hover:border-amber-400'
            }`}
          >
            {TYPE_LABELS[t]}
          </button>
        ))}
      </div>
      {error && (
        <p role="alert" className="text-sm text-red-600 mb-3 bg-red-50 dark:bg-red-950/40 rounded-lg p-2.5">{error}</p>
      )}
      <QueryState query={notificationsQuery} label="notifikasi" className="!text-left">
        <div className="space-y-5">
          {data?.length === 0 && <p className="text-gray-500 text-sm">Belum ada notifikasi.</p>}
          {groupKeys.map((key) => {
            const items = groups[key]
            const unreadInGroup = items.filter((n) => !n.read_at).map((n) => n.id)
            return (
              <section key={key}>
                <div className="flex items-center justify-between mb-2">
                  <h2 className="text-sm font-bold text-gray-500">{TYPE_LABELS[key] ?? key}</h2>
                  {unreadInGroup.length > 0 && (
                    <button
                      type="button"
                      onClick={() => markAll.mutate(unreadInGroup)}
                      disabled={markAll.isPending}
                      className="text-xs text-amber-600 hover:underline disabled:opacity-50"
                    >
                      Tandai grup dibaca
                    </button>
                  )}
                </div>
                <div className="space-y-2">
                  {items.map((n) => {
            const href = notificationHref(n.data)
            const content = (
              <div
                className={`bg-white dark:bg-gray-900 border rounded-xl p-4 ${
                  n.read_at ? 'border-gray-200 dark:border-gray-700' : 'border-amber-300 dark:border-amber-600'
                }`}
              >
                <div className="flex items-center justify-between">
                  <p className="font-medium text-sm">{n.title}</p>
                  {!n.read_at && <span className="w-2 h-2 rounded-full bg-amber-500" />}
                </div>
                <p className="text-sm text-gray-600 dark:text-gray-300 mt-1">{n.body}</p>
                <p className="text-xs text-gray-400 mt-1">{formatDate(n.created_at)}</p>
              </div>
            )
            return href ? (
              <Link key={n.id} to={href} className="block">
                {content}
              </Link>
            ) : (
              <div key={n.id}>{content}</div>
            )
                  })}
                </div>
              </section>
            )
          })}
        </div>
      </QueryState>
    </div>
  )
}
