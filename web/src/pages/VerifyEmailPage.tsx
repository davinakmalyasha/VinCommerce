import { useEffect, useState } from 'react'
import { useSearchParams, Link } from 'react-router-dom'
import { api } from '../lib/api'
import { useSession } from '../stores/session'

export function VerifyEmailPage() {
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''
  const { restore } = useSession()
  const [state, setState] = useState<'pending' | 'ok' | 'error'>('pending')
  const [message, setMessage] = useState('')

  useEffect(() => {
    if (!token) {
      setState('error')
      setMessage('Tautan verifikasi tidak valid.')
      return
    }
    // A verification token is single-use, so the second call to
    // /auth/verify-email legitimately fails. Without a cancellation check the
    // StrictMode double-mount (and the back button) surfaced that expected
    // failure as "Verifikasi gagal" even though the address IS verified.
    const controller = new AbortController()
    api
      .post('/auth/verify-email', { token }, { signal: controller.signal })
      .then(async () => {
        setState('ok')
        // Pull the fresh profile so the "verifikasi email" banner disappears
        // without a manual reload.
        await restore().catch(() => {})
      })
      .catch((e: Error) => {
        if (controller.signal.aborted || e.name === 'CanceledError') return
        setState('error')
        setMessage(e.message)
      })
    return () => controller.abort()
  }, [token, restore])

  return (
    <div className="mx-auto max-w-md px-4 py-20">
      <div className="bg-white border border-gray-200 rounded-2xl p-8 text-center">
        <p className="text-5xl mb-4" aria-hidden="true">{state === 'pending' ? '⏳' : state === 'ok' ? '✅' : '⚠️'}</p>
        <h1 className="text-xl font-bold mb-2" aria-live="polite">
          {state === 'pending' ? 'Memverifikasi email...' : state === 'ok' ? 'Email terverifikasi!' : 'Verifikasi gagal'}
        </h1>
        {message && <p role="alert" className="text-sm text-red-600 mb-4">{message}</p>}
        {state === 'ok' && <p className="text-sm text-gray-500 mb-4">Email kamu sudah terverifikasi.</p>}
        <Link to="/" className="inline-block mt-2 px-5 py-2.5 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600">
          Ke Beranda
        </Link>
      </div>
    </div>
  )
}
