import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, NavLink, Outlet } from 'react-router-dom'
import { api } from '../../lib/api'
import { useSession } from '../../stores/session'
import { formatIDR } from '../../lib/format'
import { Modal } from '../../components/Modal'

export function SellerLayout() {
  const { user } = useSession()
  const [openOnboard, setOpenOnboard] = useState(false)
  const [storeName, setStoreName] = useState('')
  const [error, setError] = useState('')

  const { data: store } = useQuery({
    queryKey: ['my-store'],
    queryFn: async () => {
      try {
        return (await api.get<{ store: { id: string; name: string; status: string } }>('/seller/store')).data.store
      } catch {
        return null
      }
    },
    enabled: !!user,
  })

  // A prompt() cannot be styled, labelled or validated, and its text is lost
  // on failure. Creating a store now goes through a real form.
  const createStore = useMutation({
    mutationFn: async (name: string) => {
      await api.post('/seller/store', { name })
    },
    onSuccess: () => {
      // Re-mints the access token so the fresh seller role is in the claims.
      window.location.reload()
    },
    onError: (e: Error) => setError(e.message || 'Gagal membuat toko.'),
  })

  // Access model:
  //  - logged-out → sign-in notice
  //  - buyer WITHOUT a store → onboarding funnel ("Buka Toko") — this is the
  //    advertised buyer→seller path and must stay reachable (the backend
  //    grants the seller role on POST /seller/store, then the page reloads).
  //  - buyer WITH a store but role grant still propagating → onboarding too
  //    (a full reload re-mints claims from the DB via /auth/refresh).
  //  - seller/admin with no store yet → onboarding as before.
  const isSellerRole = !!user && (user.roles.includes('seller') || user.roles.includes('admin'))

  if (!user) {
    return (
      <div className="mx-auto max-w-md px-4 py-20 text-center">
        <h1 className="text-xl font-bold mb-2">Masuk Diperlukan</h1>
        <p className="text-gray-500 text-sm">Masuk untuk membuka atau mengelola toko.</p>
      </div>
    )
  }

  const onboarding = (
    <div className="mx-auto max-w-xl px-4 py-20 text-center">
      <h1 className="text-2xl font-bold mb-2">Mulai Jualan di VinCommerce</h1>
      <p className="text-gray-500 text-sm mb-8">
        Buka toko gratis, jangkau jutaan pembeli, terima pembayaran escrow yang aman.
      </p>
      {!isSellerRole && store === null && (
        <p className="text-xs text-gray-400 mb-4">
          Setelah toko dibuat, akunmu otomatis mendapat peran penjual.
        </p>
      )}
      <button
        type="button"
        onClick={() => {
          setError('')
          setOpenOnboard(true)
        }}
        disabled={createStore.isPending}
        className="px-8 py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
      >
        {createStore.isPending ? 'Membuat...' : 'Buka Toko Sekarang'}
      </button>

      <Modal
        open={openOnboard}
        onClose={() => setOpenOnboard(false)}
        title="Buka Toko"
        description="Pilih nama toko yang akan tampil di halaman produkmu."
        footer={
          <>
            <button
              type="button"
              onClick={() => createStore.mutate(storeName.trim())}
              disabled={!storeName.trim() || createStore.isPending}
              className="px-6 py-2.5 rounded-xl bg-amber-500 text-white text-sm font-semibold disabled:opacity-50"
            >
              {createStore.isPending ? 'Membuat...' : 'Buat Toko'}
            </button>
            <button
              type="button"
              onClick={() => setOpenOnboard(false)}
              className="px-4 py-2.5 rounded-xl border text-sm"
            >
              Batal
            </button>
          </>
        }
      >
        <div className="space-y-2">
          <label htmlFor="store-name" className="block text-sm font-medium">Nama toko</label>
          <input
            id="store-name"
            value={storeName}
            onChange={(e) => setStoreName(e.target.value)}
            placeholder="mis. Toko Berkah"
            minLength={3}
            maxLength={60}
            required
            aria-invalid={!!error}
            aria-describedby={error ? 'store-name-error' : undefined}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          {error && (
            <p id="store-name-error" role="alert" className="text-sm text-red-600">{error}</p>
          )}
        </div>
      </Modal>
    </div>
  )


  if (!store) return onboarding
  if (!isSellerRole) {
    // Store exists but claims not refreshed yet — force the same reload flow.
    return onboarding
  }

  const nav = [
    { to: '/seller', label: 'Dashboard', end: true },
    { to: '/seller/products', label: 'Produk' },
    { to: '/seller/orders', label: 'Pesanan' },
    { to: '/seller/returns', label: 'Retur' },
    { to: '/seller/wallet', label: 'Dompet' },
    { to: '/seller/analytics', label: 'Analitik' },
    { to: '/seller/coupons', label: 'Kupon Toko' },
{ to: '/seller/live', label: '📺 Live Studio' },
    { to: '/seller/settings', label: 'Pengaturan' },
  ]

  return (
    <div className="mx-auto max-w-7xl px-4 py-6 grid grid-cols-1 md:grid-cols-6 gap-6">
      <aside className="md:col-span-1 space-y-1">
        <div className="bg-white border border-gray-200 rounded-xl p-4 mb-3">
          <p className="font-bold text-sm">{store.name}</p>
          <p className="text-xs text-gray-500">{store.status}</p>
        </div>
        {nav.map((n) => (
          <NavLink
            key={n.to}
            to={n.to}
            end={n.end}
            className={({ isActive }) =>
              `block px-4 py-2.5 rounded-lg text-sm ${
                isActive ? 'bg-amber-500 text-white font-medium' : 'hover:bg-gray-100 text-gray-700'
              }`
            }
          >
            {n.label}
          </NavLink>
        ))}
        <Link to="/" className="block px-4 py-2.5 rounded-lg text-sm text-gray-400 hover:bg-gray-100">
          ← Kembali ke toko
        </Link>
      </aside>
      <main className="md:col-span-5">
        <Outlet />
      </main>
    </div>
  )
}

export function SellerHome() {
  const { data } = useQuery({
    queryKey: ['seller-dashboard'],
    queryFn: async () =>
      (
        await api.get<{
          total_sales: number
          order_count: number
          pending_orders: number
          products_active: number
          products_draft: number
          wallet_balance: number
          avg_order_value: number
        }>('/seller/dashboard')
      ).data,
  })

  const { data: lowStock } = useQuery({
    queryKey: ['seller-low-stock'],
    queryFn: async () =>
      (
        await api.get<{
          variants: { variant_id: string; product_name: string; variant_name: string; stock: number }[]
        }>('/seller/low-stock')
      ).data,
  })

  const { data: qa } = useQuery({
    queryKey: ['seller-questions'],
    queryFn: async () =>
      (
        await api.get<{
          questions: { id: string; question: string; answer?: string; product_name?: string; ask_user_name?: string }[]
        }>('/seller/questions')
      ).data,
  })

  const cards = [
    { label: 'Total Penjualan', value: formatIDR(data?.total_sales ?? 0), color: 'text-amber-600' },
    { label: 'Pesanan', value: String(data?.order_count ?? 0), color: 'text-gray-900' },
    { label: 'Menunggu Proses', value: String(data?.pending_orders ?? 0), color: 'text-blue-600' },
    { label: 'Produk Aktif', value: String(data?.products_active ?? 0), color: 'text-green-600' },
    { label: 'Saldo Dompet', value: formatIDR(data?.wallet_balance ?? 0), color: 'text-amber-600' },
    { label: 'Rata-rata Pesanan', value: formatIDR(data?.avg_order_value ?? 0), color: 'text-gray-900' },
  ]

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-bold">Ringkasan Toko</h1>
      <div className="grid grid-cols-2 lg:grid-cols-3 gap-4">
        {cards.map((c) => (
          <div key={c.label} className="bg-white border border-gray-200 rounded-xl p-5">
            <p className="text-xs text-gray-500">{c.label}</p>
            <p className={`text-xl font-bold mt-1 ${c.color}`}>{c.value}</p>
          </div>
        ))}
      </div>

      {lowStock && lowStock.variants.length > 0 && (
        <div className="bg-red-50 dark:bg-red-950/40 border border-red-200 dark:border-red-800 rounded-xl p-5">
          <h2 className="font-bold text-sm text-red-700 dark:text-red-300 mb-3">⚠️ Stok Menipis ({lowStock.variants.length})</h2>
          <div className="space-y-2">
            {lowStock.variants.map((v) => (
              <div key={v.variant_id} className="flex items-center justify-between text-sm">
                <p className="text-red-800 dark:text-red-200 line-clamp-1">
                  {v.product_name} <span className="text-red-400">({v.variant_name})</span>
                </p>
                <p className="font-bold text-red-600">sisa {v.stock}</p>
              </div>
            ))}
          </div>
          <Link
            to="/seller/products"
            className="inline-block mt-3 text-xs text-red-600 dark:text-red-300 underline"
          >
            Kelola stok →
          </Link>
        </div>
      )}

      {qa && qa.questions.length > 0 && (
        <SellerQAInbox questions={qa.questions} />
      )}
    </div>
  )
}

const unanswered = (q: { answer?: string }) => !q.answer

function SellerQAInbox({
  questions,
}: {
  questions: { id: string; question: string; answer?: string; product_name?: string; ask_user_name?: string }[]
}) {
  const queryClient = useQueryClient()
  const [answerFor, setAnswerFor] = useState<string | null>(null)
  const [text, setText] = useState('')

  const answer = useMutation({
    mutationFn: async ({ id, body }: { id: string; body: string }) =>
      api.post(`/qa/${id}/answer`, { answer: body }),
    onSuccess: () => {
      setAnswerFor(null)
      setText('')
      queryClient.invalidateQueries({ queryKey: ['seller-questions'] })
    },
    onError: (e: Error) => setError(e.message || 'Gagal mengirim jawaban'),
  })
  const [error, setError] = useState('')

  const pending = questions.filter(unanswered)

  return (
    <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5">
      <h2 className="font-bold text-sm mb-3">
        ❓ Tanya Jawab Produk {pending.length > 0 && <span className="text-amber-600">({pending.length} belum dijawab)</span>}
      </h2>
      {error && (
        <p role="alert" className="text-sm text-red-600 mb-2 bg-red-50 dark:bg-red-950/40 rounded-lg p-2.5">{error}</p>
      )}
      <div className="space-y-3">
        {questions.slice(0, 8).map((q) => (
          <div key={q.id} className="border-b border-gray-100 dark:border-gray-800 pb-3 last:border-0 last:pb-0">
            <p className="text-sm">
              <span className="font-medium">{q.product_name ?? 'Produk'}</span>
              <span className="text-gray-400 text-xs"> · {q.ask_user_name ?? 'pembeli'}</span>
            </p>
            <p className="text-sm text-gray-600 dark:text-gray-300 mt-0.5">{q.question}</p>
            {q.answer ? (
              <p className="text-xs text-green-700 dark:text-green-400 mt-1">✓ {q.answer}</p>
            ) : answerFor === q.id ? (
              <div className="mt-2 flex gap-2">
                <label htmlFor={`qa-${q.id}`} className="sr-only">Jawaban untuk {q.product_name ?? 'produk'}</label>
                <input
                  id={`qa-${q.id}`}
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  placeholder="Tulis jawaban..."
                  className="flex-1 min-w-0 px-3 py-2 border rounded-lg text-sm outline-none focus:border-amber-400"
                />
                <button
                  type="button"
                  onClick={() => {
                    setError('')
                    if (text.trim()) answer.mutate({ id: q.id, body: text.trim() })
                  }}
                  disabled={answer.isPending || !text.trim()}
                  className="px-4 py-2 rounded-lg bg-amber-500 text-white text-xs font-medium disabled:opacity-50"
                >
                  Kirim
                </button>
              </div>
            ) : (
              <button
                type="button"
                onClick={() => {
                  setAnswerFor(q.id)
                  setText('')
                }}
                className="mt-1.5 px-3 py-1.5 rounded-lg border border-amber-400 text-amber-600 text-xs hover:bg-amber-50"
              >
                Jawab
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

