import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatDate } from '../../lib/format'

interface ReturnItem {
  id: string
  order_id: string
  item_name: string
  reason: string
  description: string
  status: string
  resolution?: string
  seller_note?: string
  requested_at: string
}

export function SellerReturns() {
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['seller-returns'],
    queryFn: async () => (await api.get<{ returns: ReturnItem[] }>('/seller/returns')).data.returns,
  })

  const decide = useMutation({
    mutationFn: async ({ id, decision, note }: { id: string; decision: string; note?: string }) =>
      api.post(`/seller/returns/${id}/decide`, { decision, note }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['seller-returns'] }),
  })

  const statusStyle: Record<string, string> = {
    requested: 'bg-amber-100 text-amber-700',
    approved: 'bg-blue-100 text-blue-700',
    rejected: 'bg-red-100 text-red-600',
    refunded: 'bg-green-100 text-green-700',
    closed: 'bg-gray-200 text-gray-600',
  }

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Permintaan Retur</h1>
      <div className="space-y-3">
        {data?.length === 0 && <p className="text-gray-500 text-sm">Tidak ada permintaan retur.</p>}
        {data?.map((r) => (
          <div key={r.id} className="bg-white border border-gray-200 rounded-xl p-5">
            <div className="flex justify-between items-start">
              <div className="flex-1">
                <div className="flex items-center gap-3">
                  <p className="font-medium text-sm">{r.item_name}</p>
                  <span className={`px-2.5 py-0.5 rounded-full text-xs font-medium ${statusStyle[r.status]}`}>
                    {r.status}
                  </span>
                </div>
                <p className="text-xs text-gray-500 mt-1">
                  Alasan: <span className="capitalize">{r.reason.replace('_', ' ')}</span> · {formatDate(r.requested_at)}
                </p>
                <p className="text-sm text-gray-600 mt-2">{r.description}</p>
                {r.seller_note && (
                  <p className="text-xs text-blue-600 mt-1">Catatan Anda: {r.seller_note}</p>
                )}
              </div>
              {r.status === 'requested' && (
                <div className="flex gap-2 ml-4">
                  <button type="button"
                    onClick={() => decide.mutate({ id: r.id, decision: 'approved', note: 'Disetujui, silakan kirim barang kembali' })}
                    disabled={decide.isPending}
                    className="px-4 py-2 rounded-lg bg-green-600 text-white text-sm hover:bg-green-700 disabled:opacity-50"
                  >
                    Setujui
                  </button>
                  <button type="button"
                    onClick={() => decide.mutate({ id: r.id, decision: 'rejected', note: 'Ditolak' })}
                    disabled={decide.isPending}
                    className="px-4 py-2 rounded-lg border border-red-300 text-red-600 text-sm hover:bg-red-50"
                  >
                    Tolak
                  </button>
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
