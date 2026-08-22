import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatIDR } from '../../lib/format'

interface ShippingMethod {
  id: string
  code: string
  name: string
  base_fee: number
  per_kg_fee: number
  min_days: number
  max_days: number
  is_active: boolean
}

export function AdminShipping() {
  const queryClient = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ code: '', name: '', base_fee: '', per_kg_fee: '', min_days: '3', max_days: '7' })

  const { data } = useQuery({
    queryKey: ['admin-shipping'],
    queryFn: async () => (await api.get<{ methods: ShippingMethod[] }>('/admin/shipping')).data.methods,
  })

  const create = useMutation({
    mutationFn: async () =>
      api.post('/admin/shipping', {
        code: form.code,
        name: form.name,
        base_fee: Number(form.base_fee),
        per_kg_fee: Number(form.per_kg_fee || 0),
        min_days: Number(form.min_days),
        max_days: Number(form.max_days),
      }),
    onSuccess: () => {
      setShowForm(false)
      setForm({ code: '', name: '', base_fee: '', per_kg_fee: '', min_days: '3', max_days: '7' })
      queryClient.invalidateQueries({ queryKey: ['admin-shipping'] })
    },
  })

  const toggle = useMutation({
    mutationFn: async ({ id, active }: { id: string; active: boolean }) =>
      api.post(`/admin/shipping/${id}/toggle`, { active }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-shipping'] }),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Metode Pengiriman</h1>
        <button
          onClick={() => setShowForm(!showForm)}
          className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm font-medium hover:bg-amber-600"
        >
          {showForm ? 'Tutup' : '+ Kurir Baru'}
        </button>
      </div>

      {showForm && (
        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 grid grid-cols-2 gap-3">
          <input placeholder="Kode (mis. jne-reg)" value={form.code} onChange={(e) => setForm({ ...form, code: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Nama (mis. JNE Reguler)" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Biaya dasar (Rp)" type="number" value={form.base_fee} onChange={(e) => setForm({ ...form, base_fee: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Biaya per kg (Rp)" type="number" value={form.per_kg_fee} onChange={(e) => setForm({ ...form, per_kg_fee: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Min hari" type="number" value={form.min_days} onChange={(e) => setForm({ ...form, min_days: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Max hari" type="number" value={form.max_days} onChange={(e) => setForm({ ...form, max_days: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <button
            onClick={() => create.mutate()}
            disabled={create.isPending || !form.code || !form.name}
            className="px-4 py-2 rounded-lg bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white text-sm disabled:opacity-50"
          >
            {create.isPending ? 'Membuat...' : 'Tambah Kurir'}
          </button>
        </div>
      )}

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 dark:bg-gray-800 text-left text-xs text-gray-500">
            <tr>
              <th className="px-4 py-3">Kurir</th>
              <th className="px-4 py-3">Biaya Dasar</th>
              <th className="px-4 py-3">Per Kg</th>
              <th className="px-4 py-3">Estimasi</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Aksi</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
            {data?.map((m) => (
              <tr key={m.id}>
                <td className="px-4 py-3">
                  <p className="font-medium">{m.name}</p>
                  <p className="text-xs text-gray-400">{m.code}</p>
                </td>
                <td className="px-4 py-3">{formatIDR(m.base_fee)}</td>
                <td className="px-4 py-3">{formatIDR(m.per_kg_fee)}</td>
                <td className="px-4 py-3">{m.min_days}-{m.max_days} hari</td>
                <td className="px-4 py-3">
                  <span className={`px-2 py-0.5 rounded-full text-xs ${m.is_active ? 'bg-green-100 text-green-700' : 'bg-gray-200 text-gray-600'}`}>
                    {m.is_active ? 'aktif' : 'nonaktif'}
                  </span>
                </td>
                <td className="px-4 py-3">
                  <button
                    onClick={() => toggle.mutate({ id: m.id, active: !m.is_active })}
                    className="text-xs text-amber-600 hover:underline"
                  >
                    {m.is_active ? 'Nonaktifkan' : 'Aktifkan'}
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
