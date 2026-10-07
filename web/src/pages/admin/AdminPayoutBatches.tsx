import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { api, downloadFile } from '../../lib/api'
import { formatIDR, formatDate } from '../../lib/format'

interface PayoutBatch {
  id: string
  batch_ref?: string
  status: string
  lag_days: number
  cutoff_at: string
  total: number
  item_count: number
  created_at: string
}

const STATUS_FILTERS = [
  'draft',
  'approved',
  'submitted',
  'paid',
  'failed',
  'cancelled',
] as const

const STATUS_COLORS: Record<string, string> = {
  draft: 'bg-slate-100 text-slate-700',
  approved: 'bg-amber-100 text-amber-700',
  submitted: 'bg-blue-100 text-blue-700',
  paid: 'bg-green-100 text-green-700',
  failed: 'bg-red-100 text-red-700',
  cancelled: 'bg-slate-100 text-slate-500 line-through',
}

const STATUS_LABELS: Record<string, string> = {
  draft: 'Draf',
  approved: 'Disetujui',
  submitted: 'Terkirim',
  paid: 'Dibayar',
  failed: 'Gagal',
  cancelled: 'Dibatalkan',
}

export function AdminPayoutBatches() {
  const queryClient = useQueryClient()
  const [status, setStatus] = useState<string>('draft')
  const [lagDays, setLagDays] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const { data: batches, isFetching } = useQuery({
    queryKey: ['admin-payout-batches', status],
    queryFn: async () =>
      (
        await api.get<{ batches: PayoutBatch[] }>(
          `/admin/payout-batches${status ? `?status=${status}` : ''}`,
        )
      ).data.batches,
    refetchInterval: 30_000,
  })

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['admin-payout-batches'] })
    queryClient.invalidateQueries({ queryKey: ['admin-payouts'] })
  }

  // `created` is reported back to the operator rather than swallowed. "I built a
  // batch" and "one was already open for this cutoff" are different outcomes and
  // the person pressing the button needs to know which happened.
  const build = useMutation({
    mutationFn: async () =>
      api.post<{ batch: PayoutBatch; added: number; created: boolean }>(
        '/admin/payout-batches/build',
        lagDays === '' ? {} : { lag_days: Number(lagDays) },
      ),
    onSuccess: ({ data }) => {
      setError(null)
      setNotice(
        data.created
          ? `Batch ${data.batch.batch_ref ?? data.batch.id} dibuat, ${data.added} pencairan masuk.`
          : `Batch ${data.batch.batch_ref ?? data.batch.id} sudah terbuka, ${data.added} pencairan ditambahkan.`,
      )
      setStatus('draft')
      invalidate()
    },
    onError: (e: Error) => setError(e.message),
  })

  const approve = useMutation({
    mutationFn: async (id: string) =>
      api.post<{ approved: boolean; items: number }>(`/admin/payout-batches/${id}/approve`),
    onSuccess: ({ data }) => {
      setError(null)
      setNotice(`Batch disetujui, ${data.items} pencairan siap dibayar.`)
      invalidate()
    },
    onError: (e: Error) => setError(e.message),
  })

  // Downloaded through downloadFile, not a bare <a href>: the endpoint is admin-only
  // and a plain link cannot send the Bearer header, so it would 401 for anyone who
  // is not also holding the refresh cookie.
  const downloadRemittance = async (id: string) => {
    setError(null)
    try {
      await downloadFile(
        `/admin/payout-batches/${id}/remittance.csv`,
        `payout-remittance-${id}.csv`,
      )
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Gagal mengunduh file remittance.')
    }
  }

  const canRemit = (s: string) => s !== 'draft' && s !== 'cancelled'

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold">Batch pencairan</h1>
          <p className="text-sm text-slate-500">
            Tumpuk pencairan yang jatuh tempo menjadi satu batch, setujui, lalu unduh
            instruksi pembayaran untuk portal bank.
          </p>
        </div>
        <div className="flex items-end gap-2">
          <label className="block">
            <span className="text-xs text-slate-500">Lag (hari)</span>
            <input
              type="number"
              min="0"
              value={lagDays}
              placeholder="sesuai pengaturan"
              onChange={(e) => setLagDays(e.target.value)}
              className="mt-1 w-44 rounded-md border border-slate-300 px-2 py-1.5 text-sm"
            />
          </label>
          <button
            onClick={() => {
              setNotice(null)
              build.mutate()
            }}
            disabled={build.isPending}
            className="rounded-md bg-slate-900 px-4 py-2 text-sm font-medium text-white hover:bg-slate-700 disabled:opacity-50"
          >
            {build.isPending ? 'Membuat…' : 'Buat batch'}
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
          {error}
        </div>
      )}
      {notice && (
        <div className="rounded-md border border-green-200 bg-green-50 px-4 py-3 text-sm text-green-700">
          {notice}
        </div>
      )}

      <div className="flex flex-wrap gap-2">
        {STATUS_FILTERS.map((s) => (
          <button
            key={s}
            onClick={() => setStatus(s)}
            className={`rounded-full px-3 py-1 text-xs font-medium transition ${
              status === s
                ? 'bg-slate-900 text-white'
                : 'bg-slate-100 text-slate-600 hover:bg-slate-200'
            }`}
          >
            {STATUS_LABELS[s]}
          </button>
        ))}
      </div>

      <div className="overflow-x-auto rounded-lg border border-slate-200">
        <table className="min-w-full text-sm">
          <thead className="bg-slate-50 text-left text-xs uppercase text-slate-500">
            <tr>
              <th className="px-4 py-3">Batch</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3 text-right">Item</th>
              <th className="px-4 py-3 text-right">Total</th>
              <th className="px-4 py-3 text-right">Lag</th>
              <th className="px-4 py-3">Cut-off</th>
              <th className="px-4 py-3">Dibuat</th>
              <th className="px-4 py-3 text-right">Aksi</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100">
            {batches?.length === 0 && (
              <tr>
                <td colSpan={8} className="px-4 py-8 text-center text-slate-500">
                  Tidak ada batch berstatus {STATUS_LABELS[status]}.
                </td>
              </tr>
            )}
            {batches?.map((b) => (
              <tr key={b.id} className="hover:bg-slate-50">
                <td className="px-4 py-3 font-mono text-xs">{b.batch_ref ?? b.id.slice(0, 8)}</td>
                <td className="px-4 py-3">
                  <span
                    className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_COLORS[b.status] ?? 'bg-slate-100 text-slate-700'}`}
                  >
                    {STATUS_LABELS[b.status] ?? b.status}
                  </span>
                </td>
                <td className="px-4 py-3 text-right tabular-nums">{b.item_count}</td>
                <td className="px-4 py-3 text-right tabular-nums font-medium">
                  {formatIDR(b.total)}
                </td>
                <td className="px-4 py-3 text-right tabular-nums">{b.lag_days}h</td>
                <td className="px-4 py-3">{formatDate(b.cutoff_at)}</td>
                <td className="px-4 py-3">{formatDate(b.created_at)}</td>
                <td className="px-4 py-3">
                  <div className="flex justify-end gap-2">
                    {b.status === 'draft' && (
                      <button
                        onClick={() => {
                          setError(null)
                          setNotice(null)
                          approve.mutate(b.id)
                        }}
                        disabled={approve.isPending}
                        className="rounded-md border border-amber-300 bg-amber-50 px-2.5 py-1 text-xs font-medium text-amber-700 hover:bg-amber-100 disabled:opacity-50"
                      >
                        {approve.isPending ? '…' : 'Setujui'}
                      </button>
                    )}
                    {canRemit(b.status) && (
                      <button
                        onClick={() => downloadRemittance(b.id)}
                        className="rounded-md border border-slate-300 px-2.5 py-1 text-xs font-medium text-slate-700 hover:bg-slate-100"
                      >
                        Remittance CSV
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <p className="text-xs text-slate-500">
        File remittance hanya tersedia setelah batch disetujui — endpoint menolak batch
        berstatus draf, sehingga pekerjaan tanpa pengawas tidak bisa menghasilkan
        instruksi pembayaran.
      </p>
      {isFetching && <p className="text-xs text-slate-400">Memuat…</p>}
    </div>
  )
}
