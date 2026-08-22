import { formatIDR } from '../lib/format'

export interface FunnelStep {
  label: string
  count: number
  rate: number
}

export interface CategorySales {
  category: string
  revenue: number
  quantity: number
}

export interface PaymentSplit {
  method: string
  revenue: number
  count: number
}

export interface BuyerCohort {
  label: string
  count: number
}

export function FunnelPanel({ funnel }: { funnel?: FunnelStep[] }) {
  if (!funnel?.length) return null
  const max = Math.max(...funnel.map((f) => f.count), 1)
  return (
    <div className="bg-white border border-gray-200 rounded-xl p-5">
      <h2 className="font-bold text-sm mb-4">Funnel Konversi (30 hari)</h2>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        {funnel.map((f, i) => (
          <div key={f.label} className="text-center">
            <div className="relative h-24 bg-gray-100 rounded-lg overflow-hidden">
              <div
                className="absolute bottom-0 left-0 right-0 bg-gradient-to-t from-amber-500 to-amber-400"
                style={{ height: `${Math.max((f.count / max) * 100, 3)}%` }}
              />
              <span className="absolute inset-0 flex items-center justify-center font-bold text-sm">
                {f.count.toLocaleString('id-ID')}
              </span>
            </div>
            <p className="text-xs text-gray-500 mt-2">{f.label}</p>
            <p className="text-[10px] text-gray-400">
              {i === 0 ? '100%' : `${f.rate.toFixed(2)}%`}
            </p>
          </div>
        ))}
      </div>
    </div>
  )
}

export function CategoryPanel({ categories }: { categories?: CategorySales[] }) {
  if (!categories?.length) return null
  const max = Math.max(...categories.map((c) => c.revenue), 1)
  return (
    <div className="bg-white border border-gray-200 rounded-xl p-5">
      <h2 className="font-bold text-sm mb-3">Penjualan per Kategori</h2>
      <div className="space-y-2">
        {categories.map((c) => (
          <div key={c.category}>
            <div className="flex items-center justify-between text-sm">
              <p className="line-clamp-1 flex-1">{c.category}</p>
              <p className="text-gray-500 text-xs w-16 text-right">{c.quantity} pcs</p>
              <p className="font-medium w-28 text-right">{formatIDR(c.revenue)}</p>
            </div>
            <div className="h-1.5 bg-gray-100 rounded-full mt-1">
              <div
                className="h-full bg-amber-400 rounded-full"
                style={{ width: `${Math.max((c.revenue / max) * 100, 2)}%` }}
              />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

export function PaymentPanel({ split }: { split?: PaymentSplit[] }) {
  if (!split?.length) return null
  const total = split.reduce((s, p) => s + p.revenue, 0)
  return (
    <div className="bg-white border border-gray-200 rounded-xl p-5">
      <h2 className="font-bold text-sm mb-3">Metode Pembayaran</h2>
      <div className="space-y-2">
        {split.map((p) => (
          <div key={p.method}>
            <div className="flex items-center justify-between text-sm">
              <p className="capitalize">{p.method.replace('_', ' ')}</p>
              <p className="font-medium">{formatIDR(p.revenue)}</p>
            </div>
            <div className="h-1.5 bg-gray-100 rounded-full mt-1">
              <div
                className="h-full bg-gray-800 rounded-full"
                style={{ width: `${Math.max((p.revenue / Math.max(total, 1)) * 100, 2)}%` }}
              />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

export function CohortPanel({ cohorts }: { cohorts?: BuyerCohort[] }) {
  if (!cohorts?.length) return null
  const total = cohorts.reduce((s, c) => s + c.count, 0) || 1
  return (
    <div className="bg-white border border-gray-200 rounded-xl p-5">
      <h2 className="font-bold text-sm mb-3">Pembeli Baru vs Berulang</h2>
      <div className="flex h-6 rounded-full overflow-hidden">
        <div className="bg-amber-500 text-white text-xs flex items-center justify-center" style={{ width: `${(cohorts.find((c) => c.label === 'new')?.count ?? 0) / total * 100}%` }}>
          Baru {((cohorts.find((c) => c.label === 'new')?.count ?? 0) / total * 100).toFixed(0)}%
        </div>
        <div className="bg-gray-700 text-white text-xs flex items-center justify-center" style={{ width: `${(cohorts.find((c) => c.label === 'returning')?.count ?? 0) / total * 100}%` }}>
          Berulang {((cohorts.find((c) => c.label === 'returning')?.count ?? 0) / total * 100).toFixed(0)}%
        </div>
      </div>
      <p className="text-xs text-gray-500 mt-2">
        {cohorts.find((c) => c.label === 'new')?.count ?? 0} pembeli baru ·{' '}
        {cohorts.find((c) => c.label === 'returning')?.count ?? 0} pembeli berulang
      </p>
    </div>
  )
}

export interface TopSeller {
  seller_id: string
  name: string
  gmv: number
  orders: number
}

export interface CouponStat {
  code: string
  type: string
  value: number
  used_count: number
}

export function TopSellersPanel({ sellers }: { sellers?: TopSeller[] }) {
  if (!sellers?.length) return null
  return (
    <div className="bg-white border border-gray-200 rounded-xl p-5">
      <h2 className="font-bold text-sm mb-3">Toko Terlaris</h2>
      <div className="space-y-2">
        {sellers.map((s, i) => (
          <div key={s.seller_id} className="flex items-center justify-between text-sm">
            <p className="line-clamp-1 flex-1">
              <span className="text-gray-400 mr-2">{i + 1}.</span>
              {s.name}
            </p>
            <p className="text-gray-500 text-xs w-16 text-right">{s.orders} pesanan</p>
            <p className="font-medium w-28 text-right">{formatIDR(s.gmv)}</p>
          </div>
        ))}
      </div>
    </div>
  )
}

export function CouponStatsPanel({ coupons }: { coupons?: CouponStat[] }) {
  if (!coupons?.length) return null
  return (
    <div className="bg-white border border-gray-200 rounded-xl p-5">
      <h2 className="font-bold text-sm mb-3">Kinerja Kupon</h2>
      <div className="space-y-2">
        {coupons.map((c) => (
          <div key={c.code} className="flex items-center justify-between text-sm">
            <p className="font-mono text-xs flex-1 truncate">{c.code}</p>
            <p className="text-gray-500 text-xs w-16 text-right">
              {c.type === 'percent' ? `${c.value}%` : formatIDR(c.value)}
            </p>
            <p className="font-medium w-20 text-right">{c.used_count}×</p>
          </div>
        ))}
      </div>
    </div>
  )
}
