import { useState } from 'react'
import { useSearchParams, Link, useNavigate } from 'react-router-dom'
import { api } from '../lib/api'

export function ResetPasswordPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const token = params.get('token') ?? ''
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  const submit = async () => {
    if (password.length < 8) {
      setError('Kata sandi minimal 8 karakter.')
      return
    }
    if (password !== confirm) {
      setError('Konfirmasi kata sandi tidak cocok.')
      return
    }
    if (submitting) return
    setSubmitting(true)
    try {
      await api.post('/auth/password/reset', { token, new_password: password })
      setDone(true)
      setTimeout(() => navigate('/login'), 2500)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="mx-auto max-w-md px-4 py-20">
      <div className="bg-white border border-gray-200 rounded-2xl p-8">
        {done ? (
          <div className="text-center">
            <p className="text-5xl mb-4">✅</p>
            <h1 className="text-xl font-bold mb-2">Kata sandi diperbarui</h1>
            <p className="text-sm text-gray-500">Mengalihkan ke halaman masuk...</p>
          </div>
        ) : token ? (
          <>
            <h1 className="text-xl font-bold mb-1">Atur kata sandi baru</h1>
            <p className="text-sm text-gray-500 mb-5">Masukkan kata sandi baru untuk akunmu.</p>
            <div className="space-y-3">
              <input
                type="password"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Kata sandi baru (min. 8 karakter)"
                className="w-full px-4 py-2.5 border rounded-lg text-sm outline-none focus:border-amber-400"
              />
              <input
                type="password"
                autoComplete="new-password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                placeholder="Ulangi kata sandi baru"
                className="w-full px-4 py-2.5 border rounded-lg text-sm outline-none focus:border-amber-400"
              />
              {error && <p className="text-sm text-red-600">{error}</p>}
              <button
                onClick={submit}
                disabled={submitting}
                className="w-full py-2.5 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
              >
                {submitting ? 'Menyimpan...' : 'Simpan Kata Sandi'}
              </button>
            </div>
          </>
        ) : (
          <div className="text-center">
            <p className="text-5xl mb-4">⚠️</p>
            <h1 className="text-xl font-bold mb-2">Tautan tidak valid</h1>
            <p className="text-sm text-gray-500 mb-4">Minta tautan reset baru dari halaman masuk.</p>
            <Link to="/login" className="inline-block px-5 py-2.5 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600">
              Ke Halaman Masuk
            </Link>
          </div>
        )}
      </div>
    </div>
  )
}
