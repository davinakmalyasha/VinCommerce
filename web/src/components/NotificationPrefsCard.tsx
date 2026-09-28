import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

interface Pref {
  category: string
  in_app: boolean
  email: boolean
}

const LABELS: Record<string, string> = {
  order: 'Pesanan',
  payment: 'Pembayaran',
  price_alert: 'Turun harga',
  back_in_stock: 'Stok kembali',
  store_new_product: 'Produk baru toko diikuti',
  low_stock: 'Stok menipis (penjual)',
  marketing: 'Promo & penawaran',
  cart_recovery: 'Pengingat keranjang',
  moderation: 'Moderasi produk',
  ticket: 'Tiket dukungan',
}

export function NotificationPrefsCard() {
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['notif-prefs'],
    queryFn: async () => (await api.get<{ preferences: Pref[] }>('/notifications/preferences')).data.preferences,
  })

  const update = useMutation({
    mutationFn: async (p: { category: string; inApp?: boolean; email?: boolean }) =>
      api.put('/notifications/preferences', {
        category: p.category,
        in_app: p.inApp ?? true,
        email: p.email ?? false,
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['notif-prefs'] }),
    onError: (e: Error) => setError(e.message || 'Gagal menyimpan preferensi.'),
  })
  const [error, setError] = useState('')
  const [status, setStatus] = useState('')

  return (
    <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 space-y-2">
      <h2 className="font-bold text-sm mb-1">🔔 Preferensi Notifikasi</h2>
      <p className="text-xs text-gray-500">
        Atur notifikasi in-app dan email per kategori. Email transaksional
        (reset password, verifikasi, status pesanan) tetap terkirim.
      </p>
      {error && <p role="alert" className="text-xs text-red-600">{error}</p>}
      <p role="status" className="sr-only">{status}</p>
      <div className="divide-y divide-gray-100 dark:divide-gray-800">
        <div className="flex items-center justify-between py-2 text-[11px] uppercase tracking-wide text-gray-400 font-medium">
          <span id="notif-prefs-head">Kategori</span>
          <span className="flex gap-6 pr-1" aria-hidden="true">
            <span>In-app</span>
            <span>Email</span>
          </span>
        </div>
        {(data ?? []).map((p) => {
          const label = LABELS[p.category] ?? p.category
          return (
            <div key={p.category} className="flex items-center justify-between py-2.5 text-sm">
              <span id={`notif-pref-${p.category}`}>{label}</span>
              <span className="flex items-center gap-6 pr-1">
                <input
                  id={`notif-pref-${p.category}-inapp`}
                  type="checkbox"
                  aria-labelledby={`notif-pref-${p.category}`}
                  aria-label={`Notifikasi in-app ${label}`}
                  checked={p.in_app}
                  onChange={(e) => {
                    setError('')
                    update.mutate(
                      { category: p.category, inApp: e.target.checked, email: p.email },
                      { onSuccess: () => setStatus(`Preferensi ${label} disimpan.`) },
                    )
                  }}
                  disabled={update.isPending}
                  className="accent-amber-500 w-4 h-4"
                />
                <input
                  id={`notif-pref-${p.category}-email`}
                  type="checkbox"
                  aria-labelledby={`notif-pref-${p.category}`}
                  aria-label={`Email ${label}`}
                  checked={p.email}
                  onChange={(e) => {
                    setError('')
                    update.mutate(
                      { category: p.category, inApp: p.in_app, email: e.target.checked },
                      { onSuccess: () => setStatus(`Preferensi ${label} disimpan.`) },
                    )
                  }}
                  disabled={update.isPending}
                  className="accent-amber-500 w-4 h-4"
                />
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
