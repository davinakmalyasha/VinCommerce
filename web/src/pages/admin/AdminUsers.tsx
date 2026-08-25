import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import { useSession } from '../../stores/session'
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
  const navigate = useNavigate()
  const startImpersonation = useSession((s) => s.startImpersonation)
  const [q, setQ] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)

  const { data } = useQuery({
    queryKey: ['admin-users', search, page],
    queryFn: async () =>
      (
        await api.get<{ users: AdminUser[]; total: number }>('/admin/users', {
          params: { q: search || undefined, page, page_size: 20 },
        })
      ).data,
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

  // Role revocation with per-row role picker.
  const [revokeFor, setRevokeFor] = useState<string | null>(null)
  const [revokeRole, setRevokeRole] = useState('seller')
  const revoke = useMutation({
    mutationFn: async ({ id, role }: { id: string; role: string }) =>
      api.post(`/admin/users/${id}/revoke-role`, { role }),
    onSuccess: () => {
      setRevokeFor(null)
      queryClient.invalidateQueries({ queryKey: ['admin-users'] })
    },
    onError: (e: Error) => alert(e.message),
  })

  const impersonate = useMutation({
    mutationFn: async (id: string) =>
      (await api.post<{ access_token: string; user: AdminUser & Record<string, unknown> }>(`/admin/users/${id}/impersonate`)).data,
    onSuccess: (res) => {
      startImpersonation(res.access_token, res.user as never)
      navigate('/')
    },
    onError: (e: Error) => alert(e.message),
  })

  const visible = (data?.users ?? []).filter((u) =>
    search ? u.full_name.toLowerCase().includes(search.toLowerCase()) || u.email.toLowerCase().includes(search.toLowerCase()) : true,
  )
  const totalPages = Math.max(1, Math.ceil((data?.total ?? 0) / 20))

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-bold">Manajemen Pengguna ({data?.total ?? 0})</h1>
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && setSearch(q)}
          placeholder="Cari nama / email..."
          className="px-3 py-2 border rounded-lg text-sm outline-none w-56 dark:bg-gray-800"
        />
      </div>
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 dark:bg-gray-800 text-left text-xs text-gray-500">
            <tr>
              <th className="px-4 py-3">Pengguna</th>
              <th className="px-4 py-3">Peran</th>
              <th className="px-4 py-3">2FA</th>
              <th className="px-4 py-3">Bergabung</th>
              <th className="px-4 py-3">Status</th>
              <th className="px-4 py-3">Aksi</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
            {visible.map((u) => (
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
                <td className="px-4 py-3 flex flex-wrap gap-2">
                  {!u.roles.includes('admin') && u.status === 'active' && (
                    <button
                      onClick={() => impersonate.mutate(u.id)}
                      disabled={impersonate.isPending}
                      className="text-xs text-indigo-600 hover:underline disabled:opacity-50"
                      title="Masuk sebagai pengguna ini untuk support"
                    >
                      🎭 Login sebagai
                    </button>
                  )}
                  {!u.roles.includes('seller') && (
                    <button
                      onClick={() => grantSeller.mutate(u.id)}
                      className="text-xs text-blue-600 hover:underline"
                    >
                      Jadikan seller
                    </button>
                  )}
                  {u.roles.filter((r) => r !== 'buyer').length > 0 && (
                    revokeFor === u.id ? (
                      <span className="inline-flex items-center gap-1">
                        <select
                          value={revokeRole}
                          onChange={(e) => setRevokeRole(e.target.value)}
                          className="border rounded px-1.5 py-0.5 text-xs bg-transparent"
                        >
                          {u.roles.filter((r) => r !== 'buyer').map((r) => (
                            <option key={r} value={r}>{r}</option>
                          ))}
                        </select>
                        <button
                          onClick={() => {
                            if (confirm(`Cabut peran "${revokeRole}" dari ${u.email}?`)) {
                              revoke.mutate({ id: u.id, role: revokeRole })
                            }
                          }}
                          disabled={revoke.isPending}
                          className="text-xs text-red-600 hover:underline disabled:opacity-50"
                        >
                          ✓
                        </button>
                        <button onClick={() => setRevokeFor(null)} className="text-xs text-gray-400 hover:underline">
                          ✕
                        </button>
                      </span>
                    ) : (
                      <button
                        onClick={() => {
                          setRevokeFor(u.id)
                          setRevokeRole(u.roles.find((r) => r !== 'buyer') ?? 'seller')
                        }}
                        className="text-xs text-red-500 hover:underline"
                        title="Cabut peran (demote)"
                      >
                        Cabut peran
                      </button>
                    )
                  )}
                  {u.status === 'active' ? (
                    <button
                      onClick={() => {
                        if (confirm(`Suspensi akun ${u.email}?`)) setStatus.mutate({ id: u.id, status: 'suspended' })
                      }}
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
      {(data?.total ?? 0) > 20 && (
        <div className="flex items-center justify-center gap-3">
          <button onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page <= 1} className="px-3 py-1.5 rounded-lg border text-sm disabled:opacity-40">
            ← Sebelumnya
          </button>
          <span className="text-sm text-gray-500">Halaman {page} / {totalPages}</span>
          <button onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={page >= totalPages} className="px-3 py-1.5 rounded-lg border text-sm disabled:opacity-40">
            Berikutnya →
          </button>
        </div>
      )}
    </div>
  )
}
