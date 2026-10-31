import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { formatIDR, formatDate } from '../lib/format'
import { midtransEnabled, payTopUp } from '../lib/midtrans'

interface WalletTx {
  id: number
  kind: 'credit' | 'debit'
  reason: string
  amount: number
  balance_after: number
  ref_id?: string
  created_at: string
}

const reasonLabels: Record<string, string> = {
  order_payment: 'Pembayaran pesanan',
  escrow_release: 'Hasil penjualan',
  refund: 'Refund',
  payout: 'Penarikan dana',
  commission: 'Komisi platform',
  adjustment: 'Penyesuaian',
  topup: 'Isi saldo',
}

const topUpPresets = [50_000, 100_000, 200_000, 500_000]

export function WalletPage() {
  const queryClient = useQueryClient()
  const [amount, setAmount] = useState('50000')
  const [notice, setNotice] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['wallet'],
    queryFn: async () =>
      (await api.get<{ wallet: { balance: number; held_balance: number }; transactions: WalletTx[] }>('/wallet')).data,
    // The credit is written by the gateway webhook, not by the popup. Poll while
    // the page is open so a buyer who returns from Snap sees the balance move
    // without knowing to refresh.
    refetchInterval: 15_000,
  })

  const topUp = useMutation({
    mutationFn: async (value: number) => {
      if (midtransEnabled()) {
        return payTopUp(value)
      }
      // Development/sandbox: there is no Snap client, so complete the same
      // signed webhook the real provider would send.
      const { data: created } = await api.post<{ topup: { id: string } }>('/wallet/topups', {
        amount: value,
      })
      await api.post(`/payments/sandbox/topups/${created.topup.id}/approve`)
      return 'paid' as const
    },
    onSuccess: (outcome) => {
      queryClient.invalidateQueries({ queryKey: ['wallet'] })
      setNotice(
        outcome === 'paid'
          ? 'Pembayaran diterima. Saldo bertambah setelah konfirmasi gateway.'
          : outcome === 'pending'
            ? 'Pembayaran menunggu konfirmasi. Saldo bertambah otomatis setelah lunas.'
            : 'Pembayaran belum selesai.',
      )
    },
    onError: (error: Error) => setNotice(error.message),
  })

  const submitTopUp = () => {
    const value = Number(amount)
    if (!Number.isFinite(value) || value < 10_000 || value > 20_000_000) {
      setNotice('Nominal isi saldo antara Rp 10.000 dan Rp 20.000.000.')
      return
    }
    if (!Number.isInteger(value)) {
      setNotice('Nominal harus rupiah bulat.')
      return
    }
    setNotice('')
    topUp.mutate(value)
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-8 space-y-5">
      <div>
        <Link to="/account" className="text-sm text-gray-400 hover:text-gray-700">← Akun</Link>
        <h1 className="text-xl font-bold mt-1">Dompet Saya</h1>
      </div>
      {isLoading && <p className="text-sm text-gray-500">Memuat...</p>}
      {data && (
        <>
          <div className="grid grid-cols-2 gap-4">
            <div className="bg-gradient-to-br from-amber-500 to-orange-500 rounded-2xl p-6 text-white">
              <p className="text-xs opacity-80">Saldo Tersedia</p>
              <p className="text-2xl font-extrabold mt-1">{formatIDR(data.wallet.balance)}</p>
              <p className="text-xs opacity-80 mt-2">Termasuk refund &amp; bonus</p>
            </div>
            <div className="bg-white border border-gray-200 rounded-2xl p-6">
              <p className="text-xs text-gray-400">Dana Ditahan (escrow)</p>
              <p className="text-2xl font-extrabold mt-1 text-gray-400">{formatIDR(data.wallet.held_balance)}</p>
              <p className="text-xs text-gray-400 mt-2">Lepas saat pesanan selesai</p>
            </div>
          </div>

          <div className="bg-white border border-gray-200 rounded-xl p-5">
            <h2 className="font-bold text-sm mb-1">Isi Saldo</h2>
            <p className="text-xs text-gray-500 mb-3">
              Saldo bisa dipakai untuk membayar pesanan. Minimal Rp 10.000.
            </p>
            <div className="flex flex-wrap gap-2 mb-3">
              {topUpPresets.map((preset) => (
                <button
                  key={preset}
                  type="button"
                  onClick={() => setAmount(String(preset))}
                  aria-pressed={Number(amount) === preset}
                  className={`px-3 py-1.5 rounded-lg border text-sm ${
                    Number(amount) === preset
                      ? 'border-amber-500 bg-amber-50 text-amber-700'
                      : 'border-gray-200 hover:bg-gray-50'
                  }`}
                >
                  {formatIDR(preset)}
                </button>
              ))}
            </div>
            <div className="flex flex-wrap items-end gap-2">
              <div>
                <label htmlFor="topup-amount" className="block text-xs text-gray-500 mb-1">
                  Nominal (Rp)
                </label>
                <input
                  id="topup-amount"
                  type="number"
                  min={10_000}
                  max={20_000_000}
                  step={10_000}
                  value={amount}
                  onChange={(e) => setAmount(e.target.value)}
                  className="w-40 px-3 py-2 border rounded-lg text-sm outline-none"
                />
              </div>
              <button
                type="button"
                onClick={submitTopUp}
                disabled={topUp.isPending}
                className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm font-medium disabled:opacity-50"
              >
                {topUp.isPending ? 'Memproses…' : 'Isi Saldo'}
              </button>
            </div>
            {notice && (
              <p role="status" className="text-xs text-gray-600 mt-3">
                {notice}
              </p>
            )}
          </div>

          <div className="bg-white border border-gray-200 rounded-xl p-5">
            <h2 className="font-bold text-sm mb-4">Riwayat Transaksi</h2>
            {data.transactions.length === 0 && <p className="text-sm text-gray-500">Belum ada transaksi.</p>}
            <div className="space-y-0 divide-y divide-gray-100">
              {data.transactions.map((t) => (
                <div key={t.id} className="flex items-center justify-between py-3">
                  <div>
                    <p className="text-sm font-medium">{reasonLabels[t.reason] ?? t.reason}</p>
                    <p className="text-xs text-gray-400">{formatDate(t.created_at)}</p>
                  </div>
                  <div className="text-right">
                    <p className={`text-sm font-bold ${t.kind === 'credit' ? 'text-green-600' : 'text-red-600'}`}>
                      {t.kind === 'credit' ? '+' : '−'}{formatIDR(t.amount)}
                    </p>
                    <p className="text-xs text-gray-400">Saldo: {formatIDR(t.balance_after)}</p>
                  </div>
                </div>
              ))}
            </div>
          </div>
        </>
      )}
    </div>
  )
}
