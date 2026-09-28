import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { api } from '../../lib/api'
import { formatIDR, formatDate } from '../../lib/format'
import { Modal } from '../../components/Modal'

interface AdminPayout {
  id: string
  wallet_id: string
  user_email: string
  user_name: string
  amount: number
  status: string
  gateway_ref: string
  bank_name: string
  bank_account: string
  requested_at: string
  processed_at?: string | null
}

type Payout = AdminPayout

const STATUS_COLORS: Record<string, string> = {
  pending: 'bg-amber-100 text-amber-700',
  sent: 'bg-green-100 text-green-700',
  failed: 'bg-red-100 text-red-700',
}

export function AdminPayouts() {
  const queryClient = useQueryClient()
  const [status, setStatus] = useState('pending')
  const [refFor, setRefFor] = useState<string | null>(null)
  const [ref, setRef] = useState('')

  const { data: payouts } = useQuery({
    queryKey: ['admin-payouts', status],
    queryFn: async () => (await api.get<{ payouts: AdminPayout[] }>(`/admin/payouts?status=${status}`)).data.payouts,
    refetchInterval: 15_000,
  })

  const process = useMutation({
    mutationFn: async ({ id, action, reference }: { id: string; action: string; reference?: string }) =>
      api.post(`/admin/payouts/${id}/process`, { action, reference }),
    onSuccess: (_d, v) => {
      setRefFor(null)
      setRef('')
      queryClient.invalidateQueries({ queryKey: ['admin-payouts'] })
      if (v.action === 'failed') {
        // failed payouts refund the wallet — switch view so the operator sees it leave the queue
        setStatus('pending')
      }
    },
    onError: (e: Error) => setError(e.message || 'Gagal memproses penarikan'),
  })
  const [error, setError] = useState('')
  const [rejectFor, setRejectFor] = useState<Payout | null>(null)

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold">Proses Penarikan Dana</h1>
          <p className="text-sm text-gray-500">
            Verifikasi transfer manual ke rekening penjual. "Gagal" otomatis mengembalikan saldo.
          </p>
        </div>
        <label htmlFor="payout-status" className="sr-only">Status penarikan</label>
        <select
          id="payout-status"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          className="border rounded-lg px-3 py-2 text-sm bg-transparent"
        >
          {['pending', 'sent', 'failed'].map((s) => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>
      </div>

      <div className="space-y-3">
        {!payouts?.length && (
          <p className="text-sm text-gray-500 py-10 text-center">
            Tidak ada penarikan berstatus "{status}".
          </p>
        )}
        {payouts?.map((p) => (
          <div key={p.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4 space-y-2">
            <div className="flex items-start justify-between gap-3">
              <div>
                <p className="font-bold">{formatIDR(p.amount)}</p>
                <p className="text-sm">{p.user_name} <span className="text-gray-400">· {p.user_email}</span></p>
                <p className="text-xs text-gray-500 mt-0.5">
                  {p.bank_name} •••• {p.bank_account.slice(-4)} · diminta {formatDate(p.requested_at)}
                </p>
                {p.gateway_ref && <p className="text-xs text-gray-400 font-mono mt-0.5">ref: {p.gateway_ref}</p>}
              </div>
              <span className={`px-2 py-0.5 rounded-full text-xs ${STATUS_COLORS[p.status] ?? ''}`}>{p.status}</span>
            </div>

            {p.status === 'pending' && (
              refFor === p.id ? (
                <div className="flex gap-2 pt-1">
                  <label htmlFor={`payout-ref-${p.id}`} className="sr-only">No. referensi transfer</label>
                  <input
                    id={`payout-ref-${p.id}`}
                    value={ref}
                    onChange={(e) => setRef(e.target.value)}
                    placeholder="No. referensi transfer bank"
                    className="flex-1 px-3 py-2 border rounded-lg text-xs outline-none focus:border-amber-400"
                  />
                  <button
                    type="button"
                    onClick={() => process.mutate({ id: p.id, action: 'sent', reference: ref })}
                    disabled={process.isPending || !ref.trim()}
                    className="px-4 py-2 rounded-lg bg-green-600 text-white text-xs font-medium disabled:opacity-50"
                  >
                    Tandai Terkirim
                  </button>
                  <button type="button" onClick={() => setRefFor(null)} className="px-3 py-2 rounded-lg border border-gray-300 text-xs">
                    Batal
                  </button>
                </div>
              ) : (
                <div className="flex gap-2 pt-1">
                  <button
                    type="button"
                    onClick={() => {
                      setRefFor(p.id)
                      setRef('')
                    }}
                    className="px-4 py-2 rounded-lg bg-green-600 text-white text-xs font-medium hover:bg-green-700"
                  >
                    ✓ Transfer Terkirim
                  </button>
                  <button
                    type="button"
                    onClick={() => setRejectFor(p)}
                    disabled={process.isPending}
                    className="px-4 py-2 rounded-lg border border-red-300 text-red-600 text-xs hover:bg-red-50 disabled:opacity-50"
                  >
                    ✕ Tolak & Kembalikan Saldo
                  </button>
                </div>
              )
            )}
          </div>
        ))}
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-red-50 dark:bg-red-950/40 p-2.5 text-sm text-red-700">{error}</p>
      )}

      <Modal
        open={!!rejectFor}
        onClose={() => setRejectFor(null)}
        title="Tolak penarikan"
        footer={
          <>
            <button
              type="button"
              onClick={() => {
                if (rejectFor) process.mutate({ id: rejectFor.id, action: 'failed' })
                setRejectFor(null)
              }}
              className="px-4 py-2 rounded-lg bg-red-600 text-white text-sm"
            >
              Ya, tolak
            </button>
            <button type="button" onClick={() => setRejectFor(null)} className="px-4 py-2 rounded-lg border text-sm">
              Batal
            </button>
          </>
        }
      >
        <p className="text-sm text-gray-600 dark:text-gray-300">
          Tolak penarikan {rejectFor ? <b>{formatIDR(rejectFor.amount)}</b> : ''} dan kembalikan saldo ke
          penjual?
        </p>
      </Modal>
    </div>
  )
}
