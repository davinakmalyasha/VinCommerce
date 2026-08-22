import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, downloadFile } from '../../lib/api'
import { formatIDR } from '../../lib/format'
import {
  FunnelPanel,
  CategoryPanel,
  CohortPanel,
  type FunnelStep,
  type CategorySales,
  type BuyerCohort,
} from '../../components/AnalyticsPanels'

export function SellerAnalytics() {
  const { data } = useQuery({
    queryKey: ['seller-analytics'],
    queryFn: async () =>
      (
        await api.get<{
          summary: { gmv: number; order_count: number; buyer_count: number; avg_order: number }
          sales_series: { day: string; gmv: number; order_count: number }[]
          top_products: { name: string; quantity: number; revenue: number }[]
          funnel: FunnelStep[]
          category_sales: CategorySales[]
          buyer_cohorts: BuyerCohort[]
        }>('/seller/analytics')
      ).data,
  })

  const [exporting, setExporting] = useState(false)
  const exportCsv = async () => {
    setExporting(true)
    try {
      await downloadFile('/seller/analytics/export.csv', 'analitik-toko.csv')
    } finally {
      setExporting(false)
    }
  }

  const maxGmv = Math.max(...(data?.sales_series ?? []).map((p) => p.gmv), 1)

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Analitik Penjualan</h1>
        <button onClick={exportCsv} disabled={exporting} className="text-sm text-amber-600 hover:underline disabled:opacity-50">
          {exporting ? 'Mengekspor...' : '⬇ Ekspor CSV'}
        </button>
      </div>

      <div className="grid grid-cols-3 gap-4">
        <div className="bg-white border border-gray-200 rounded-xl p-5">
          <p className="text-xs text-gray-500">Total Penjualan (30 hari)</p>
          <p className="text-xl font-bold text-amber-600 mt-1">{formatIDR(data?.summary.gmv ?? 0)}</p>
        </div>
        <div className="bg-white border border-gray-200 rounded-xl p-5">
          <p className="text-xs text-gray-500">Pesanan</p>
          <p className="text-xl font-bold mt-1">{data?.summary.order_count ?? 0}</p>
        </div>
        <div className="bg-white border border-gray-200 rounded-xl p-5">
          <p className="text-xs text-gray-500">Rata-rata</p>
          <p className="text-xl font-bold mt-1">{formatIDR(data?.summary.avg_order ?? 0)}</p>
        </div>
      </div>

      <div className="bg-white border border-gray-200 rounded-xl p-5">
        <h2 className="font-bold text-sm mb-4">Penjualan per Hari</h2>
        <div className="flex items-end gap-1 h-40">
          {data?.sales_series.map((p) => (
            <div key={p.day} className="flex-1 flex flex-col items-center gap-1" title={`${p.day}: ${formatIDR(p.gmv)}`}>
              <div
                className="w-full bg-amber-400 rounded-t hover:bg-amber-500 transition-all"
                style={{ height: `${Math.max((p.gmv / maxGmv) * 100, 2)}%` }}
              />
              <span className="text-[10px] text-gray-400">{p.day.slice(5)}</span>
            </div>
          ))}
        </div>
      </div>

      <FunnelPanel funnel={data?.funnel} />

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <CategoryPanel categories={data?.category_sales} />
        <CohortPanel cohorts={data?.buyer_cohorts} />
      </div>

      <div className="bg-white border border-gray-200 rounded-xl p-5">
        <h2 className="font-bold text-sm mb-3">Produk Terlaris</h2>
        <div className="space-y-2">
          {data?.top_products.map((p, i) => (
            <div key={p.name} className="flex items-center justify-between text-sm">
              <p className="line-clamp-1 flex-1">
                <span className="text-gray-400 mr-2">{i + 1}.</span>
                {p.name}
              </p>
              <p className="text-gray-500 text-xs w-20 text-right">{p.quantity} pcs</p>
              <p className="font-medium w-28 text-right">{formatIDR(p.revenue)}</p>
            </div>
          ))}
          {data?.top_products.length === 0 && <p className="text-sm text-gray-500">Belum ada penjualan.</p>}
        </div>
      </div>
    </div>
  )
}
