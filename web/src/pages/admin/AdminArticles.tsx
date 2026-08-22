import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import type { HelpArticle, HelpCategory } from '../../types'

export function AdminArticles() {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<HelpArticle | null>(null)
  const [showForm, setShowForm] = useState(false)

  const { data } = useQuery({
    queryKey: ['admin-articles'],
    queryFn: async () => (await api.get<{ articles: HelpArticle[] }>('/admin/articles')).data.articles,
  })

  const { data: categories } = useQuery({
    queryKey: ['help-categories'],
    queryFn: async () => (await api.get<{ categories: HelpCategory[] }>('/help/categories')).data.categories,
  })

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/admin/articles/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-articles'] }),
  })

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Editor Artikel ({data?.length ?? 0})</h1>
        <button
          onClick={() => {
            setEditing(null)
            setShowForm(true)
          }}
          className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm font-medium hover:bg-amber-600"
        >
          + Artikel Baru
        </button>
      </div>

      <div className="space-y-2">
        {data?.map((a) => (
          <div key={a.id} className="bg-white border border-gray-200 rounded-xl p-4 flex items-center justify-between">
            <div>
              <div className="flex items-center gap-2">
                <p className="font-medium text-sm">{a.title}</p>
                <span className="px-2 py-0.5 rounded-full bg-gray-100 text-gray-600 text-xs">{a.section}</span>
                <span className={`px-2 py-0.5 rounded-full text-xs ${a.is_published ? 'bg-green-100 text-green-700' : 'bg-gray-200 text-gray-500'}`}>
                  {a.is_published ? 'terbit' : 'draft'}
                </span>
                <span className="text-xs text-gray-400">{a.view_count}× dilihat</span>
              </div>
              <p className="text-xs text-gray-500 mt-0.5">{a.excerpt}</p>
            </div>
            <div className="flex gap-3">
              <button
                onClick={() => {
                  setEditing(a)
                  setShowForm(true)
                }}
                className="text-xs text-blue-600 hover:underline"
              >
                Edit
              </button>
              <button onClick={() => remove.mutate(a.id)} className="text-xs text-red-500 hover:underline">
                Hapus
              </button>
            </div>
          </div>
        ))}
      </div>

      {showForm && (
        <ArticleForm
          article={editing}
          categories={categories ?? []}
          onClose={() => setShowForm(false)}
          onSaved={() => {
            setShowForm(false)
            setEditing(null)
            queryClient.invalidateQueries({ queryKey: ['admin-articles'] })
            queryClient.invalidateQueries({ queryKey: ['help-articles'] })
          }}
        />
      )}
    </div>
  )
}

function ArticleForm({
  article,
  categories,
  onClose,
  onSaved,
}: {
  article: HelpArticle | null
  categories: HelpCategory[]
  onClose: () => void
  onSaved: () => void
}) {
  const [form, setForm] = useState({
    title: article?.title ?? '',
    category_id: article?.category_id ?? '',
    excerpt: article?.excerpt ?? '',
    content: article?.content ?? '',
    section: article?.section ?? 'help',
    is_published: article?.is_published ?? false,
  })
  const [error, setError] = useState('')

  const save = useMutation({
    mutationFn: async () => {
      const payload = {
        title: form.title,
        category_id: form.category_id || undefined,
        excerpt: form.excerpt,
        content: form.content,
        section: form.section,
        is_published: form.is_published,
      }
      if (article) {
        await api.put(`/admin/articles/${article.id}`, payload)
      } else {
        await api.post('/admin/articles', payload)
      }
    },
    onSuccess: onSaved,
    onError: (e: Error) => setError(e.message),
  })

  return (
    <div className="fixed inset-0 z-50 overflow-y-auto">
      <div className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div className="relative mx-auto my-8 max-w-2xl bg-white rounded-2xl shadow-xl p-6">
        <div className="flex justify-between items-center mb-4">
          <h2 className="font-bold text-lg">{article ? 'Edit Artikel' : 'Artikel Baru'}</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-700">✕</button>
        </div>
        <div className="space-y-3">
          <input
            placeholder="Judul"
            value={form.title}
            onChange={(e) => setForm({ ...form, title: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <div className="grid grid-cols-2 gap-3">
            <select
              value={form.section}
              onChange={(e) => setForm({ ...form, section: e.target.value })}
              className="px-4 py-3 border rounded-xl text-sm outline-none"
            >
              <option value="help">Bantuan</option>
              <option value="docs">Dokumentasi</option>
              <option value="legal">Legal</option>
            </select>
            <select
              value={form.category_id}
              onChange={(e) => setForm({ ...form, category_id: e.target.value })}
              className="px-4 py-3 border rounded-xl text-sm outline-none"
            >
              <option value="">Tanpa kategori</option>
              {categories.map((c) => (
                <option key={c.id} value={c.id}>{c.name}</option>
              ))}
            </select>
          </div>
          <input
            placeholder="Ringkasan (excerpt)"
            value={form.excerpt}
            onChange={(e) => setForm({ ...form, excerpt: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none"
          />
          <textarea
            placeholder="Konten (HTML)"
            rows={8}
            value={form.content}
            onChange={(e) => setForm({ ...form, content: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm font-mono outline-none focus:border-amber-400"
          />
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.is_published}
              onChange={(e) => setForm({ ...form, is_published: e.target.checked })}
            />
            Terbitkan
          </label>
          {error && <p className="text-sm text-red-600">{error}</p>}
          <div className="flex gap-2">
            <button
              onClick={() => save.mutate()}
              disabled={save.isPending || !form.title || !form.content}
              className="flex-1 py-3 rounded-xl bg-amber-500 text-white font-semibold disabled:opacity-50"
            >
              {save.isPending ? 'Menyimpan...' : 'Simpan'}
            </button>
            <button onClick={onClose} className="px-6 py-3 rounded-xl border text-sm">Batal</button>
          </div>
        </div>
      </div>
    </div>
  )
}
