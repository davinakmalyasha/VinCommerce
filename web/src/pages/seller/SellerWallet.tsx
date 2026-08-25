import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatIDR } from '../../lib/format'

interface WalletData {
  wallet: { balance: number; held_balance: number }
  transactions: { id: number; kind: string; reason: string; amount: number; balance_after: number; created_at: string }[]
}

export function SellerWallet() {
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['wallet'],
    queryFn: async () => (await api.get<WalletData>('/wallet')).data,
  })

  const { data: payouts } = useQuery({
    queryKey: ['payouts'],
    queryFn: async () => (await api.get<{ payouts: { id: string; amount: number; status: string; bank_name: string; created_at: string }[] }>('/wallet/payouts')).data.payouts,
  })

  const { data: kyc } = useQuery({
    queryKey: ['seller-kyc'],
    queryFn: async () =>
      (
        await api.get<{
          kyc: { bank_name?: string; bank_account?: string; status: string } | null
        }>('/seller/kyc')
      ).data.kyc,
  })

  const payout = useMutation({
    mutationFn: async () => {
      const amount = prompt('Jumlah penarikan (Rp):')
      if (!amount) throw new Error('dibatalkan')
      // Withdrawals always go to the KYC-verified bank account — never to
      // buyer-entered details, which would be a fraud vector.
      if (!kyc?.bank_name || !kyc?.bank_account) {
        throw new Error('Lengkapi rekening bank pada form KYC terlebih dahulu')
      }
      await api.post('/wallet/payouts', {
        amount: Number(amount),
        bank_name: kyc.bank_name,
        bank_account: kyc.bank_account,
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['wallet'] })
      queryClient.invalidateQueries({ queryKey: ['payouts'] })
    },
    onError: (e) => alert(e instanceof Error ? e.message : 'Penarikan gagal'),
  })

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-bold">Dompet Penjual</h1>
      <div className="bg-white border border-gray-200 rounded-xl p-6 flex items-center justify-between">
        <div>
          <p className="text-xs text-gray-500">Saldo Tersedia</p>
          <p className="text-3xl font-extrabold text-amber-600">{formatIDR(data?.wallet.balance ?? 0)}</p>
          <p className="text-xs text-gray-400 mt-1">Ditahan: {formatIDR(data?.wallet.held_balance ?? 0)}</p>
          <p className="text-xs text-gray-500 mt-2">
            {kyc?.bank_name && kyc?.bank_account
              ? `Cair ke: ${kyc.bank_name} •••• ${kyc.bank_account.slice(-4)}`
              : 'Rekening belum terdaftar — lengkapi KYC di Pengaturan.'}
          </p>
        </div>
        <button
          onClick={() => payout.mutate()}
          disabled={payout.isPending}
          className="px-6 py-3 rounded-xl bg-gray-900 text-white text-sm hover:bg-gray-800 disabled:opacity-50"
        >
          Tarik Dana
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <div className="bg-white border border-gray-200 rounded-xl p-5">
          <h2 className="font-bold text-sm mb-3">Transaksi Terakhir</h2>
          <div className="space-y-2">
            {data?.transactions.map((t) => (
              <div key={t.id} className="flex justify-between text-sm">
                <div>
                  <p className="capitalize">{t.reason.replace('_', ' ')}</p>
                  <p className="text-xs text-gray-400">{t.created_at.slice(0, 16).replace('T', ' ')}</p>
                </div>
                <p className={t.kind === 'credit' ? 'text-green-600 font-medium' : 'text-red-600 font-medium'}>
                  {t.kind === 'credit' ? '+' : '−'}{formatIDR(t.amount)}
                </p>
              </div>
            ))}
            {data?.transactions.length === 0 && <p className="text-sm text-gray-500">Belum ada transaksi.</p>}
          </div>
        </div>

        <div className="bg-white border border-gray-200 rounded-xl p-5">
          <h2 className="font-bold text-sm mb-3">Riwayat Penarikan</h2>
          <div className="space-y-2">
            {payouts?.map((p) => (
              <div key={p.id} className="flex justify-between text-sm">
                <div>
                  <p className="font-medium">{formatIDR(p.amount)}</p>
                  <p className="text-xs text-gray-400">{p.bank_name} · {p.created_at.slice(0, 16).replace('T', ' ')}</p>
                </div>
                <span className={`px-2 py-0.5 rounded-full text-xs ${p.status === 'sent' ? 'bg-green-100 text-green-700' : 'bg-amber-100 text-amber-700'}`}>
                  {p.status}
                </span>
              </div>
            ))}
            {payouts?.length === 0 && <p className="text-sm text-gray-500">Belum ada penarikan.</p>}
          </div>
        </div>
      </div>
    </div>
  )
}
