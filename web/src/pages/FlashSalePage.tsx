import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { formatIDR } from '../lib/format'
import { useActiveFlashSale } from '../lib/flashSale'
import { Countdown } from '../components/Countdown'

export function FlashSalePage() {
  const queryClient = useQueryClient()
  const { data } = useActiveFlashSale()

  const endsAt = data?.flash_sale.ends_at ? new Date(data.flash_sale.ends_at).getTime() : 0

  const addToCart = useMutation({
    mutationFn: async (variantId: string) => api.post('/cart/items', { variant_id: variantId, quantity: 1 }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['cart'] }),
  })

  return (
    <div className="mx-auto max-w-7xl px-4 py-6">
      <div className="rounded-2xl bg-gradient-to-r from-red-500 to-orange-500 text-white p-8 mb-8 flex items-center justify-between flex-wrap gap-4">
        <div>
          <h1 className="text-3xl font-extrabold">⚡ {data?.flash_sale.name ?? 'Flash Sale'}</h1>
          <p className="text-red-100 mt-1">{data?.flash_sale.description}</p>
        </div>
        {/* Ticking is confined to <Countdown>: the page-level interval this
            replaced re-rendered every product tile once a second. */}
        {endsAt > 0 && (
          <Countdown
            to={endsAt}
            variant="blocks"
            expiredLabel={<span className="font-mono text-lg font-bold">Flash sale berakhir</span>}
          />
        )}
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-5 gap-4">
        {data?.items.map((it) => {
          const pct = it.regular_price > 0 ? Math.round((1 - it.sale_price / it.regular_price) * 100) : 0
          return (
            <div key={it.variant_id} className="bg-white dark:bg-gray-900 border border-red-100 dark:border-gray-700 rounded-xl p-3 hover:shadow-md transition-all">
              <Link to={`/product/${it.product_slug}`}>
                {it.image_url && (
                  <img src={it.image_url} alt="" className="w-full aspect-square object-cover rounded-lg bg-gray-100" />
                )}
                <p className="text-sm mt-2 line-clamp-2">{it.product_name}</p>
              </Link>
              <div className="flex items-baseline gap-2 mt-1">
                <p className="text-red-600 font-bold">{formatIDR(it.sale_price)}</p>
                <p className="text-xs text-gray-400 line-through">{formatIDR(it.regular_price)}</p>
              </div>
              {pct > 0 && <p className="text-xs text-red-500 font-semibold">-{pct}%</p>}
              <button
                type="button"
                onClick={() => addToCart.mutate(it.variant_id)}
                disabled={it.stock === 0 || addToCart.isPending}
                className="w-full mt-2 py-2 rounded-lg bg-red-500 text-white text-sm hover:bg-red-600 disabled:opacity-40"
              >
                {it.stock === 0 ? 'Habis' : 'Beli Sekarang'}
              </button>
            </div>
          )
        })}
      </div>
    </div>
  )
}
