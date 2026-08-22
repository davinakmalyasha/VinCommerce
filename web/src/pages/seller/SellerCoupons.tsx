import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatIDR } from '../../lib/format'

interface Coupon {
  id: string
  code: string
  type: string
  value: number
  min_subtotal: number
  used_count: number
  usage_limit: number
  is_active: boolean
  valid_until?: string
}

export function SellerCoupons() {
  const queryClient = useQueryClient()
  const [form, setForm] = useState({ code: '', type: 'percent', value: '', min_subtotal: '', per_user_limit: '1', valid_days: '30' })

  const { data } = useQuery({
    queryKey: ['seller-coupons'],
    queryFn: async () => (await api.get<{ coupons: Coupon[] }>('/seller/coupons')).data.coupons,
  })

  const create = useMutation({
    mutationFn: async () =>
      api.post('/seller/coupons', {
        code: form.code, type: form.type, value: Number(form.value),
        min_subtotal: Number(form.min_subtotal || 0), per_user_limit: Number(form.per_user_limit),
        valid_days: Number(form.valid_days || 0),
      }),
    onSuccess: () => {
      setForm({ code: '', type: 'percent', value: '', min_subtotal: '', per_user_limit: '1', valid_days: '30' })
      queryClient.invalidateQueries({ queryKey: ['seller-coupons'] })
    },
  })

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Kupon Toko</h1>
      <p className="text-sm text-gray-500">
        Kupon toko hanya berlaku untuk produkmu dan muncul di halaman kumpulan kupon pembeli.
      </p>

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 grid grid-cols-2 gap-3">
        <input placeholder="Kode (mis. TOKOHEMAT)" value={form.code} onChange={(e) => setForm({ ...form, code: e.target.value.toUpperCase() })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
        <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800">
          <option value="percent">Persen (%)</option>
          <option value="fixed">Nominal (Rp)</option>
        </select>
        <input placeholder="Nilai" type="number" value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
        <input placeholder="Min. subtotal (Rp)" type="number" value={form.min_subtotal} onChange={(e) => setForm({ ...form, min_subtotal: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
        <input placeholder="Masa berlaku (hari)" type="number" value={form.valid_days} onChange={(e) => setForm({ ...form, valid_days: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
        <button
          onClick={() => create.mutate()}
          disabled={create.isPending || !form.code || !form.value}
          className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm disabled:opacity-50"
        >
          {create.isPending ? 'Membuat...' : 'Buat Kupon'}
        </button>
      </div>

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 dark:bg-gray-800 text-left text-xs text-gray-500">
            <tr>
              <th className="px-4 py-3">Kode</th>
              <th className="px-4 py-3">Nilai</th>
              <th className="px-4 py-3">Min.</th>
              <th className="px-4 py-3">Pemakaian</th>
              <th className="px-4 py-3">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
            {data?.map((c) => (
              <tr key={c.id}>
                <td className="px-4 py-3 font-mono font-semibold">{c.code}</td>
                <td className="px-4 py-3">{c.type === 'percent' ? `${c.value}%` : formatIDR(c.value)}</td>
                <td className="px-4 py-3">{formatIDR(c.min_subtotal)}</td>
                <td className="px-4 py-3">{c.used_count} / {c.usage_limit || '∞'}</td>
                <td className="px-4 py-3">
                  <span className={`px-2 py-0.5 rounded-full text-xs ${c.is_active ? 'bg-green-100 text-green-700' : 'bg-gray-200 text-gray-600'}`}>
                    {c.is_active ? 'aktif' : 'nonaktif'}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
