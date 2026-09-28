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
  max_discount?: number
  usage_limit: number
  used_count: number
  per_user_limit: number
  valid_until?: string
  is_active: boolean
}

export function AdminCoupons() {
  const queryClient = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({
    code: '', type: 'percent', value: '', min_subtotal: '', usage_limit: '', per_user_limit: '1', valid_days: '30',
  })

  const { data } = useQuery({
    queryKey: ['admin-coupons'],
    queryFn: async () => (await api.get<{ coupons: Coupon[] }>('/admin/coupons')).data.coupons,
  })

  const create = useMutation({
    mutationFn: async () =>
      api.post('/admin/coupons', {
        code: form.code,
        type: form.type,
        value: Number(form.value),
        min_subtotal: Number(form.min_subtotal || 0),
        usage_limit: Number(form.usage_limit || 0),
        per_user_limit: Number(form.per_user_limit || 1),
        valid_days: Number(form.valid_days || 0),
      }),
    onSuccess: () => {
      setShowForm(false)
      setForm({ code: '', type: 'percent', value: '', min_subtotal: '', usage_limit: '', per_user_limit: '1', valid_days: '30' })
      queryClient.invalidateQueries({ queryKey: ['admin-coupons'] })
    },
  })

  const toggle = useMutation({
    mutationFn: async ({ id, active }: { id: string; active: boolean }) =>
      api.post(`/admin/coupons/${id}/toggle`, { active }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-coupons'] }),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Manajemen Kupon</h1>
        <button type="button"
          onClick={() => setShowForm(!showForm)}
          className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm font-medium hover:bg-amber-600"
        >
          {showForm ? 'Tutup' : '+ Kupon Baru'}
        </button>
      </div>

      {showForm && (
        <div className="bg-white border border-gray-200 rounded-xl p-5 grid grid-cols-2 gap-3">
          <input placeholder="Kode (mis. HEMAT20)" value={form.code} onChange={(e) => setForm({ ...form, code: e.target.value.toUpperCase() })} className="px-3 py-2 border rounded-lg text-sm outline-none" />
          <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none">
            <option value="percent">Persen (%)</option>
            <option value="fixed">Nominal (Rp)</option>
          </select>
          <input placeholder="Nilai" type="number" value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none" />
          <input placeholder="Min. subtotal (Rp)" type="number" value={form.min_subtotal} onChange={(e) => setForm({ ...form, min_subtotal: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none" />
          <input placeholder="Batas pemakaian total (0 = tak terbatas)" type="number" value={form.usage_limit} onChange={(e) => setForm({ ...form, usage_limit: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none" />
          <input placeholder="Batas per user" type="number" value={form.per_user_limit} onChange={(e) => setForm({ ...form, per_user_limit: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none" />
          <input placeholder="Masa berlaku (hari)" type="number" value={form.valid_days} onChange={(e) => setForm({ ...form, valid_days: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none" />
          <button type="button"
            onClick={() => create.mutate()}
            disabled={create.isPending || !form.code || !form.value}
            className="px-4 py-2 rounded-lg bg-gray-900 text-white text-sm disabled:opacity-50"
          >
            {create.isPending ? 'Membuat...' : 'Buat Kupon'}
          </button>
        </div>
      )}

      <div className="bg-white border border-gray-200 rounded-xl overflow-x-auto">
        <table className="w-full min-w-[44rem] text-sm">
          <thead className="bg-gray-50 text-left text-xs text-gray-500">
            <tr>
              <th className="px-4 py-3">Kode</th>
              <th className="px-4 py-3">Nilai</th>
              <th className="px-4 py-3">Min.</th>
              <th className="px-4 py-3">Pemakaian</th>
              <th className="px-4 py-3">Berlaku s/d</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Aksi</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {data?.map((c) => (
              <tr key={c.id}>
                <td className="px-4 py-3 font-mono font-semibold">{c.code}</td>
                <td className="px-4 py-3">
                  {c.type === 'percent' ? `${c.value}%` : formatIDR(c.value)}
                </td>
                <td className="px-4 py-3">{formatIDR(c.min_subtotal)}</td>
                <td className="px-4 py-3">{c.used_count} / {c.usage_limit || '∞'}</td>
                <td className="px-4 py-3">{c.valid_until ? c.valid_until.slice(0, 10) : '—'}</td>
                <td className="px-4 py-3">
                  <span className={`px-2 py-0.5 rounded-full text-xs ${c.is_active ? 'bg-green-100 text-green-700' : 'bg-gray-200 text-gray-600'}`}>
                    {c.is_active ? 'aktif' : 'nonaktif'}
                  </span>
                </td>
                <td className="px-4 py-3">
                  <button type="button"
                    onClick={() => toggle.mutate({ id: c.id, active: !c.is_active })}
                    className="text-xs text-amber-600 hover:underline"
                  >
                    {c.is_active ? 'Nonaktifkan' : 'Aktifkan'}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
