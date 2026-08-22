import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import type { Product } from '../types'
import { formatIDR } from '../lib/format'

interface CompareItem extends Product {
  compared_at: number
}

function loadCompare(): CompareItem[] {
  try {
    return JSON.parse(localStorage.getItem('vc_compare') ?? '[]')
  } catch {
    return []
  }
}

export function ComparePage() {
  const [items, setItems] = useState<CompareItem[]>(loadCompare)

  const remove = (id: string) => {
    const next = items.filter((i) => i.id !== id)
    localStorage.setItem('vc_compare', JSON.stringify(next))
    setItems(next)
  }

  const allAttrs = useMemo(() => {
    const set = new Set<string>()
    items.forEach((p) => Object.keys(p.attributes ?? {}).forEach((k) => set.add(k)))
    return [...set]
  }, [items])

  if (items.length === 0) {
    return (
      <div className="mx-auto max-w-3xl px-4 py-20 text-center">
        <p className="text-gray-500 mb-4">Belum ada produk untuk dibandingkan.</p>
        <p className="text-sm text-gray-400 mb-6">
          Klik "bandingkan" pada kartu produk untuk menambah.
        </p>
        <Link to="/search" className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium">
          Cari Produk
        </Link>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-6xl px-4 py-8">
      <h1 className="text-2xl font-extrabold mb-6">Bandingkan Produk ({items.length})</h1>
      <div className="overflow-x-auto">
        <table className="w-full text-sm border-collapse">
          <thead>
            <tr>
              <th className="text-left text-gray-500 font-medium p-3 w-40">Produk</th>
              {items.map((p) => (
                <th key={p.id} className="p-3 min-w-52">
                  <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-3">
                    {p.images?.[0]?.url && (
                      <img src={p.images[0].url} alt="" className="w-full aspect-square object-cover rounded-lg bg-gray-100" />
                    )}
                    <Link to={`/product/${p.slug}`} className="font-medium text-sm hover:text-amber-600 mt-2 block line-clamp-2">
                      {p.name}
                    </Link>
                    <button onClick={() => remove(p.id)} className="text-xs text-red-500 hover:underline mt-1">
                      Hapus
                    </button>
                  </div>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <tr>
              <td className="p-3 font-medium text-gray-500">Harga</td>
              {items.map((p) => (
                <td key={p.id} className="p-3 font-bold text-amber-600">
                  {formatIDR(p.variants?.[0]?.price ?? 0)}
                </td>
              ))}
            </tr>
            <tr className="bg-gray-50 dark:bg-gray-800/50">
              <td className="p-3 font-medium text-gray-500">Rating</td>
              {items.map((p) => (
                <td key={p.id} className="p-3">
                  {'★'.repeat(Math.round(p.avg_rating))} ({p.rating_count})
                </td>
              ))}
            </tr>
            <tr>
              <td className="p-3 font-medium text-gray-500">Terjual</td>
              {items.map((p) => (
                <td key={p.id} className="p-3">{p.sold_count}</td>
              ))}
            </tr>
            {allAttrs.map((attr) => (
              <tr key={attr} className={attr ? 'bg-gray-50 dark:bg-gray-800/50' : ''}>
                <td className="p-3 font-medium text-gray-500 capitalize">{attr}</td>
                {items.map((p) => (
                  <td key={p.id} className="p-3">{p.attributes?.[attr] ?? '—'}</td>
                ))}
              </tr>
            ))}
            <tr>
              <td className="p-3 font-medium text-gray-500">Deskripsi</td>
              {items.map((p) => (
                <td key={p.id} className="p-3 text-xs text-gray-600 dark:text-gray-300 line-clamp-4">
                  {p.description || '—'}
                </td>
              ))}
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  )
}
