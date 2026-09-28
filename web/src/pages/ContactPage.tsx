import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import type { Order } from '../types'

export function ContactPage() {
  const { user } = useSession()
  const [form, setForm] = useState({ subject: '', category: 'general', priority: 'normal', order_id: '', message: '' })
  const [done, setDone] = useState<string | null>(null)

  const { data: orders } = useQuery({
    queryKey: ['orders'],
    queryFn: async () => (await api.get<{ orders: Order[] }>('/orders')).data.orders,
    enabled: !!user,
  })

  const submit = useMutation({
    mutationFn: async () =>
      api.post('/tickets', {
        subject: form.subject,
        category: form.category,
        priority: form.priority,
        order_id: form.order_id || undefined,
        message: form.message,
      }),
    onSuccess: (res) => setDone(res.data.ticket.ticket_number),
  })

  if (done) {
    return (
      <div className="mx-auto max-w-xl px-4 py-16 text-center">
        <p className="text-5xl mb-4">🎫</p>
        <h1 className="text-2xl font-bold mb-2">Tiket dibuat!</h1>
        <p className="text-gray-500 mb-6">
          Nomor tiket: <span className="font-bold text-amber-600">{done}</span>. Tim kami akan merespons dalam 1×24 jam.
        </p>
        <Link to={user ? '/account/tickets' : '/login'} className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium">
          Lihat Tiket Saya
        </Link>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-2xl px-4 py-10">
      <h1 className="text-3xl font-extrabold mb-2">Hubungi Kami</h1>
      <p className="text-gray-500 text-sm mb-8">
        {user ? 'Buat tiket dukungan, kami akan membalas lewat email dan notifikasi.' : 'Masuk dulu untuk membuat tiket dukungan.'}
      </p>

      {!user ? (
        <div className="bg-white border border-gray-200 rounded-xl p-8 text-center">
          <Link to="/login" className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium">
            Masuk untuk lanjut
          </Link>
        </div>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            submit.mutate()
          }}
          className="bg-white border border-gray-200 rounded-2xl p-6 space-y-4"
        >
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium block mb-1">Subjek</label>
              <input
                required
                value={form.subject}
                onChange={(e) => setForm({ ...form, subject: e.target.value })}
                placeholder="Ringkasan masalah"
                className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
              />
            </div>
            <div>
              <label className="text-sm font-medium block mb-1">Kategori</label>
              <select
                value={form.category}
                onChange={(e) => setForm({ ...form, category: e.target.value })}
                className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
              >
                <option value="general">Umum</option>
                <option value="order">Pesanan</option>
                <option value="shipping">Pengiriman</option>
                <option value="payment">Pembayaran</option>
                <option value="return">Retur & Refund</option>
                <option value="account">Akun</option>
              </select>
            </div>
            <div>
              <label className="text-sm font-medium block mb-1">Prioritas</label>
              <select
                value={form.priority}
                onChange={(e) => setForm({ ...form, priority: e.target.value })}
                className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
              >
                <option value="low">Rendah</option>
                <option value="normal">Normal</option>
                <option value="high">Tinggi</option>
                <option value="urgent">Sangat Tinggi</option>
              </select>
            </div>
            <div>
              <label className="text-sm font-medium block mb-1">Terkait pesanan (opsional)</label>
              <select
                value={form.order_id}
                onChange={(e) => setForm({ ...form, order_id: e.target.value })}
                className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
              >
                <option value="">Tidak ada</option>
                {orders?.map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.order_number}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <div>
            <label className="text-sm font-medium block mb-1">Pesan</label>
            <textarea
              required
              rows={5}
              value={form.message}
              onChange={(e) => setForm({ ...form, message: e.target.value })}
              placeholder="Jelaskan masalahmu secara detail..."
              className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
            />
          </div>
          {submit.isError && <p role="alert" className="text-sm text-red-600">Gagal membuat tiket. Coba lagi.</p>}
          <button
            type="submit"
            disabled={submit.isPending}
            className="w-full py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
          >
            {submit.isPending ? 'Mengirim...' : 'Kirim Tiket'}
          </button>
        </form>
      )}
    </div>
  )
}
