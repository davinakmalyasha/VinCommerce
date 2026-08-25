import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { useMemo } from 'react'
import { api } from '../lib/api'
import type { HelpArticle } from '../types'
import { sanitizeHtml } from '../lib/sanitize'

// Legal and static pages rendered from admin-editable seeded articles.
const SLUGS: Record<string, string> = {
  terms: 'syarat-ketentuan',
  privacy: 'kebijakan-privasi',
  'refund-policy': 'kebijakan-refund',
  'shipping-policy': 'kebijakan-pengiriman',
}

const TITLES: Record<string, string> = {
  terms: 'Syarat & Ketentuan',
  privacy: 'Kebijakan Privasi',
  'refund-policy': 'Kebijakan Refund',
  'shipping-policy': 'Kebijakan Pengiriman',
}

export function LegalPage({ page }: { page: keyof typeof SLUGS }) {
  const slug = SLUGS[page]

  const { data } = useQuery({
    queryKey: ['legal', slug],
    queryFn: async () => (await api.get<{ article: HelpArticle }>(`/help/articles/${slug}`)).data.article,
  })

  const safeContent = useMemo(() => sanitizeHtml(data?.content ?? ''), [data?.content])

  return (
    <div className="mx-auto max-w-3xl px-4 py-10">
      <Link to="/docs" className="text-sm text-gray-400 hover:text-gray-700">← Dokumentasi</Link>
      <h1 className="text-3xl font-extrabold mt-4 mb-6">{TITLES[page]}</h1>
      {data ? (
        <div
          className="prose prose-sm prose-gray max-w-none bg-white border border-gray-200 rounded-2xl p-8"
          dangerouslySetInnerHTML={{ __html: safeContent }}
        />
      ) : (
        <p className="text-gray-500 text-sm">Dokumen sedang disiapkan.</p>
      )}
    </div>
  )
}
