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
  })

  return (
    <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 space-y-2">
      <h2 className="font-bold text-sm mb-1">🔔 Preferensi Notifikasi</h2>
      <p className="text-xs text-gray-500">
        Atur notifikasi in-app dan email per kategori. Email transaksional
        (reset password, verifikasi, status pesanan) tetap terkirim.
      </p>
      <div className="divide-y divide-gray-100 dark:divide-gray-800">
        <div className="flex items-center justify-between py-2 text-[11px] uppercase tracking-wide text-gray-400 font-medium">
          <span>Kategori</span>
          <span className="flex gap-6 pr-1">
            <span>In-app</span>
            <span>Email</span>
          </span>
        </div>
        {(data ?? []).map((p) => (
          <div key={p.category} className="flex items-center justify-between py-2.5 text-sm">
            <span>{LABELS[p.category] ?? p.category}</span>
            <span className="flex items-center gap-6 pr-1">
              <input
                type="checkbox"
                aria-label={`Notifikasi in-app ${LABELS[p.category] ?? p.category}`}
                checked={p.in_app}
                onChange={(e) => update.mutate({ category: p.category, inApp: e.target.checked, email: p.email })}
                disabled={update.isPending}
                className="accent-amber-500 w-4 h-4"
              />
              <input
                type="checkbox"
                aria-label={`Email ${LABELS[p.category] ?? p.category}`}
                checked={p.email}
                onChange={(e) => update.mutate({ category: p.category, inApp: p.in_app, email: e.target.checked })}
                disabled={update.isPending}
                className="accent-amber-500 w-4 h-4"
              />
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}
