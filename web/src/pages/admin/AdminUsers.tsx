import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { formatDate } from '../../lib/format'

interface AdminUser {
  id: string
  email: string
  full_name: string
  roles: string[]
  status: string
  two_factor_enabled: boolean
  created_at: string
}

export function AdminUsers() {
  const queryClient = useQueryClient()

  const { data } = useQuery({
    queryKey: ['admin-users'],
    queryFn: async () => (await api.get<{ users: AdminUser[]; total: number }>('/admin/users')).data,
  })

  const setStatus = useMutation({
    mutationFn: async ({ id, status }: { id: string; status: string }) =>
      api.post(`/admin/users/${id}/status`, { status }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-users'] }),
  })

  const grantSeller = useMutation({
    mutationFn: async (id: string) => api.post(`/admin/users/${id}/grant-seller`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['admin-users'] }),
  })

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-bold">Manajemen Pengguna ({data?.total ?? 0})</h1>
      <div className="bg-white border border-gray-200 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 text-left text-xs text-gray-500">
            <tr>
              <th className="px-4 py-3">Pengguna</th>
              <th className="px-4 py-3">Peran</th>
              <th className="px-4 py-3">2FA</th>
              <th className="px-4 py-3">Bergabung</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Aksi</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100">
            {data?.users.map((u) => (
              <tr key={u.id}>
                <td className="px-4 py-3">
                  <p className="font-medium">{u.full_name}</p>
                  <p className="text-xs text-gray-500">{u.email}</p>
                </td>
                <td className="px-4 py-3">
                  <div className="flex gap-1">
                    {u.roles.map((r) => (
                      <span key={r} className="px-2 py-0.5 rounded-full bg-amber-100 text-amber-700 text-xs">
                        {r}
                      </span>
                    ))}
                  </div>
                </td>
                <td className="px-4 py-3">{u.two_factor_enabled ? '✓' : '—'}</td>
                <td className="px-4 py-3 text-xs text-gray-500">{formatDate(u.created_at)}</td>
                <td className="px-4 py-3">
                  <span className={`px-2 py-0.5 rounded-full text-xs ${
                    u.status === 'active' ? 'bg-green-100 text-green-700'
                    : u.status === 'suspended' ? 'bg-orange-100 text-orange-700'
                    : 'bg-red-100 text-red-600'
                  }`}>
                    {u.status}
                  </span>
                </td>
                <td className="px-4 py-3 flex gap-2">
                  {!u.roles.includes('seller') && (
                    <button
                      onClick={() => grantSeller.mutate(u.id)}
                      className="text-xs text-blue-600 hover:underline"
                    >
                      Jadikan seller
                    </button>
                  )}
                  {u.status === 'active' ? (
                    <button
                      onClick={() => setStatus.mutate({ id: u.id, status: 'suspended' })}
                      className="text-xs text-orange-600 hover:underline"
                    >
                      Suspensi
                    </button>
                  ) : (
                    <button
                      onClick={() => setStatus.mutate({ id: u.id, status: 'active' })}
                      className="text-xs text-green-600 hover:underline"
                    >
                      Aktifkan
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
