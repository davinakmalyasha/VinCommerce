import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatDate } from '../../lib/format'

interface PendingReview {
  id: string
  product_id: string
  user_name: string
  rating: number
  title: string
  content: string
  created_at: string
}

export function AdminReviews() {
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['admin-reviews'],
    queryFn: async () => (await api.get<{ reviews: PendingReview[] }>('/admin/reviews/pending')).data.reviews,
    refetchInterval: 15_000,
  })

  const moderate = useMutation({
    mutationFn: async ({ id, status }: { id: string; status: string }) =>
      api.post(`/admin/reviews/${id}/moderate`, { status }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-reviews'] }),
  })

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Moderasi Ulasan ({data?.length ?? 0})</h1>
      <div className="space-y-3">
        {data?.length === 0 && <p className="text-gray-500 text-sm">Tidak ada ulasan menunggu moderasi.</p>}
        {data?.map((r) => (
          <div key={r.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5">
            <div className="flex items-center justify-between">
              <p className="font-medium text-sm">
                {r.user_name} <span className="text-amber-500 ml-2">{'★'.repeat(r.rating)}</span>
              </p>
              <span className="text-xs text-gray-400">{formatDate(r.created_at)}</span>
            </div>
            {r.title && <p className="text-sm font-medium mt-2">{r.title}</p>}
            <p className="text-sm text-gray-600 dark:text-gray-300 mt-1">{r.content}</p>
            <div className="flex gap-2 mt-3">
              <button type="button"
                onClick={() => moderate.mutate({ id: r.id, status: 'approved' })}
                className="px-4 py-2 rounded-lg bg-green-600 text-white text-sm hover:bg-green-700"
              >
                Setujui
              </button>
              <button type="button"
                onClick={() => moderate.mutate({ id: r.id, status: 'rejected' })}
                className="px-4 py-2 rounded-lg border border-red-300 text-red-600 text-sm hover:bg-red-50"
              >
                Tolak
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
