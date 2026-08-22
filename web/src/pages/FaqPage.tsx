import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { HelpArticle } from '../types'

export function FaqPage() {
  const { data } = useQuery({
    queryKey: ['faq-articles'],
    queryFn: async () => (await api.get<{ articles: HelpArticle[] }>('/help/articles?section=help&q=faq')).data.articles,
  })

  const fallback = [
    'Bagaimana cara memesan produk?',
    'Kapan dana saya dilepas ke penjual?',
    'Bagaimana cara mengajukan retur?',
    'Bagaimana cara melacak pesanan?',
    'Kupon apa yang tersedia?',
  ]

  return (
    <div className="mx-auto max-w-3xl px-4 py-10">
      <h1 className="text-3xl font-extrabold mb-2">Pertanyaan Umum</h1>
      <p className="text-gray-500 text-sm mb-8">Jawaban cepat untuk pertanyaan yang paling sering ditanyakan.</p>

      <div className="space-y-3">
        {data?.length ? (
          data.map((a) => (
            <details key={a.id} className="bg-white border border-gray-200 rounded-xl p-5 group">
              <summary className="font-medium cursor-pointer flex justify-between items-center group-open:mb-3">
                {a.title}
                <span className="text-gray-400 group-open:rotate-45 transition-transform">+</span>
              </summary>
              <p className="text-sm text-gray-600">{a.excerpt}</p>
              <Link to={`/help/${a.slug}`} className="text-xs text-amber-600 hover:underline mt-2 inline-block">
                Baca selengkapnya →
              </Link>
            </details>
          ))
        ) : (
          fallback.map((q) => (
            <details key={q} className="bg-white border border-gray-200 rounded-xl p-5 group">
              <summary className="font-medium cursor-pointer flex justify-between items-center">
                {q}
                <span className="text-gray-400 group-open:rotate-45 transition-transform">+</span>
              </summary>
            </details>
          ))
        )}
      </div>

      <div className="mt-10 bg-amber-50 border border-amber-200 rounded-xl p-6 text-center">
        <p className="font-medium mb-2">Tidak menemukan jawabanmu?</p>
        <Link to="/contact" className="text-amber-600 font-medium hover:underline">
          Hubungi tim dukungan kami →
        </Link>
      </div>
    </div>
  )
}
