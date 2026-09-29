import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import { API_DOCS_BASE } from '../lib/docs'
import type { HelpArticle } from '../types'

export function DocsPage() {
  const { data } = useQuery({
    queryKey: ['docs-articles'],
    queryFn: async () => (await api.get<{ articles: HelpArticle[] }>('/help/articles?section=docs')).data.articles,
  })

  return (
    <div className="mx-auto max-w-5xl px-4 py-10">
      <h1 className="text-3xl font-extrabold mb-2">Dokumentasi Platform</h1>
      <p className="text-gray-500 text-sm mb-8">
        Panduan penggunaan VinCommerce — cara kerja escrow, panduan penjual, dan referensi API.
      </p>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-10">
        <a
          // /api-docs, not /docs. Swagger moved there because the SPA owns
          // /docs, and a plain <a> is used rather than <Link> because this
          // points at the API's own server, not at an SPA route: the SPA router
          // would intercept a react-router <Link> and render the docs page
          // instead of the specification.
          href={`${API_DOCS_BASE}/openapi.json`}
          className="block bg-gray-900 text-white rounded-xl p-6 hover:bg-gray-800 transition-colors"
        >
          <p className="text-lg font-bold">OpenAPI Specification</p>
          <p className="text-sm text-gray-400 mt-1">Referensi API lengkap format OpenAPI 3.0</p>
        </a>
        <Link to="/docs/api" className="block bg-amber-500 text-white rounded-xl p-6 hover:bg-amber-600 transition-colors">
          <p className="text-lg font-bold">API Quickstart</p>
          <p className="text-sm text-amber-100 mt-1">Auth, katalog, checkout — mulai dalam 5 menit</p>
        </Link>
      </div>

      <h2 className="font-bold mb-4">Panduan</h2>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {data?.map((a) => (
          <Link
            key={a.id}
            to={`/help/${a.slug}`}
            className="bg-white border border-gray-200 rounded-xl p-5 hover:border-amber-400 hover:shadow-md transition-all"
          >
            <h3 className="font-semibold">{a.title}</h3>
            <p className="text-sm text-gray-500 mt-1">{a.excerpt}</p>
          </Link>
        ))}
        {data?.length === 0 && <p className="text-gray-500 text-sm">Dokumen belum tersedia.</p>}
      </div>

      <div className="mt-10 grid grid-cols-2 md:grid-cols-4 gap-3 text-sm">
        <Link to="/terms" className="text-amber-600 hover:underline">Syarat & Ketentuan</Link>
        <Link to="/privacy" className="text-amber-600 hover:underline">Kebijakan Privasi</Link>
        <Link to="/refund-policy" className="text-amber-600 hover:underline">Kebijakan Refund</Link>
        <Link to="/shipping-policy" className="text-amber-600 hover:underline">Kebijakan Pengiriman</Link>
      </div>
    </div>
  )
}
