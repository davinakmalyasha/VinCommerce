import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useSession } from '../stores/session'
import { ApiError } from '../lib/api'

export function LoginPage() {
  const { login } = useSession()
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)
    setError('')
    try {
      await login(email, password, totp || undefined)
      navigate('/')
    } catch (err) {
      const apiErr = err as ApiError
      setError(apiErr.code === 'TWO_FACTOR_REQUIRED' ? 'Kode 2FA salah' : apiErr.message)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="mx-auto max-w-md px-4 py-16">
      <div className="bg-white border border-gray-200 rounded-2xl p-8">
        <h1 className="text-xl font-bold mb-6 text-center">Masuk ke VinCommerce</h1>
        <form onSubmit={submit} className="space-y-4">
          <input
            type="email"
            required
            placeholder="Email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <input
            type="password"
            required
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <input
            placeholder="Kode 2FA (jika aktif)"
            value={totp}
            onChange={(e) => setTotp(e.target.value)}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          {error && <p className="text-sm text-red-600">{error}</p>}
          <button
            disabled={loading}
            className="w-full py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
          >
            {loading ? 'Memproses...' : 'Masuk'}
          </button>
        </form>
        <p className="text-sm text-gray-500 text-center mt-4">
          Belum punya akun?{' '}
          <Link to="/register" className="text-amber-600 hover:underline">Daftar</Link>
        </p>
        <p className="text-xs text-gray-400 text-center mt-6">
          Akun demo: buyer.sample@vincommerce.com / BuyerPass123! · admin@vincommerce.com / AdminPass123!
        </p>
      </div>
    </div>
  )
}
