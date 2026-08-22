import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'
import { ApiError } from '../lib/api'

export function RegisterPage() {
  const { register } = useSession()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [form, setForm] = useState({ email: '', password: '', full_name: '', ref: params.get('ref') ?? '' })
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [refBonus, setRefBonus] = useState('')

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)
    setError('')
    try {
      await register({ email: form.email, password: form.password, full_name: form.full_name })
      if (form.ref.trim()) {
        try {
          await api.post('/referral/redeem', { code: form.ref.trim() })
          setRefBonus('Kode referral diterapkan — kamu dapat 500 poin!')
        } catch {
          // invalid code: ignore, registration already succeeded
        }
      }
      navigate('/')
    } catch (err) {
      setError((err as ApiError).message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="mx-auto max-w-md px-4 py-16">
      <div className="bg-white border border-gray-200 rounded-2xl p-8">
        <h1 className="text-xl font-bold mb-6 text-center">Buat Akun Baru</h1>
        <form onSubmit={submit} className="space-y-4">
          <input
            required
            placeholder="Nama lengkap"
            value={form.full_name}
            onChange={(e) => setForm({ ...form, full_name: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <input
            type="email"
            required
            placeholder="Email"
            value={form.email}
            onChange={(e) => setForm({ ...form, email: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <input
            type="password"
            required
            minLength={8}
            placeholder="Password (min. 8 karakter)"
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <input
            placeholder="Kode undangan (opsional)"
            value={form.ref}
            onChange={(e) => setForm({ ...form, ref: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          {refBonus && <p className="text-sm text-green-600">{refBonus}</p>}
          {error && <p className="text-sm text-red-600">{error}</p>}
          <button
            disabled={loading}
            className="w-full py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
          >
            {loading ? 'Memproses...' : 'Daftar'}
          </button>
        </form>
        <p className="text-sm text-gray-500 text-center mt-4">
          Sudah punya akun?{' '}
          <Link to="/login" className="text-amber-600 hover:underline">Masuk</Link>
        </p>
      </div>
    </div>
  )
}
