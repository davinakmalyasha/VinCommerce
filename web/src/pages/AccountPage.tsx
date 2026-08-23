import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, Navigate } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import type { Address } from '../types'
import { BonusCenter } from './BonusCenter'
import { NotificationPrefsCard } from '../components/NotificationPrefsCard'
import { FileUpload } from '../components/FileUpload'

export function AccountPage() {
  const { user } = useSession()
  const [tab, setTab] = useState<'profil' | 'keamanan' | 'alamat' | 'pantauan' | 'undang' | 'bonus'>('profil')

  if (!user) return <Navigate to="/login" replace />

  return (
    <div className="mx-auto max-w-4xl px-4 py-6">
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-2xl p-6 mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-xl font-bold">{user.full_name}</h1>
          <p className="text-sm text-gray-500">{user.email}</p>
          <div className="flex gap-2 mt-2">
            {user.roles.map((r) => (
              <span key={r} className="px-2.5 py-0.5 rounded-full bg-amber-100 text-amber-700 text-xs">
                {r}
              </span>
            ))}
          </div>
        </div>
        <nav className="flex gap-1 text-sm">
          {(['profil', 'keamanan', 'alamat', 'pantauan', 'undang', 'bonus'] as const).map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={`px-4 py-2 rounded-lg capitalize ${tab === t ? 'bg-amber-500 text-white' : 'hover:bg-gray-100 dark:hover:bg-gray-800'}`}
            >
              {t === 'pantauan' ? 'Pantauan' : t === 'undang' ? 'Poin & Undang' : t === 'bonus' ? '🎁 Bonus' : t}
            </button>
          ))}
        </nav>
      </div>

      <div className="flex flex-wrap gap-2">
        {[
          { to: '/wallet', label: '💰 Dompet Saya' },
          { to: '/returns', label: '↩️ Retur Saya' },
          { to: '/vouchers', label: '🎟️ Voucher Saya' },
          { to: '/notifications', label: '🔔 Notifikasi' },
        ].map((l) => (
          <Link
            key={l.to}
            to={l.to}
            className="px-4 py-2 rounded-full bg-white border border-gray-200 text-sm hover:border-amber-400"
          >
            {l.label}
          </Link>
        ))}
      </div>

      <NotificationPrefsCard />

      {tab === 'profil' && <ProfileTab />}
      {tab === 'keamanan' && <SecurityTab />}
      {tab === 'alamat' && <AddressesTab />}
      {tab === 'pantauan' && <WatchesTab />}
      {tab === 'undang' && <ReferralTab />}
      {tab === 'bonus' && <BonusCenter />}
    </div>
  )
}

function ProfileTab() {
  const { user } = useSession()
  const [name, setName] = useState(user?.full_name ?? '')
  const [phone, setPhone] = useState(user?.phone ?? '')
  const [avatarUrl, setAvatarUrl] = useState(user?.avatar_url ?? '')
  const [saved, setSaved] = useState(false)

  const save = useMutation({
    mutationFn: async () => api.put('/auth/profile', { full_name: name, phone, avatar_url: avatarUrl }),
    onSuccess: () => {
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
    },
  })

  return (
    <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-6 space-y-4">
      <h2 className="font-bold">Profil</h2>
      <div className="flex items-center gap-4">
        {avatarUrl ? (
          <img src={avatarUrl} alt="" className="w-20 h-20 rounded-full object-cover bg-gray-100 border border-gray-200" />
        ) : (
          <div className="w-20 h-20 rounded-full bg-amber-500 flex items-center justify-center text-2xl font-extrabold text-white">
            {name.slice(0, 1).toUpperCase()}
          </div>
        )}
        <FileUpload value={avatarUrl} onChange={setAvatarUrl} label="Unggah foto profil" />
      </div>
      <div>
        <label className="text-sm font-medium block mb-1">Nama lengkap</label>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400 dark:bg-gray-800"
        />
      </div>
      <div>
        <label className="text-sm font-medium block mb-1">No. HP</label>
        <input
          value={phone}
          onChange={(e) => setPhone(e.target.value)}
          className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400 dark:bg-gray-800"
        />
      </div>
      <button
        onClick={() => save.mutate()}
        disabled={save.isPending}
        className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium disabled:opacity-50"
      >
        {save.isPending ? 'Menyimpan...' : 'Simpan Profil'}
      </button>
      {saved && <p className="text-sm text-green-600">Profil diperbarui!</p>}
    </div>
  )
}

function SecurityTab() {
  const { user } = useSession()
  const [cur, setCur] = useState('')
  const [next, setNext] = useState('')
  const [msg, setMsg] = useState('')
  const [totp, setTotp] = useState<{ secret: string; otpauth_url: string } | null>(null)
  const [totpCode, setTotpCode] = useState('')
  const [backupCodes, setBackupCodes] = useState<string[] | null>(null)

  const changePass = useMutation({
    mutationFn: async () => api.post('/auth/password/change', { current_password: cur, new_password: next }),
    onSuccess: () => {
      setMsg('Password diganti! Sesi lain telah diakhiri.')
      setCur('')
      setNext('')
    },
    onError: (e: Error) => setMsg(e.message),
  })

  const setupTotp = useMutation({
    mutationFn: async () => (await api.post<{ secret: string; otpauth_url: string }>('/auth/2fa/setup')).data,
    onSuccess: setTotp,
  })

  const confirmTotp = useMutation({
    mutationFn: async () =>
      (await api.post<{ enabled: boolean; backup_codes: string[] }>('/auth/2fa/confirm', { code: totpCode })).data,
    onSuccess: (res) => {
      setTotp(null)
      setMsg('2FA aktif!')
      setBackupCodes(res.backup_codes)
      window.location.reload()
    },
  })

  const disableTotp = useMutation({
    mutationFn: async () => api.post('/auth/2fa/disable', { code: totpCode }),
    onSuccess: () => setMsg('2FA dinonaktifkan'),
  })

  const regenerateBackup = useMutation({
    mutationFn: async () =>
      (await api.post<{ backup_codes: string[] }>('/auth/2fa/backup-codes/regenerate')).data.backup_codes,
    onSuccess: (codes) => setBackupCodes(codes),
  })

  const { data: backupInfo } = useQuery({
    queryKey: ['backup-codes'],
    queryFn: async () => (await api.get<{ total: number; remaining: number }>('/auth/2fa/backup-codes')).data,
    enabled: !!user?.two_factor_enabled,
  })

  return (
    <div className="space-y-4">
      {backupCodes && (
        <div className="bg-white dark:bg-gray-900 border border-amber-300 rounded-xl p-6 space-y-3">
          <h2 className="font-bold">🔑 Kode Cadangan 2FA</h2>
          <p className="text-sm text-gray-600">
            Simpan kode berikut di tempat aman. Setiap kode hanya bisa dipakai <b>sekali</b> untuk masuk
            jika kamu kehilangan akses ke aplikasi autentikator.
          </p>
          <div className="grid grid-cols-2 gap-2">
            {backupCodes.map((c) => (
              <code key={c} className="font-mono text-sm bg-gray-100 dark:bg-gray-800 rounded-lg p-2 text-center">
                {c}
              </code>
            ))}
          </div>
          <button
            onClick={() => {
              navigator.clipboard?.writeText(backupCodes.join('\n')).catch(() => {})
              setMsg('Kode cadangan disalin!')
            }}
            className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm"
          >
            Salin Semua
          </button>
        </div>
      )}

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-6 space-y-3">
        <h2 className="font-bold">Ganti Password</h2>
        <input
          type="password"
          placeholder="Password saat ini"
          value={cur}
          onChange={(e) => setCur(e.target.value)}
          className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
        />
        <input
          type="password"
          placeholder="Password baru (min. 8 karakter)"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
        />
        <button
          onClick={() => changePass.mutate()}
          disabled={changePass.isPending || !cur || !next}
          className="px-6 py-3 rounded-xl bg-gray-900 dark:bg-gray-100 dark:text-gray-900 text-white font-medium disabled:opacity-40"
        >
          Ganti Password
        </button>
        {msg && <p className="text-sm text-green-600">{msg}</p>}
      </div>

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-6 space-y-3">
        <h2 className="font-bold">Verifikasi Dua Langkah (2FA)</h2>
        <p className="text-sm text-gray-500">
          Status: {user?.two_factor_enabled ? '✓ Aktif' : 'Nonaktif'}
        </p>
        {!user?.two_factor_enabled && !totp && (
          <button
            onClick={() => setupTotp.mutate()}
            disabled={setupTotp.isPending}
            className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium disabled:opacity-50"
          >
            Aktifkan 2FA
          </button>
        )}
        {totp && (
          <div className="space-y-3 bg-gray-50 dark:bg-gray-800 rounded-xl p-4">
            <p className="text-sm">
              Buka aplikasi autentikator (Google Authenticator / Authy) dan scan URL berikut, atau masukkan secret manual:
            </p>
            <a href={totp.otpauth_url} className="text-xs text-amber-600 break-all hover:underline">
              {totp.otpauth_url}
            </a>
            <p className="font-mono text-xs bg-gray-100 dark:bg-gray-700 p-2 rounded">Secret: {totp.secret}</p>
            <input
              placeholder="Kode 6 digit"
              value={totpCode}
              onChange={(e) => setTotpCode(e.target.value)}
              className="w-full px-4 py-3 border rounded-xl text-sm outline-none"
            />
            <button
              onClick={() => confirmTotp.mutate()}
              disabled={confirmTotp.isPending || totpCode.length < 6}
              className="px-6 py-3 rounded-xl bg-amber-500 text-white font-medium disabled:opacity-50"
            >
              Konfirmasi & Aktifkan
            </button>
          </div>
        )}
        {user?.two_factor_enabled && (
          <div className="space-y-3">
            <input
              placeholder="Kode 6 digit untuk nonaktifkan"
              value={totpCode}
              onChange={(e) => setTotpCode(e.target.value)}
              className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
            />
            <button
              onClick={() => disableTotp.mutate()}
              disabled={disableTotp.isPending || totpCode.length < 6}
              className="px-6 py-3 rounded-xl border border-red-300 text-red-600 font-medium disabled:opacity-50"
            >
              Nonaktifkan 2FA
            </button>
            <div className="border-t pt-3 text-sm">
              <p className="text-gray-600">
                Kode cadangan: {backupInfo?.remaining ?? '—'} tersisa (dari {backupInfo?.total ?? '—'})
              </p>
              <button
                onClick={() => regenerateBackup.mutate()}
                disabled={regenerateBackup.isPending}
                className="mt-2 px-4 py-2 rounded-lg border border-amber-400 text-amber-600 text-sm disabled:opacity-50"
              >
                {regenerateBackup.isPending ? 'Membuat...' : 'Buat kode cadangan baru'}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

interface RestockAlert {
  id: string
  variant_id: string
  status: string
  product_name: string
  variant_name: string
  created_at: string
}

interface PriceAlert {
  id: string
  variant_id: string
  status: string
  target_price: number
  current_price: number
  product_name: string
  variant_name: string
  created_at: string
}

function WatchesTab() {
  const queryClient = useQueryClient()

  const { data: restock } = useQuery({
    queryKey: ['restock-alerts'],
    queryFn: async () => (await api.get<{ alerts: RestockAlert[] }>('/back-in-stock')).data.alerts,
  })

  const { data: prices } = useQuery({
    queryKey: ['price-alerts'],
    queryFn: async () => (await api.get<{ alerts: PriceAlert[] }>('/price-alerts')).data.alerts,
  })

  const cancelRestock = useMutation({
    mutationFn: async (id: string) => api.delete(`/back-in-stock/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['restock-alerts'] }),
  })

  const cancelPrice = useMutation({
    mutationFn: async (id: string) => api.delete(`/price-alerts/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['price-alerts'] }),
  })

  return (
    <div className="space-y-4">
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-6 space-y-3">
        <h2 className="font-bold">🔔 Notifikasi Stok</h2>
        {!restock?.length && <p className="text-sm text-gray-500">Belum ada. Aktifkan dari halaman produk saat stok habis.</p>}
        {restock?.map((a) => (
          <div key={a.id} className="flex items-center justify-between border-b border-gray-50 dark:border-gray-800 pb-3 last:border-0">
            <div>
              <p className="text-sm font-medium">{a.product_name}</p>
              <p className="text-xs text-gray-500">{a.variant_name}</p>
            </div>
            <button onClick={() => cancelRestock.mutate(a.id)} className="text-xs text-red-500 hover:underline">
              Hapus
            </button>
          </div>
        ))}
      </div>

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-6 space-y-3">
        <h2 className="font-bold">💸 Pantauan Harga</h2>
        {!prices?.length && <p className="text-sm text-gray-500">Belum ada pantauan harga.</p>}
        {prices?.map((a) => (
          <div key={a.id} className="flex items-center justify-between border-b border-gray-50 dark:border-gray-800 pb-3 last:border-0">
            <div>
              <p className="text-sm font-medium">{a.product_name}</p>
              <p className="text-xs text-gray-500">
                {a.variant_name} · target Rp{a.target_price.toLocaleString('id-ID')} · sekarang Rp
                {a.current_price.toLocaleString('id-ID')}
              </p>
            </div>
            <button onClick={() => cancelPrice.mutate(a.id)} className="text-xs text-red-500 hover:underline">
              Hapus
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}

const REASON_LABELS: Record<string, string> = {
  order_complete: 'Poin belanja',
  referral_bonus: 'Bonus undangan',
  referral_reward: 'Hadiah referral',
  referral_redeem: 'Kode referral',
}

interface LoyaltyEntry {
  id: number
  change: number
  reason: string
  ref_id: string
  created_at: string
}

function ReferralTab() {
  const [copied, setCopied] = useState(false)

  const { data: referral } = useQuery({
    queryKey: ['referral-code'],
    queryFn: async () => (await api.get<{ code: string }>('/referral/code')).data,
  })

  const { data: loyalty } = useQuery({
    queryKey: ['loyalty'],
    queryFn: async () => (await api.get<{ balance: number; ledger: LoyaltyEntry[] }>('/loyalty')).data,
  })

  const code = referral?.code ?? ''
  const shareUrl = `${window.location.origin}/register?ref=${encodeURIComponent(code)}`

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // clipboard unavailable
    }
  }

  return (
    <div className="space-y-4">
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-6 space-y-4">
        <h2 className="font-bold">🎁 Undang Teman</h2>
        <p className="text-sm text-gray-500">
          Bagikan kode undanganmu. Kamu dan temanmu masing-masing mendapat <b>500 poin</b> saat mereka mendaftar
          dan memakai kode ini.
        </p>
        <div className="flex items-center gap-2">
          <code className="flex-1 px-4 py-3 bg-gray-100 dark:bg-gray-800 rounded-xl font-mono font-bold text-center">
            {code || 'Memuat...'}
          </code>
          <button
            onClick={copy}
            className="px-4 py-3 rounded-xl bg-amber-500 text-white text-sm font-medium hover:bg-amber-600"
          >
            {copied ? '✓ Tersalin' : 'Salin'}
          </button>
        </div>
        <a
          href={`https://wa.me/?text=${encodeURIComponent('Belanja di VinCommerce pakai kode undanganku: ' + code)}`}
          target="_blank"
          rel="noreferrer"
          className="inline-block px-4 py-2 rounded-xl border border-gray-300 text-sm hover:border-amber-400"
        >
          Bagikan via WhatsApp
        </a>
        <p className="text-xs text-gray-400">Link undangan: {shareUrl}</p>
      </div>

      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-6 space-y-3">
        <div className="flex items-center justify-between">
          <h2 className="font-bold">⭐ Poin Loyalitas</h2>
          <p className="text-2xl font-extrabold text-amber-600">{loyalty?.balance ?? 0} poin</p>
        </div>
        <p className="text-xs text-gray-500">Dapatkan 1 poin per Rp1.000 belanja yang selesai.</p>
        {!loyalty?.ledger.length && <p className="text-sm text-gray-500">Belum ada riwayat poin.</p>}
        {loyalty?.ledger.map((e) => (
          <div key={e.id} className="flex items-center justify-between border-b border-gray-50 dark:border-gray-800 pb-2 last:border-0">
            <div>
              <p className="text-sm font-medium">{REASON_LABELS[e.reason] ?? e.reason}</p>
              <p className="text-xs text-gray-400">{new Date(e.created_at).toLocaleDateString('id-ID')}</p>
            </div>
            <p className={`text-sm font-bold ${e.change > 0 ? 'text-green-600' : 'text-red-500'}`}>
              {e.change > 0 ? '+' : ''}{e.change}
            </p>
          </div>
        ))}
      </div>
    </div>
  )
}

function AddressesTab() {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<Address | null>(null)
  const [form, setForm] = useState({
    label: '', recipient: '', phone: '', address_line1: '', city: '', province: '', postal_code: '', is_default: false,
  })

  const { data: addresses } = useQuery({
    queryKey: ['addresses'],
    queryFn: async () => (await api.get<{ addresses: Address[] }>('/account/addresses')).data.addresses,
  })

  const saveAddress = useMutation({
    mutationFn: async () => {
      if (editing) {
        await api.put(`/account/addresses/${editing.id}`, form)
      } else {
        await api.post('/account/addresses', form)
      }
    },
    onSuccess: () => {
      setEditing(null)
      setForm({ label: '', recipient: '', phone: '', address_line1: '', city: '', province: '', postal_code: '', is_default: false })
      queryClient.invalidateQueries({ queryKey: ['addresses'] })
    },
  })

  const remove = useMutation({
    mutationFn: async (id: string) => api.delete(`/account/addresses/${id}`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['addresses'] }),
  })

  return (
    <div className="space-y-3">
      <h2 className="font-bold">Alamat Tersimpan</h2>
      {addresses?.map((a) => (
        <div key={a.id} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-4 flex items-start justify-between">
          <div>
            <p className="text-sm font-medium">
              {a.label} {a.is_default && <span className="text-xs text-amber-600">(default)</span>}
            </p>
            <p className="text-sm text-gray-600 mt-1">{a.recipient} · {a.phone}</p>
            <p className="text-sm text-gray-500">{a.address_line1}, {a.city}, {a.province} {a.postal_code}</p>
          </div>
          <div className="flex gap-3 text-xs">
            <button
              onClick={() => {
                setEditing(a)
                setForm({ label: a.label, recipient: a.recipient, phone: a.phone, address_line1: a.address_line1, city: a.city, province: a.province, postal_code: a.postal_code, is_default: a.is_default })
              }}
              className="text-blue-600 hover:underline"
            >
              Edit
            </button>
            <button onClick={() => remove.mutate(a.id)} className="text-red-500 hover:underline">
              Hapus
            </button>
          </div>
        </div>
      ))}

      {(editing || !addresses?.length) && (
        <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-5 grid grid-cols-2 gap-3">
          <input placeholder="Label (Home/Kantor)" value={form.label} onChange={(e) => setForm({ ...form, label: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Nama penerima" value={form.recipient} onChange={(e) => setForm({ ...form, recipient: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="No. HP" value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Alamat" value={form.address_line1} onChange={(e) => setForm({ ...form, address_line1: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Kota" value={form.city} onChange={(e) => setForm({ ...form, city: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Provinsi" value={form.province} onChange={(e) => setForm({ ...form, province: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <input placeholder="Kode pos" value={form.postal_code} onChange={(e) => setForm({ ...form, postal_code: e.target.value })} className="px-3 py-2 border rounded-lg text-sm outline-none dark:bg-gray-800" />
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={form.is_default} onChange={(e) => setForm({ ...form, is_default: e.target.checked })} />
            Jadikan default
          </label>
          <div className="flex gap-2 col-span-2">
            <button onClick={() => saveAddress.mutate()} className="px-4 py-2 rounded-lg bg-amber-500 text-white text-sm">
              {editing ? 'Simpan Perubahan' : 'Tambah'}
            </button>
            <button onClick={() => setEditing(null)} className="px-4 py-2 rounded-lg border text-sm">Batal</button>
          </div>
        </div>
      )}
      {!editing && addresses?.length ? (
        <button
          onClick={() => setEditing({ id: '', label: '', recipient: '', phone: '', address_line1: '', city: '', province: '', postal_code: '', country: 'Indonesia', is_default: false, created_at: '', user_id: '' } as Address)}
          className="px-4 py-2 rounded-lg border border-amber-400 text-amber-600 text-sm"
        >
          + Tambah Alamat
        </button>
      ) : null}
    </div>
  )
}
