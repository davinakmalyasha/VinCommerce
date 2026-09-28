import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
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

interface ClaimedCoupon {
  coupon_id: string
  code: string
  type: string
  value: number
  min_subtotal: number
  store_name?: string
}

export function VouchersPage() {
  const queryClient = useQueryClient()
  const [code, setCode] = useState('')
  const [msg, setMsg] = useState('')
  const [msgOk, setMsgOk] = useState(false)

  const { data } = useQuery({
    queryKey: ['vouchers'],
    queryFn: async () => (await api.get<{ vouchers: Voucher[] }>('/vouchers')).data.vouchers,
  })

  const { data: claims } = useQuery({
    queryKey: ['voucher-claims'],
    queryFn: async () => (await api.get<{ claims: ClaimedCoupon[] }>('/vouchers/claims')).data.claims,
  })

  const claimByCode = useMutation({
    // Accept the code explicitly so the voucher-list buttons claim THEIR code
    // (not whatever happens to sit in the search input).
    mutationFn: async (claimCode: string) =>
      (await api.post<{ coupon: { code: string } }>('/vouchers/claim', { code: claimCode })).data,
    onSuccess: (res) => {
      setMsg(`Kupon ${res.coupon.code} berhasil diklaim! 🎉`)
      setMsgOk(true)
      setCode('')
      queryClient.invalidateQueries({ queryKey: ['voucher-claims'] })
    },
    onError: (e: Error) => {
      setMsg(e.message)
      setMsgOk(false)
    },
  })

  return (
    <div className="mx-auto max-w-3xl px-4 py-8 space-y-8">
      <div>
        <h1 className="text-2xl font-extrabold mb-2">Kupon &amp; Voucher</h1>
        <p className="text-gray-500 text-sm">Klaim kupon ke akunmu, lalu pilih saat checkout.</p>
      </div>

      <section className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-5">
        <h2 className="font-bold text-sm mb-3">Klaim dengan Kode</h2>
        <div className="flex gap-2">
          <input
            value={code}
            onChange={(e) => setCode(e.target.value.toUpperCase())}
            placeholder="Masukkan kode kupon"
            className="flex-1 px-4 py-2.5 border rounded-xl text-sm uppercase outline-none focus:border-amber-400"
          />
          <button type="button"
            onClick={() => claimByCode.mutate(code.trim())}
            disabled={claimByCode.isPending || !code.trim()}
            className="px-6 py-2.5 rounded-xl bg-amber-500 text-white text-sm font-medium hover:bg-amber-600 disabled:opacity-50"
          >
            {claimByCode.isPending ? 'Mengklaim...' : 'Klaim'}
          </button>
        </div>
        {msg && <p className={`text-xs mt-2 ${msgOk ? 'text-green-600' : 'text-red-600'}`}>{msg}</p>}
      </section>

      <section className="space-y-3">
        <h2 className="font-bold">🎟️ Kupon Saya ({claims?.length ?? 0})</h2>
        {claims?.length === 0 && (
          <p className="text-sm text-gray-500">Belum ada kupon terklaim — klaim dari daftar di bawah!</p>
        )}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          {claims?.map((c) => (
            <div key={c.code} className="bg-gradient-to-r from-amber-50 to-orange-50 dark:from-amber-900/30 dark:to-orange-900/20 border border-amber-200 dark:border-amber-800 rounded-xl p-4 flex items-center justify-between">
              <div>
                <p className="text-amber-700 dark:text-amber-300 font-bold">{c.code}</p>
                <p className="text-xs text-amber-600 dark:text-amber-400">
                  {c.type === 'percent' ? `${c.value}% off` : `${formatIDR(c.value)} off`}
                  {c.min_subtotal > 0 ? ` · min ${formatIDR(c.min_subtotal)}` : ''}
                </p>
                {c.store_name && <p className="text-xs text-gray-400">{c.store_name}</p>}
              </div>
              <span className="px-2 py-1 rounded-full bg-green-100 text-green-700 text-xs">Siap dipakai</span>
            </div>
          ))}
        </div>
      </section>

      <section className="space-y-3">
        <h2 className="font-bold">Kupon Tersedia</h2>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {data?.length === 0 && <p className="text-gray-500 text-sm">Belum ada kupon toko tersedia.</p>}
          {data?.map((v) => {
            const claimed = claims?.some((c) => c.code.toUpperCase() === v.code.toUpperCase())
            return (
              <div key={v.id} className="bg-white dark:bg-gray-900 border border-amber-200 dark:border-amber-800 rounded-2xl p-5 flex items-center justify-between gap-3">
                <div>
                  <p className="text-amber-600 font-bold text-lg">
                    {v.type === 'percent' ? `${v.value}%` : formatIDR(v.value)}
                  </p>
                  <p className="font-medium text-sm mt-0.5">{v.store_name ?? 'VinCommerce'}</p>
                  <p className="text-xs text-gray-500 mt-1">Min. belanja {formatIDR(v.min_subtotal)}</p>
                  {v.valid_until && <p className="text-xs text-gray-400">Berlaku s/d {v.valid_until.slice(0, 10)}</p>}
                </div>
                <button type="button"
                  onClick={() => claimByCode.mutate(v.code)}
                  disabled={claimed || claimByCode.isPending}
                  className={`px-4 py-2.5 rounded-xl text-sm font-medium shrink-0 ${
                    claimed
                      ? 'bg-green-100 text-green-700 cursor-default'
                      : 'bg-amber-500 text-white hover:bg-amber-600 disabled:opacity-50'
                  }`}
                >
                  {claimed ? '✓ Diklaim' : `Klaim`}
                </button>
              </div>
            )
          })}
        </div>
      </section>
    </div>
  )
}
