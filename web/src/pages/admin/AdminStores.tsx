import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatIDR, formatDate } from '../../lib/format'

interface PendingStore {
  id: string
  name: string
  owner_id: string
  status: string
  joined_at: string
}

interface KycSubmission {
  id: string
  store_id: string
  store_name: string
  owner_name: string
  id_number: string
  id_document_url?: string
  bank_name: string
  bank_account: string
  status: string
  created_at: string
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
    onError: (e: Error) => alert(e.message),
  })

  return (
    <div className="space-y-8">
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
                  disabled={decide.isPending}
                  className="px-4 py-2 rounded-lg bg-green-600 text-white text-sm hover:bg-green-700 disabled:opacity-50"
                >
                  Setujui
                </button>
                <button
                  onClick={() => {
                    if (confirm(`Tolak toko "${s.name}"?`)) decide.mutate({ id: s.id, decision: 'reject' })
                  }}
                  disabled={decide.isPending}
                  className="px-4 py-2 rounded-lg border border-red-300 text-red-600 text-sm hover:bg-red-50 disabled:opacity-50"
                >
                  Tolak
                </button>
              </div>
            </div>
          ))}
        </div>
      </div>
      <KYCQueue />
    </div>
  )
}

function KYCQueue() {
  const queryClient = useQueryClient()
  const [noteFor, setNoteFor] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const [pendingDecision, setPendingDecision] = useState<'approve' | 'reject'>('approve')

  const { data } = useQuery({
    queryKey: ['admin-kyc'],
    queryFn: async () => (await api.get<{ kycs: KycSubmission[] }>('/admin/kyc')).data.kycs,
  })

  const decideKyc = useMutation({
    mutationFn: async ({ storeId, decision, note }: { storeId: string; decision: string; note?: string }) =>
      api.post(`/admin/stores/${storeId}/kyc/decide`, { decision, note }),
    onSuccess: () => {
      setNoteFor(null)
      setNote('')
      queryClient.invalidateQueries({ queryKey: ['admin-kyc'] })
    },
    onError: (e: Error) => alert(e.message),
  })

  const submitDecision = (storeId: string) => {
    if (pendingDecision === 'reject' && !note.trim()) return
    decideKyc.mutate({ storeId, decision: pendingDecision, note: note || undefined })
  }

  return (
    <div className="space-y-4">
      <h2 className="text-lg font-bold">Antrean Verifikasi KYC ({data?.length ?? 0})</h2>
      <p className="text-xs text-gray-500 -mt-2">
        Seller tidak bisa menarik dana sebelum KYC disetujui.
      </p>
      <div className="space-y-3">
        {data?.length === 0 && <p className="text-gray-500 text-sm">Tidak ada pengajuan KYC menunggu review.</p>}
        {data?.map((k) => (
          <div key={k.id} className="bg-white border border-gray-200 rounded-xl p-5 space-y-3">
            <div className="flex items-start justify-between">
              <div>
                <p className="font-medium">{k.store_name}</p>
                <p className="text-xs text-gray-500">
                  {k.owner_name} · NIK {k.id_number.slice(0, 4)}••••{k.id_number.slice(-4)} · Diajukan{' '}
                  {formatDate(k.created_at)}
                </p>
              </div>
              <span className="px-2.5 py-0.5 rounded-full bg-amber-100 text-amber-700 text-xs">pending</span>
            </div>
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 text-xs bg-gray-50 rounded-lg p-3">
              <div>
                <p className="text-gray-400">Bank</p>
                <p className="font-medium">{k.bank_name || '—'}</p>
              </div>
              <div>
                <p className="text-gray-400">No. Rekening</p>
                <p className="font-mono">{k.bank_account || '—'}</p>
              </div>
              <div>
                <p className="text-gray-400">Dokumen Identitas</p>
                {k.id_document_url ? (
                  <a href={k.id_document_url} target="_blank" rel="noreferrer" className="text-blue-600 hover:underline">
                    Lihat dokumen ↗
                  </a>
                ) : (
                  <p>—</p>
                )}
              </div>
            </div>
            {noteFor === k.id ? (
              <div className="space-y-2">
                <input
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  placeholder={pendingDecision === 'reject' ? 'Alasan penolakan (wajib)' : 'Catatan (opsional)'}
                  className="w-full px-3 py-2 border rounded-lg text-sm outline-none"
                />
                <div className="flex gap-2">
                  <button
                    onClick={() => submitDecision(k.store_id)}
                    disabled={decideKyc.isPending || (pendingDecision === 'reject' && !note.trim())}
                    className={`px-4 py-2 rounded-lg text-white text-sm disabled:opacity-50 ${
                      pendingDecision === 'approve' ? 'bg-green-600 hover:bg-green-700' : 'bg-red-600 hover:bg-red-700'
                    }`}
                  >
                    {decideKyc.isPending ? 'Memproses...' : pendingDecision === 'approve' ? 'Konfirmasi Setujui' : 'Konfirmasi Tolak'}
                  </button>
                  <button onClick={() => setNoteFor(null)} className="px-4 py-2 rounded-lg border text-sm">
                    Batal
                  </button>
                </div>
              </div>
            ) : (
              <div className="flex gap-2">
                <button
                  onClick={() => {
                    setPendingDecision('approve')
                    setNoteFor(k.id)
                    setNote('')
                  }}
                  className="px-4 py-2 rounded-lg bg-green-600 text-white text-sm hover:bg-green-700"
                >
                  Setujui KYC
                </button>
                <button
                  onClick={() => {
                    setPendingDecision('reject')
                    setNoteFor(k.id)
                    setNote('')
                  }}
                  className="px-4 py-2 rounded-lg border border-red-300 text-red-600 text-sm hover:bg-red-50"
                >
                  Tolak KYC
                </button>
              </div>
            )}
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

  const { data: counts } = useQuery({
    queryKey: ['pending-counts'],
    queryFn: async () =>
      (await api.get<{ stores: number; kyc: number; reviews: number; returns: number; disputes: number; tickets: number }>('/admin/pending-counts')).data,
    refetchInterval: 30_000,
  })

  const cards = [
    { label: 'GMV (30 hari)', value: formatIDR(data?.summary.gmv ?? 0) },
    { label: 'Pesanan', value: String(data?.summary.order_count ?? 0) },
    { label: 'Pembeli', value: String(data?.summary.buyer_count ?? 0) },
    { label: 'Produk Aktif', value: String(data?.summary.product_count ?? 0) },
    { label: 'Toko Aktif', value: String(data?.summary.store_count ?? 0) },
    { label: 'Rata-rata Pesanan', value: formatIDR(data?.summary.avg_order ?? 0) },
  ]

  const queues = [
    { to: '/admin/stores', label: 'Toko menunggu', count: counts?.stores, warn: (counts?.stores ?? 0) > 5 },
    { to: '/admin/stores', label: 'KYC pending', count: counts?.kyc, warn: (counts?.kyc ?? 0) > 0 },
    { to: '/admin/reviews', label: 'Ulasan dimoderasi', count: counts?.reviews, warn: (counts?.reviews ?? 0) > 10 },
    { to: '/admin/returns', label: 'Retur diajukan', count: counts?.returns, warn: false },
    { to: '/admin/disputes', label: 'Sengketa terbuka', count: counts?.disputes, warn: (counts?.disputes ?? 0) > 0 },
    { to: '/admin/tickets', label: 'Tiket aktif', count: counts?.tickets, warn: false },
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

      <div className="space-y-3">
        <h2 className="font-bold text-sm text-gray-500 uppercase tracking-wide">Antrean Operasional</h2>
        <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3">
          {queues.map((q) => {
            const n = q.count ?? 0
            const loading = q.count === undefined
            return (
              <QueueTile key={q.label} to={q.to} label={q.label} count={n} loading={loading} warn={q.warn} />
            )
          })}
        </div>
      </div>
    </div>
  )
}

function QueueTile({ to, label, count, loading, warn }: { to: string; label: string; count: number; loading: boolean; warn: boolean }) {
  return (
    <Link to={to} className={`block bg-white border rounded-xl p-4 transition-colors ${
      warn && !loading ? 'border-red-300 bg-red-50' : 'border-gray-200 hover:border-amber-400'
    }`}>
      <p className="text-xs text-gray-500">{label}</p>
      <p className={`text-2xl font-extrabold mt-1 ${loading ? 'text-gray-300' : warn ? 'text-red-600' : 'text-gray-900'}`}>
        {loading ? '…' : count}
      </p>
    </Link>
  )
}
