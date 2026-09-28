import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatDate } from '../../lib/format'

interface ReturnItem {
  id: string
  order_id: string
  item_name: string
  buyer_id: string
  seller_id: string
  reason: string
  description: string
  status: string
  resolution?: string
  amount?: number
  requested_at: string
}

export function AdminReturns() {
  const queryClient = useQueryClient()
  const [filter, setFilter] = useState('all')

  const { data } = useQuery({
    queryKey: ['admin-returns', filter],
    queryFn: async () =>
      (await api.get<{ returns: ReturnItem[] }>(`/admin/returns?status=${filter}`)).data.returns,
  })

  const refund = useMutation({
    mutationFn: async (id: string) => api.post(`/admin/returns/${id}/refund`, { note: 'refunded by admin' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-returns'] }),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Antrian Retur</h1>
        <select
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800"
        >
          <option value="all">Semua aktif</option>
          <option value="requested">Requested</option>
          <option value="approved">Approved</option>
          <option value="rejected">Rejected</option>
          <option value="refunded">Refunded</option>
        </select>
      </div>
      <div className="space-y-3">
        {data?.length === 0 && <p className="text-gray-500 text-sm">Tidak ada retur.</p>}
        {data?.map((r) => (
          <div key={r.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5">
            <div className="flex items-center justify-between">
              <p className="font-medium text-sm">{r.item_name}</p>
              <span className={`px-2.5 py-0.5 rounded-full text-xs font-medium ${
                r.status === 'requested' ? 'bg-amber-100 text-amber-700'
                : r.status === 'approved' ? 'bg-blue-100 text-blue-700'
                : r.status === 'refunded' ? 'bg-green-100 text-green-700'
                : 'bg-red-100 text-red-600'
              }`}>
                {r.status}
              </span>
            </div>
            <p className="text-xs text-gray-500 mt-1">
              Alasan: <span className="capitalize">{r.reason.replace('_', ' ')}</span> · {formatDate(r.requested_at)}
            </p>
            <p className="text-sm text-gray-600 dark:text-gray-300 mt-2">{r.description}</p>
            {r.status === 'approved' && (
              <button type="button"
                onClick={() => refund.mutate(r.id)}
                disabled={refund.isPending}
                className="mt-3 px-4 py-2 rounded-lg bg-green-600 text-white text-sm hover:bg-green-700 disabled:opacity-50"
              >
                Proses Refund
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
