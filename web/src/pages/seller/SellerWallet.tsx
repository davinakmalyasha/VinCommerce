import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatIDR } from '../../lib/format'
import { Modal } from '../../components/Modal'

interface WalletData {
  wallet: { balance: number; held_balance: number }
  transactions: { id: number; kind: string; reason: string; amount: number; balance_after: number; created_at: string }[]
}

const MIN_PAYOUT = 10_000
const MAX_PAYOUT = 50_000_000

export function SellerWallet() {
  const queryClient = useQueryClient()
  const [payoutOpen, setPayoutOpen] = useState(false)
  const [amount, setAmount] = useState('')
  const [toast, setToast] = useState<{ tone: 'ok' | 'err'; text: string } | null>(null)

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

  // A prompt() is the only input for a MONEY movement and accepted anything,
  // including "abc", "-5" and "1e9". The value now comes from a validated
  // number input with explicit min/max/integer bounds.
  const payout = useMutation({
    mutationFn: async (value: number) => {
      // Withdrawals always go to the KYC-verified bank account — never to
      // buyer-entered details, which would be a fraud vector.
      if (!kyc?.bank_name || !kyc?.bank_account) {
        throw new Error('Lengkapi rekening bank pada form KYC terlebih dahulu')
      }
      await api.post('/wallet/payouts', {
        amount: value,
        bank_name: kyc.bank_name,
        bank_account: kyc.bank_account,
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['wallet'] })
      queryClient.invalidateQueries({ queryKey: ['payouts'] })
      setPayoutOpen(false)
      setAmount('')
      setToast({ tone: 'ok', text: 'Permintaan penarikan dikirim. Menunggu verifikasi admin.' })
    },
    onError: (e: Error) => setToast({ tone: 'err', text: e.message || 'Penarikan gagal' }),
  })

  const balance = data?.wallet.balance ?? 0
  const held = data?.wallet.held_balance ?? 0
  const parsed = Number(amount)
  const amountError =
    amount.trim() === ''
      ? null
      : !Number.isFinite(parsed) || !Number.isInteger(parsed)
        ? 'Masukkan angka bulat yang valid.'
        : parsed < MIN_PAYOUT
          ? `Minimal penarikan ${formatIDR(MIN_PAYOUT)}.`
          : parsed > MAX_PAYOUT
            ? `Maksimal penarikan ${formatIDR(MAX_PAYOUT)} per permintaan.`
            : parsed > balance
              ? `Saldo tidak cukup. Tersedia ${formatIDR(balance)}.`
              : null
  const canSubmit = !amountError && amount.trim() !== ''

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-bold">Dompet Penjual</h1>
      <div className="bg-white border border-gray-200 rounded-xl p-6 flex items-center justify-between flex-wrap gap-4">
        <div>
          <p className="text-xs text-gray-500">Saldo Tersedia</p>
          <p className="text-3xl font-extrabold text-amber-600">{formatIDR(balance)}</p>
          <p className="text-xs text-gray-400 mt-1">Ditahan: {formatIDR(held)}</p>
          <p className="text-xs text-gray-500 mt-2">
            {kyc?.bank_name && kyc?.bank_account
              ? `Cair ke: ${kyc.bank_name} •••• ${kyc.bank_account.slice(-4)}`
              : 'Rekening belum terdaftar — lengkapi KYC di Pengaturan.'}
          </p>
        </div>
        <button
          type="button"
          onClick={() => setPayoutOpen(true)}
          disabled={balance < MIN_PAYOUT}
          title={balance < MIN_PAYOUT ? `Minimal penarikan ${formatIDR(MIN_PAYOUT)}` : undefined}
          className="px-6 py-3 rounded-xl bg-gray-900 text-white text-sm hover:bg-gray-800 disabled:opacity-50"
        >
          Tarik Dana
        </button>
      </div>

      {toast && (
        <p
          role="status"
          className={`rounded-lg p-2.5 text-sm ${
            toast.tone === 'ok'
              ? 'bg-green-50 dark:bg-green-900/30 text-green-700'
              : 'bg-red-50 dark:bg-red-950/40 text-red-700'
          }`}
        >
          {toast.text}
          <button type="button" onClick={() => setToast(null)} className="ml-2 underline">Tutup</button>
        </p>
      )}

      <Modal
        open={payoutOpen}
        onClose={() => setPayoutOpen(false)}
        title="Tarik Dana"
        description={
          kyc?.bank_name && kyc?.bank_account
            ? `Dana dicairkan ke ${kyc.bank_name} •••• ${kyc.bank_account.slice(-4)}`
            : 'Rekening bank belum terdaftar. Lengkapi KYC di Pengaturan terlebih dahulu.'
        }
        footer={
          <>
            <button
              type="button"
              onClick={() => payout.mutate(parsed)}
              disabled={!canSubmit || payout.isPending}
              className="px-6 py-2.5 rounded-xl bg-gray-900 text-white text-sm font-medium disabled:opacity-50"
            >
              {payout.isPending ? 'Mengirim...' : 'Kirim Penarikan'}
            </button>
            <button type="button" onClick={() => setPayoutOpen(false)} className="px-4 py-2.5 rounded-xl border text-sm">
              Batal
            </button>
          </>
        }
      >
        <div className="space-y-2">
          <label htmlFor="payout-amount" className="block text-sm font-medium">Jumlah penarikan (Rp)</label>
          <input
            id="payout-amount"
            type="number"
            inputMode="numeric"
            step={1}
            min={MIN_PAYOUT}
            max={MAX_PAYOUT}
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            aria-invalid={!!amountError}
            aria-describedby={amountError ? 'payout-amount-error' : 'payout-amount-hint'}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          {amountError ? (
            <p id="payout-amount-error" role="alert" className="text-xs text-red-600">{amountError}</p>
          ) : (
            <p id="payout-amount-hint" className="text-xs text-gray-500">
              {formatIDR(MIN_PAYOUT)} – {formatIDR(MAX_PAYOUT)}. Saldo tersedia {formatIDR(balance)}.
            </p>
          )}
        </div>
      </Modal>

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
