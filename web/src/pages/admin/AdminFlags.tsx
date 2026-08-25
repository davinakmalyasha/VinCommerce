import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'

interface Flag {
  id: string
  key: string
  description: string
  enabled: boolean
  updated_at: string
}

// Mirrors the flags seeded by cmd/seed — keys here MUST match the enforced
// flagMw("key") call sites in the backend router exactly.
const DEFAULT_FLAGS: { key: string; description: string }[] = [
  { key: 'flash_sales', description: 'Aktifkan program Flash Sale' },
  { key: 'referrals', description: 'Program referral (bonus kode)' },
  { key: 'loyalty_points', description: 'Poin loyalitas (earned & spend)' },
  { key: 'ai_assistant', description: 'Asisten AI di chat widget' },
  { key: 'games', description: 'Bonus Center (check-in & roda hadiah)' },
  { key: 'live_commerce', description: 'Live shopping' },
]

export function AdminFlags() {
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['admin-flags'],
    queryFn: async () => (await api.get<{ flags: Flag[] }>('/admin/flags')).data.flags,
  })

  const toggle = useMutation({
    mutationFn: async ({ key, enabled }: { key: string; enabled: boolean }) =>
      api.post(`/admin/flags/${key}/toggle`, { enabled }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-flags'] }),
  })

  const known = data?.length ? data : DEFAULT_FLAGS.map((f) => ({ ...f, id: f.key, enabled: false, updated_at: '' }))

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Feature Flags</h1>
      <p className="text-sm text-gray-500">
        Fitur yang dimatikan akan menolak permintaan dengan kode <code>FEATURE_DISABLED</code>.
      </p>
      <div className="space-y-3">
        {known.map((f) => (
          <div key={f.key} className="bg-white border border-gray-200 rounded-xl p-5 flex items-center justify-between">
            <div>
              <p className="font-mono font-medium text-sm">{f.key}</p>
              <p className="text-xs text-gray-500 mt-0.5">{f.description}</p>
            </div>
            <button
              onClick={() => toggle.mutate({ key: f.key, enabled: !f.enabled })}
              className={`w-14 h-8 rounded-full relative transition-colors ${f.enabled ? 'bg-green-500' : 'bg-gray-300'}`}
              title={f.enabled ? 'Aktif' : 'Nonaktif'}
            >
              <span
                className={`absolute top-1 w-6 h-6 rounded-full bg-white shadow transition-all ${f.enabled ? 'left-7' : 'left-1'}`}
              />
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}
