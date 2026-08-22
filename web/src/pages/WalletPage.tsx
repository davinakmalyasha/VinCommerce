import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { formatIDR, formatDate } from '../lib/format'

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
}

export function WalletPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['wallet'],
    queryFn: async () =>
      (await api.get<{ wallet: { balance: number; held_balance: number }; transactions: WalletTx[] }>('/wallet')).data,
  })

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
