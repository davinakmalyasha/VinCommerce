import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { formatIDR, formatDate, orderStatusColors } from '../lib/format'

interface ReturnItem {
  id: string
  order_id: string
  item_name?: string
  amount?: number
  reason: string
  description: string
  status: string
  resolution?: string
  seller_note?: string
  admin_note?: string
  requested_at: string
  resolved_at?: string | null
}

const returnStatusLabels: Record<string, string> = {
  requested: 'Diajukan',
  approved: 'Disetujui — kirim barang',
  rejected: 'Ditolak',
  returned: 'Barang diterima penjual',
  refunded: 'Refund diterbitkan',
  closed: 'Selesai',
}

export function MyReturnsPage() {
  const queryClient = useQueryClient()
  const [disputeFor, setDisputeFor] = useState<string | null>(null)
  const [disputeDesc, setDisputeDesc] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['my-returns'],
    queryFn: async () => (await api.get<{ returns: ReturnItem[] }>('/returns')).data.returns,
  })

  const escalate = useMutation({
    mutationFn: async (returnId: string) =>
      api.post('/disputes', {
        return_id: returnId,
        subject: 'Eskalasi keputusan retur',
        description: disputeDesc,
      }),
    onSuccess: () => {
      setDisputeFor(null)
      setDisputeDesc('')
      queryClient.invalidateQueries({ queryKey: ['my-returns'] })
      alert('Sengketa diajukan. Admin akan meninjau.')
    },
    onError: (e: Error) => alert(e.message),
  })

  return (
    <div className="mx-auto max-w-3xl px-4 py-8 space-y-5">
      <div>
        <Link to="/account" className="text-sm text-gray-400 hover:text-gray-700">← Akun</Link>
        <h1 className="text-xl font-bold mt-1">Retur Saya</h1>
      </div>
      {isLoading && <p className="text-sm text-gray-500">Memuat...</p>}
      {!isLoading && data?.length === 0 && (
        <p className="text-sm text-gray-500">Belum ada pengajuan retur.</p>
      )}
      <div className="space-y-3">
        {data?.map((r) => (
          <div key={r.id} className="bg-white border border-gray-200 rounded-xl p-5 space-y-2">
            <div className="flex items-start justify-between">
              <div>
                <p className="font-medium text-sm">{r.item_name || 'Item pesanan'}</p>
                <p className="text-xs text-gray-500">
                  Pesanan <Link to={`/orders/${r.order_id}`} className="text-amber-600 hover:underline">#{r.order_id.slice(0, 8)}</Link>
                  {' · '}Diajukan {formatDate(r.requested_at)}
                </p>
              </div>
              <span className={`px-3 py-1 rounded-full text-xs font-medium ${orderStatusColors[r.status] ?? ''}`}>
                {returnStatusLabels[r.status] ?? r.status}
              </span>
            </div>
            <p className="text-sm"><span className="text-gray-400">Alasan:</span> {r.reason}{r.description ? ` — ${r.description}` : ''}</p>
            {r.seller_note && <p className="text-sm"><span className="text-gray-400">Catatan penjual:</span> {r.seller_note}</p>}
            {r.admin_note && <p className="text-sm"><span className="text-gray-400">Keputusan admin:</span> {r.admin_note}</p>}
            {r.amount ? <p className="text-sm font-bold text-amber-600">{formatIDR(r.amount)}</p> : null}
            {r.status === 'rejected' && disputeFor !== r.id && (
              <button onClick={() => setDisputeFor(r.id)} className="text-xs text-red-600 hover:underline">
                Tidak setuju? Eskalasi jadi sengketa
              </button>
            )}
            {disputeFor === r.id && (
              <div className="space-y-2 bg-red-50 border border-red-100 rounded-lg p-3">
                <textarea
                  value={disputeDesc}
                  onChange={(e) => setDisputeDesc(e.target.value)}
                  rows={2}
                  placeholder="Jelaskan mengapa kamu menolak keputusan retur..."
                  className="w-full px-3 py-2 border rounded-lg text-sm outline-none"
                />
                <div className="flex gap-2">
                  <button
                    onClick={() => escalate.mutate(r.id)}
                    disabled={escalate.isPending || !disputeDesc.trim()}
                    className="px-4 py-2 rounded-lg bg-red-600 text-white text-sm disabled:opacity-50"
                  >
                    Ajukan Sengketa
                  </button>
                  <button onClick={() => setDisputeFor(null)} className="px-4 py-2 rounded-lg border text-sm">Batal</button>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
