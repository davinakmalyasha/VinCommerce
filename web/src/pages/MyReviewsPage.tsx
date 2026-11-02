import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { formatDate } from '../lib/format'
import { QueryState } from '../components/QueryState'
import { FileUpload } from '../components/FileUpload'
import { Rating } from '../components/Rating'

interface OwnReview {
  id: string
  product_id: string
  product_name: string
  rating: number
  title: string
  content: string
  images?: string[]
  status: string
  reply?: string
  created_at: string
}

const statusLabels: Record<string, string> = {
  pending: 'Menunggu moderasi',
  approved: 'Tayang',
  rejected: 'Ditolak',
}

export function MyReviewsPage() {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<string | null>(null)
  const [form, setForm] = useState({ rating: 5, title: '', content: '' })
  const [images, setImages] = useState<string[]>([])
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null)

  const reviewsQuery = useQuery({
    queryKey: ['my-reviews'],
    queryFn: async () =>
      (await api.get<{ reviews: OwnReview[] }>('/products/reviews/mine')).data.reviews,
  })

  const startEdit = (r: OwnReview) => {
    setEditing(r.id)
    setForm({ rating: r.rating, title: r.title ?? '', content: r.content ?? '' })
    setImages(r.images ?? [])
    setConfirmDelete(null)
  }

  const save = useMutation({
    mutationFn: async (id: string) =>
      api.put(`/products/reviews/${id}`, {
        rating: form.rating,
        title: form.title,
        content: form.content,
        images: images.filter(Boolean),
      }),
    onSuccess: () => {
      setEditing(null)
      queryClient.invalidateQueries({ queryKey: ['my-reviews'] })
    },
  })

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/products/reviews/${id}`),
    onSuccess: () => {
      setConfirmDelete(null)
      queryClient.invalidateQueries({ queryKey: ['my-reviews'] })
    },
  })

  return (
    <div className="mx-auto max-w-3xl px-4 py-8 space-y-5">
      <div>
        <Link to="/account" className="text-sm text-gray-400 hover:text-gray-700">← Akun</Link>
        <h1 className="text-xl font-bold mt-1">Ulasan Saya</h1>
        <p className="text-xs text-gray-500 mt-1">
          Ulasan yang diubah kembali ke moderasi sebelum tayang.
        </p>
      </div>

      <QueryState query={reviewsQuery} label="ulasan">
        {(reviewsQuery.data?.length ?? 0) === 0 && !reviewsQuery.isLoading ? (
          <p className="text-sm text-gray-500">Belum ada ulasan.</p>
        ) : (
          <div className="space-y-3">
            {(reviewsQuery.data ?? []).map((r) => (
              <div key={r.id} className="bg-white border border-gray-200 rounded-xl p-5 space-y-2">
                <div className="flex items-start justify-between gap-2">
                  <div>
                    <p className="font-medium text-sm">{r.product_name}</p>
                    <p className="text-xs text-gray-500">Ditulis {formatDate(r.created_at)}</p>
                  </div>
                  <span className="px-2.5 py-0.5 rounded-full text-xs bg-gray-100 text-gray-600 shrink-0">
                    {statusLabels[r.status] ?? r.status}
                  </span>
                </div>

                {editing === r.id ? (
                  <div className="space-y-3 pt-1">
                    <div className="flex gap-1" role="radiogroup" aria-label="Rating">
                      {[1, 2, 3, 4, 5].map((n) => (
                        <button
                          key={n}
                          type="button"
                          role="radio"
                          aria-checked={n === form.rating}
                          aria-label={`${n} bintang`}
                          onClick={() => setForm({ ...form, rating: n })}
                          className={`text-xl ${n <= form.rating ? 'text-amber-500' : 'text-gray-300'}`}
                        >
                          ★
                        </button>
                      ))}
                    </div>
                    <div>
                      <label htmlFor={`edit-title-${r.id}`} className="block text-xs text-gray-500 mb-1">Judul</label>
                      <input
                        id={`edit-title-${r.id}`}
                        value={form.title}
                        onChange={(e) => setForm({ ...form, title: e.target.value })}
                        className="w-full px-3 py-2 border rounded-lg text-sm outline-none"
                      />
                    </div>
                    <div>
                      <label htmlFor={`edit-content-${r.id}`} className="block text-xs text-gray-500 mb-1">Ulasan</label>
                      <textarea
                        id={`edit-content-${r.id}`}
                        rows={3}
                        value={form.content}
                        onChange={(e) => setForm({ ...form, content: e.target.value })}
                        className="w-full px-3 py-2 border rounded-lg text-sm outline-none"
                      />
                    </div>
                    <div>
                      <p className="text-xs text-gray-500 mb-1">Foto (maksimal 3)</p>
                      <div className="space-y-2">
                        {images.map((url, i) => (
                          <FileUpload
                            key={i}
                            id={`edit-photo-${r.id}-${i}`}
                            label={`Foto ulasan ${i + 1}`}
                            value={url}
                            onChange={(next) =>
                              setImages((prev) => {
                                const nextArr = [...prev]
                                if (next) nextArr[i] = next
                                else nextArr.splice(i, 1)
                                return nextArr.filter(Boolean)
                              })
                            }
                          />
                        ))}
                        {images.length < 3 && (
                          <FileUpload
                            id={`edit-photo-${r.id}-new`}
                            label="Tambah foto"
                            value=""
                            onChange={(url) => {
                              if (url) setImages((prev) => [...prev, url])
                            }}
                          />
                        )}
                      </div>
                    </div>
                    {save.isError && (
                      <p role="alert" className="text-sm text-red-600">
                        Gagal menyimpan: {save.error.message}
                      </p>
                    )}
                    <div className="flex gap-2">
                      <button
                        type="button"
                        onClick={() => save.mutate(r.id)}
                        disabled={save.isPending || !form.content.trim()}
                        className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm font-medium disabled:opacity-50"
                      >
                        {save.isPending ? 'Menyimpan...' : 'Simpan'}
                      </button>
                      <button
                        type="button"
                        onClick={() => setEditing(null)}
                        className="px-4 py-2 rounded-lg border text-sm"
                      >
                        Batal
                      </button>
                    </div>
                  </div>
                ) : (
                  <>
                    <div className="flex items-center gap-2">
                      <Rating value={r.rating} size="text-xs" />
                      {r.title && <p className="text-sm font-medium">{r.title}</p>}
                    </div>
                    <p className="text-sm text-gray-600">{r.content}</p>
                    {r.reply && (
                      <div className="bg-amber-50 border border-amber-100 rounded-lg p-2.5">
                        <p className="text-xs font-semibold text-amber-700 mb-0.5">Balasan toko</p>
                        <p className="text-sm">{r.reply}</p>
                      </div>
                    )}
                    <div className="flex gap-3 pt-1">
                      <button
                        type="button"
                        onClick={() => startEdit(r)}
                        className="text-xs text-blue-600 hover:underline"
                      >
                        Ubah
                      </button>
                      {confirmDelete === r.id ? (
                        <span className="flex items-center gap-2 text-xs">
                          <span className="text-gray-500">Hapus ulasan ini?</span>
                          <button
                            type="button"
                            onClick={() => remove.mutate(r.id)}
                            disabled={remove.isPending}
                            className="text-red-600 font-medium hover:underline disabled:opacity-50"
                          >
                            {remove.isPending ? 'Menghapus...' : 'Ya, hapus'}
                          </button>
                          <button
                            type="button"
                            onClick={() => setConfirmDelete(null)}
                            className="text-gray-500 hover:underline"
                          >
                            Batal
                          </button>
                        </span>
                      ) : (
                        <button
                          type="button"
                          onClick={() => setConfirmDelete(r.id)}
                          className="text-xs text-red-600 hover:underline"
                        >
                          Hapus
                        </button>
                      )}
                    </div>
                    {remove.isError && confirmDelete === r.id && (
                      <p role="alert" className="text-sm text-red-600">
                        Gagal menghapus: {remove.error.message}
                      </p>
                    )}
                  </>
                )}
              </div>
            ))}
          </div>
        )}
      </QueryState>
    </div>
  )
}
