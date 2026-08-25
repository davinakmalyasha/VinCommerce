import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import { useMemo } from 'react'
import { api } from '../lib/api'
import type { HelpArticle } from '../types'
import { Seo } from '../components/Seo'
import { sanitizeHtml } from '../lib/sanitize'

export function HelpArticlePage() {
  const { slug } = useParams()

  const { data, isError } = useQuery({
    queryKey: ['help-article', slug],
    queryFn: async () => (await api.get<{ article: HelpArticle }>(`/help/articles/${slug}`)).data.article,
    retry: false,
  })

  // Admin-authored free-text HTML — sanitize before it ever touches the DOM.
  const safeContent = useMemo(() => sanitizeHtml(data?.content ?? ''), [data?.content])

  if (isError) {
    return <div className="mx-auto max-w-3xl px-4 py-16 text-center text-gray-500">Artikel tidak ditemukan.</div>
  }

  if (!data) {
    return <div className="mx-auto max-w-3xl px-4 py-16 text-center text-gray-500">Memuat...</div>
  }

  return (
    <>
      <Seo title={`${data.title} — Pusat Bantuan VinCommerce`} path={`/help/${data.slug}`} />
      <div className="mx-auto max-w-3xl px-4 py-10">
      <Link to="/help" className="text-sm text-gray-400 hover:text-gray-700">
        ← Pusat Bantuan
      </Link>
      <div className="mt-4 bg-white border border-gray-200 rounded-2xl p-8">
        <div className="flex items-center gap-2 mb-3">
          {data.category && (
            <span className="text-xs px-2.5 py-0.5 rounded-full bg-amber-100 text-amber-700">{data.category.name}</span>
          )}
        </div>
        <h1 className="text-2xl font-bold mb-4">{data.title}</h1>
        <div
          className="prose prose-sm prose-gray max-w-none"
          dangerouslySetInnerHTML={{ __html: safeContent }}
        />
        <div className="border-t mt-8 pt-4 flex items-center justify-between text-xs text-gray-400">
          <span>Diperbarui {new Date(data.updated_at).toLocaleDateString('id-ID')}</span>
          <span>{data.view_count}× dilihat</span>
        </div>
      </div>
    </div>
    </>
  )
}
