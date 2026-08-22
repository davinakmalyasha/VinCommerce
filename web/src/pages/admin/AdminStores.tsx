import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatIDR } from '../../lib/format'

interface PendingStore {
  id: string
  name: string
  owner_id: string
  status: string
  joined_at: string
}

export function AdminStores() {
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['admin-stores'],
    queryFn: async () => (await api.get<{ stores: PendingStore[] }>('/admin/stores')).data.stores,
  })

  const decide = useMutation({
    mutationFn: async ({ id, decision }: { id: string; decision: string }) =>
      api.post(`/admin/stores/${id}/decide`, { decision }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-stores'] }),
  })

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Persetujuan Toko ({data?.length ?? 0})</h1>
      <div className="space-y-3">
        {data?.length === 0 && <p className="text-gray-500 text-sm">Tidak ada toko menunggu persetujuan.</p>}
        {data?.map((s) => (
          <div key={s.id} className="bg-white border border-gray-200 rounded-xl p-5 flex items-center justify-between">
            <div>
              <p className="font-medium">{s.name}</p>
              <p className="text-xs text-gray-500">
                Pemilik: {s.owner_id.slice(0, 8)}… · Bergabung {s.joined_at.slice(0, 10)}
              </p>
            </div>
            <div className="flex gap-2">
              <button
                onClick={() => decide.mutate({ id: s.id, decision: 'approve' })}
                className="px-4 py-2 rounded-lg bg-green-600 text-white text-sm hover:bg-green-700"
              >
                Setujui
              </button>
              <button
                onClick={() => decide.mutate({ id: s.id, decision: 'reject' })}
                className="px-4 py-2 rounded-lg border border-red-300 text-red-600 text-sm hover:bg-red-50"
              >
                Tolak
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

export function AdminOverview() {
  const { data } = useQuery({
    queryKey: ['admin-analytics'],
    queryFn: async () =>
      (
        await api.get<{
          summary: { gmv: number; order_count: number; buyer_count: number; product_count: number; store_count: number; avg_order: number }
        }>('/admin/analytics')
      ).data,
  })

  const cards = [
    { label: 'GMV (30 hari)', value: formatIDR(data?.summary.gmv ?? 0) },
    { label: 'Pesanan', value: String(data?.summary.order_count ?? 0) },
    { label: 'Pembeli', value: String(data?.summary.buyer_count ?? 0) },
    { label: 'Produk Aktif', value: String(data?.summary.product_count ?? 0) },
    { label: 'Toko Aktif', value: String(data?.summary.store_count ?? 0) },
    { label: 'Rata-rata Pesanan', value: formatIDR(data?.summary.avg_order ?? 0) },
  ]

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-bold">Ringkasan Platform</h1>
      <div className="grid grid-cols-2 lg:grid-cols-3 gap-4">
        {cards.map((c) => (
          <div key={c.label} className="bg-white border border-gray-200 rounded-xl p-5">
            <p className="text-xs text-gray-500">{c.label}</p>
            <p className="text-xl font-bold mt-1 text-amber-600">{c.value}</p>
          </div>
        ))}
      </div>
    </div>
  )
}
