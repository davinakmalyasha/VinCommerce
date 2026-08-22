import { useEffect, useState } from 'react'
import { useSearchParams, Link } from 'react-router-dom'
import { api } from '../lib/api'

export function VerifyEmailPage() {
  const [params] = useSearchParams()
  const token = params.get('token') ?? ''
  const [state, setState] = useState<'pending' | 'ok' | 'error'>('pending')
  const [message, setMessage] = useState('')

  useEffect(() => {
    if (!token) {
      setState('error')
      setMessage('Tautan verifikasi tidak valid.')
      return
    }
    api
      .post('/auth/verify-email', { token })
      .then(() => setState('ok'))
      .catch((e: Error) => {
        setState('error')
        setMessage(e.message)
      })
  }, [token])

  return (
    <div className="mx-auto max-w-md px-4 py-20">
      <div className="bg-white border border-gray-200 rounded-2xl p-8 text-center">
        <p className="text-5xl mb-4">{state === 'pending' ? '⏳' : state === 'ok' ? '✅' : '⚠️'}</p>
        <h1 className="text-xl font-bold mb-2">
          {state === 'pending' ? 'Memverifikasi email...' : state === 'ok' ? 'Email terverifikasi!' : 'Verifikasi gagal'}
        </h1>
        {message && <p className="text-sm text-red-600 mb-4">{message}</p>}
        <Link to="/" className="inline-block mt-2 px-5 py-2.5 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600">
          Ke Beranda
        </Link>
      </div>
    </div>
  )
}
