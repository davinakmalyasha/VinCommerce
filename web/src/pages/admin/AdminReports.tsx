import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../../lib/api'
import { formatDate } from '../../lib/format'

interface Report {
  id: string
  product_id: string
  product_name: string
  product_slug: string
  reporter_id: string
  reporter_name: string
  seller_id: string
  reason: string
  description: string
  status: string
  admin_note: string
  created_at: string
  resolved_at: string | null
}

const REASON_LABELS: Record<string, string> = {
  fake: 'Barang palsu',
  prohibited: 'Barang terlarang',
  copyright: 'Hak cipta',
  misleading: 'Menyesatkan',
  other: 'Lainnya',
}

export function AdminReports() {
  const queryClient = useQueryClient()
  const [filter, setFilter] = useState('open')

  const { data } = useQuery({
    queryKey: ['admin-reports', filter],
    queryFn: async () =>
      (await api.get<{ reports: Report[] }>(`/admin/reports?status=${filter}`)).data.reports,
  })

  const resolve = useMutation({
    mutationFn: async ({ id, note, takedown }: { id: string; note: string; takedown: boolean }) =>
      api.post(`/admin/reports/${id}/resolve`, { note, takedown }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-reports'] }),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Moderasi Produk</h1>
        <select
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="px-3 py-2 border rounded-lg text-sm outline-none"
        >
          <option value="open">Terbuka</option>
          <option value="resolved">Terselesaikan</option>
          <option value="all">Semua</option>
        </select>
      </div>

      {!data?.length && <p className="text-sm text-gray-500">Tidak ada laporan.</p>}

      {data?.map((r) => (
        <div key={r.id} className="bg-white border border-gray-200 rounded-xl p-5 space-y-3">
          <div className="flex items-center justify-between">
            <Link to={`/product/${r.product_slug}`} className="font-medium text-sm hover:text-amber-600 line-clamp-1">
              {r.product_name}
            </Link>
            <span
              className={`px-2.5 py-0.5 rounded-full text-xs ${
                r.status === 'open' ? 'bg-red-100 text-red-700' : 'bg-green-100 text-green-700'
              }`}
            >
              {r.status === 'open' ? 'Terbuka' : 'Selesai'}
            </span>
          </div>
          <div className="text-sm text-gray-600">
            <p>
              <span className="text-gray-400">Alasan:</span> {REASON_LABELS[r.reason] ?? r.reason}
            </p>
            {r.description && <p className="text-gray-500 text-xs mt-1">{r.description}</p>}
            <p className="text-xs text-gray-400 mt-1">
              Dilaporkan oleh {r.reporter_name} · {formatDate(r.created_at)}
            </p>
            {r.admin_note && (
              <p className="text-xs text-gray-500 mt-1">Catatan admin: {r.admin_note}</p>
            )}
          </div>
          {r.status === 'open' && (
            <ResolveRow
              onResolve={(note, takedown) => resolve.mutate({ id: r.id, note, takedown })}
              busy={resolve.isPending}
            />
          )}
        </div>
      ))}
    </div>
  )
}

function ResolveRow({ onResolve, busy }: { onResolve: (note: string, takedown: boolean) => void; busy: boolean }) {
  const [note, setNote] = useState('')
  return (
    <div className="flex flex-wrap items-center gap-2">
      <input
        value={note}
        onChange={(e) => setNote(e.target.value)}
        placeholder="Catatan admin (opsional)"
        className="flex-1 min-w-40 px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
      />
      <button type="button"
        onClick={() => onResolve(note, false)}
        disabled={busy}
        className="px-4 py-2 rounded-lg border border-gray-300 text-sm hover:bg-gray-50 disabled:opacity-50"
      >
        Tutup (tidak ada tindakan)
      </button>
      <button type="button"
        onClick={() => onResolve(note, true)}
        disabled={busy}
        className="px-4 py-2 rounded-lg bg-red-600 text-white text-sm disabled:opacity-50"
      >
        Tutup + Nonaktifkan produk
      </button>
    </div>
  )
}
