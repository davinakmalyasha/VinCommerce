import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { formatIDR } from '../lib/format'

interface Voucher {
  id: string
  code: string
  type: string
  value: number
  min_subtotal: number
  valid_until?: string
  store_name?: string
}

export function VouchersPage() {
  const { data } = useQuery({
    queryKey: ['vouchers'],
    queryFn: async () => (await api.get<{ vouchers: Voucher[] }>('/vouchers')).data.vouchers,
  })

  const copy = (code: string) => {
    navigator.clipboard?.writeText(code)
    alert(`Kode ${code} disalin — pakai di checkout!`)
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-8">
      <h1 className="text-2xl font-extrabold mb-2">Kumpulkan Kupon Toko</h1>
      <p className="text-gray-500 text-sm mb-6">
        Kupon eksklusif dari toko favoritmu. Salin kodenya dan gunakan saat checkout.
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {data?.length === 0 && <p className="text-gray-500 text-sm">Belum ada kupon toko tersedia.</p>}
        {data?.map((v) => (
          <div key={v.id} className="bg-white dark:bg-gray-900 border border-amber-200 dark:border-amber-800 rounded-2xl p-5 flex items-center justify-between gap-3">
            <div>
              <p className="text-amber-600 font-bold text-lg">
                {v.type === 'percent' ? `${v.value}%` : formatIDR(v.value)}
              </p>
              <p className="font-medium text-sm mt-0.5">{v.store_name ?? 'Toko'}</p>
              <p className="text-xs text-gray-500 mt-1">Min. belanja {formatIDR(v.min_subtotal)}</p>
              {v.valid_until && <p className="text-xs text-gray-400">Berlaku s/d {v.valid_until.slice(0, 10)}</p>}
            </div>
            <button
              onClick={() => copy(v.code)}
              className="px-4 py-2.5 rounded-xl bg-amber-500 text-white text-sm font-medium hover:bg-amber-600 shrink-0"
            >
              Klaim {v.code}
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}
