import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { api, downloadFile } from '../../lib/api'
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

interface ReturnParcel {
  id: string
  sequence: number
  status: string
  carrier: string
  tracking_number?: string
  label_url?: string
}

/**
 * Labels are whatever the carrier handed back -- an absolute signed link for one
 * carrier, a same-origin path for another. `downloadFile` is authenticated but
 * goes through the API client, so an absolute URL would be prefixed with the
 * API base and 404. Split on the scheme instead of guessing.
 */
function openLabel(url: string) {
  if (/^https?:\/\//i.test(url)) {
    window.open(url, '_blank', 'noopener,noreferrer')
    return
  }
  void downloadFile(url, 'label-retur.pdf')
}

export function SellerReturns() {
  const queryClient = useQueryClient()
  const [error, setError] = useState('')
  const [expanded, setExpanded] = useState<string | null>(null)
  const [confirmLabel, setConfirmLabel] = useState<string | null>(null)

  const { data } = useQuery({
    queryKey: ['seller-returns'],
    queryFn: async () => (await api.get<{ returns: ReturnItem[] }>('/seller/returns')).data.returns,
    refetchInterval: 30_000,
  })

  const decide = useMutation({
    mutationFn: async ({ id, decision, note }: { id: string; decision: string; note?: string }) =>
      api.post(`/seller/returns/${id}/decide`, { decision, note }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['seller-returns'] }),
    onError: (e: Error) => setError(e.message),
  })

  // Fetched only for the one row the seller opened. Pulling the parcel for every
  // return in the list would be a request per row, most of which nobody looks at.
  const { data: parcelData, refetch: refetchParcel, isFetching: fetchingParcel } = useQuery({
    queryKey: ['seller-return-parcel', expanded],
    queryFn: async () =>
      (await api.get<{ parcel: ReturnParcel | null }>(`/seller/returns/${expanded}/parcel`)).data.parcel,
    enabled: expanded !== null,
  })

  // Buys a label, which SPENDS MONEY and is often irreversible at the carrier.
  // Separate button, separate confirmation step -- never folded into "approve".
  const buyLabel = useMutation({
    mutationFn: async ({ id, format }: { id: string; format: string }) =>
      api.post<{ parcel: ReturnParcel; label_url: string }>(`/seller/returns/${id}/return-label`, { format }),
    onSuccess: async ({ data }) => {
      setConfirmLabel(null)
      if (data.label_url) openLabel(data.label_url)
      await queryClient.invalidateQueries({ queryKey: ['seller-return-parcel'] })
    },
    onError: (e: Error) => {
      setConfirmLabel(null)
      setError(e.message)
    },
  })

  // Records delivery of the return parcel. It moves the return's status and nothing
  // else -- no escrow release, no wallet debit. The refund is a separate action.
  const noteArrived = useMutation({
    mutationFn: async ({ id }: { id: string }) =>
      api.post(`/seller/returns/${id}/arrived`, {}),
    onSuccess: async () => {
      setError('')
      await queryClient.invalidateQueries({ queryKey: ['seller-returns'] })
      await refetchParcel()
    },
    onError: (e: Error) => setError(e.message),
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
      {error && (
        <p role="alert" className="rounded-lg bg-red-50 p-2.5 text-sm text-red-700">
          {error}
          <button type="button" onClick={() => setError('')} className="ml-2 underline">Tutup</button>
        </p>
      )}
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
              <div className="flex gap-2 ml-4">
                {r.status === 'requested' && (
                  <>
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
                  </>
                )}
                {r.status === 'approved' && (
                  <button type="button"
                    onClick={() => setExpanded(expanded === r.id ? null : r.id)}
                    className="px-4 py-2 rounded-lg border border-gray-300 text-sm text-gray-700 hover:bg-gray-50"
                  >
                    {expanded === r.id ? 'Tutup' : 'Lacak retur'}
                  </button>
                )}
              </div>
            </div>

            {expanded === r.id && r.status === 'approved' && (
              <div className="border-t mt-4 pt-4 space-y-3">
                {fetchingParcel && <p className="text-xs text-gray-500">Memuat parcel retur…</p>}

                {!fetchingParcel && !parcelData && (
                  <div className="space-y-2">
                    <p className="text-sm text-gray-600">
                      Belum ada parcel retur. Terbitkan label agar pembeli bisa mengirim
                      barang kembali — ini berbayar dan tidak selalu bisa dibatalkan.
                    </p>
                    {confirmLabel === r.id ? (
                      <div className="flex items-center gap-2">
                        <span className="text-xs text-red-600">
                          Lanjutkan membeli label? Biaya tidak dapat dikembalikan.
                        </span>
                        <button type="button"
                          onClick={() => buyLabel.mutate({ id: r.id, format: 'pdf' })}
                          disabled={buyLabel.isPending}
                          className="px-3 py-1.5 rounded-lg bg-red-600 text-white text-xs disabled:opacity-50"
                        >
                          {buyLabel.isPending ? 'Membeli…' : 'Ya, beli label'}
                        </button>
                        <button type="button" onClick={() => setConfirmLabel(null)} className="text-xs text-gray-500">
                          Batal
                        </button>
                      </div>
                    ) : (
                      <button type="button"
                        onClick={() => setConfirmLabel(r.id)}
                        className="px-4 py-2 rounded-lg bg-indigo-600 text-white text-sm hover:bg-indigo-700"
                      >
                        Terbitkan label retur
                      </button>
                    )}
                  </div>
                )}

                {parcelData && (
                  <div className="rounded-lg bg-gray-50 p-3 text-sm space-y-2">
                    <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
                      <span className="text-xs text-gray-500">#{parcelData.sequence}</span>
                      <span className="capitalize">{parcelData.status}</span>
                      <span className="text-xs text-gray-500">{parcelData.carrier}</span>
                      {parcelData.tracking_number && (
                        <span className="font-mono text-xs">{parcelData.tracking_number}</span>
                      )}
                    </div>
                    <div className="flex gap-2">
                      {parcelData.label_url && (
                        <button type="button"
                          onClick={() => openLabel(parcelData.label_url!)}
                          className="text-xs text-indigo-600 hover:underline"
                        >
                          Unduh label
                        </button>
                      )}
                      {/* Records delivery only. The refund stays a separate action --
                          this endpoint moves no money and must not appear to. */}
                      {!['delivered', 'received'].includes(parcelData.status) && (
                        <button type="button"
                          onClick={() => noteArrived.mutate({ id: r.id })}
                          disabled={noteArrived.isPending}
                          className="text-xs text-emerald-700 hover:underline disabled:opacity-50"
                        >
                          {noteArrived.isPending ? 'Menyimpan…' : 'Tandai barang tiba'}
                        </button>
                      )}
                      <button type="button" onClick={() => refetchParcel()} className="text-xs text-gray-500 hover:underline">
                        Muat ulang
                      </button>
                    </div>
                    <p className="text-xs text-gray-500">
                      Menandai tiba hanya mencatat penerimaan barang. Refund tetap
                      diproses terpisah.
                    </p>
                  </div>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
