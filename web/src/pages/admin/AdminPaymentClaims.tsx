import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatDate, formatIDR } from '../../lib/format'

/**
 * Off-platform payment claims awaiting verification.
 *
 * A buyer paying outside the app files a CLAIM: a reference and an amount. It is not
 * money. The order stays `pending` until someone here approves it, and approving is
 * the only route from a claim to a paid order -- which is what makes "the buyer says
 * they transferred" different from "the money arrived".
 *
 * Rejection leaves the order pending, so the buyer can file corrected evidence.
 */
interface Claim {
  id: string
  order_id: string
  order_number: string
  buyer_email: string
  reference: string
  amount: number
  order_total: number
  paid_at: string
  created_at: string
}

export function AdminPaymentClaims() {
  const queryClient = useQueryClient()

  const { data = [], isLoading } = useQuery({
    queryKey: ['admin-payment-claims'],
    queryFn: async () =>
      (await api.get<{ claims: Claim[] }>('/admin/external-payments')).data.claims,
  })

  const decide = useMutation({
    mutationFn: async ({ id, decision }: { id: string; decision: 'approved' | 'rejected' }) =>
      api.post(`/admin/external-payments/${id}/decide`, { decision }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-payment-claims'] }),
  })

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-xl font-bold">Verifikasi Pembayaran Manual</h1>
        <p className="text-sm text-gray-500 mt-1">
          Klaim transfer dari pembeli. Pesanan tetap <em>pending</em> sampai disetujui di sini.
        </p>
      </div>

      {isLoading ? (
        <p className="text-sm text-gray-500">Memuat klaim…</p>
      ) : data.length === 0 ? (
        <p className="text-sm text-gray-500">Tidak ada klaim yang menunggu verifikasi.</p>
      ) : (
        <div className="bg-white border border-gray-200 rounded-xl overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500">
              <tr>
                <th className="px-4 py-3">Pesanan</th>
                <th className="px-4 py-3">Pembeli</th>
                <th className="px-4 py-3">Referensi</th>
                <th className="px-4 py-3">Nominal klaim</th>
                <th className="px-4 py-3">Total pesanan</th>
                <th className="px-4 py-3">Dibayar</th>
                <th className="px-4 py-3">Aksi</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {data.map((c) => (
                <tr key={c.id}>
                  <td className="px-4 py-3 font-medium">{c.order_number}</td>
                  <td className="px-4 py-3 text-gray-600">{c.buyer_email}</td>
                  <td className="px-4 py-3 font-mono text-xs">{c.reference}</td>
                  <td className="px-4 py-3">{formatIDR(c.amount)}</td>
                  <td className="px-4 py-3">
                    {formatIDR(c.order_total)}
                    {c.amount !== c.order_total && (
                      <span className="ml-2 text-xs text-red-600">tidak cocok</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-gray-500">{formatDate(c.paid_at)}</td>
                  <td className="px-4 py-3">
                    <div className="flex gap-2">
                      <button
                        type="button"
                        onClick={() => decide.mutate({ id: c.id, decision: 'approved' })}
                        disabled={decide.isPending}
                        className="px-3 py-1.5 rounded-lg bg-green-600 text-white text-xs font-medium disabled:opacity-50"
                      >
                        Setujui
                      </button>
                      <button
                        type="button"
                        onClick={() => decide.mutate({ id: c.id, decision: 'rejected' })}
                        disabled={decide.isPending}
                        className="px-3 py-1.5 rounded-lg bg-gray-900 text-white text-xs font-medium disabled:opacity-50"
                      >
                        Tolak
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
