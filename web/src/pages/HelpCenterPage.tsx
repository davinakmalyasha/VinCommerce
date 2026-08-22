import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import type { HelpArticle, HelpCategory } from '../types'

export function HelpCenterPage() {
  const [params] = useSearchParams()
  const [q, setQ] = useState(params.get('q') ?? '')
  const [search, setSearch] = useState(params.get('q') ?? '')

  const { data: categories } = useQuery({
    queryKey: ['help-categories'],
    queryFn: async () => (await api.get<{ categories: HelpCategory[] }>('/help/categories')).data.categories,
  })

  const { data: articles } = useQuery({
    queryKey: ['help-articles', search],
    queryFn: async () =>
      (await api.get<{ articles: HelpArticle[] }>(`/help/articles?section=help&q=${encodeURIComponent(search)}`)).data
        .articles,
  })

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    setSearch(q)
  }

  return (
    <div className="mx-auto max-w-5xl px-4 py-10">
      <div className="text-center mb-10">
        <h1 className="text-3xl font-extrabold mb-3">Pusat Bantuan</h1>
        <p className="text-gray-500 mb-6">Temukan jawaban untuk pertanyaanmu tentang belanja, pembayaran, dan pengiriman.</p>
        <form onSubmit={submit} className="max-w-xl mx-auto flex">
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Cari bantuan..."
            className="flex-1 h-12 px-4 rounded-l-xl border border-r-0 outline-none focus:border-amber-400 text-sm"
          />
          <button className="px-6 h-12 bg-amber-500 text-white rounded-r-xl hover:bg-amber-600 text-sm font-medium">
            Cari
          </button>
        </form>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-4 gap-8">
        <aside className="md:col-span-1 space-y-2">
          <p className="font-semibold text-sm text-gray-500 mb-3">Kategori</p>
          <Link to="/help" className="block px-3 py-2 rounded-lg text-sm hover:bg-amber-50 text-gray-700">
            Semua
          </Link>
          {categories?.map((c) => (
            <button key={c.id} className="block w-full text-left px-3 py-2 rounded-lg text-sm hover:bg-amber-50 text-gray-700">
              {c.name}
            </button>
          ))}
          <div className="pt-4 space-y-2">
            <Link to="/faq" className="block px-3 py-2 rounded-lg text-sm text-amber-600 hover:bg-amber-50">
              FAQ
            </Link>
            <Link to="/contact" className="block px-3 py-2 rounded-lg text-sm text-amber-600 hover:bg-amber-50">
              Hubungi Kami
            </Link>
            <Link to="/docs" className="block px-3 py-2 rounded-lg text-sm text-amber-600 hover:bg-amber-50">
              Dokumentasi
            </Link>
          </div>
        </aside>

        <div className="md:col-span-3 space-y-3">
          {articles?.length === 0 && <p className="text-gray-500 text-sm">Tidak ada artikel yang cocok.</p>}
          {articles?.map((a) => (
            <Link
              key={a.id}
              to={`/help/${a.slug}`}
              className="block bg-white border border-gray-200 rounded-xl p-5 hover:border-amber-400 hover:shadow-md transition-all"
            >
              <div className="flex items-center gap-2 mb-1">
                {a.category && (
                  <span className="text-xs px-2 py-0.5 rounded-full bg-amber-100 text-amber-700">{a.category.name}</span>
                )}
              </div>
              <h2 className="font-semibold hover:text-amber-600">{a.title}</h2>
              <p className="text-sm text-gray-500 mt-1">{a.excerpt}</p>
            </Link>
          ))}
        </div>
      </div>
    </div>
  )
}
