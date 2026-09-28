import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { useSession } from '../../stores/session'

export function VerifyEmailBanner() {
  const { user } = useSession()
  const [hidden, setHidden] = useState(() => localStorage.getItem('vc_verify_dismissed') === '1')
  const [sent, setSent] = useState(false)

  const resend = useMutation({
    mutationFn: async () => api.post('/auth/verify-email/request'),
    onSuccess: () => setSent(true),
  })

  if (!user || user.email_verified_at || hidden) return null

  return (
    <div className="bg-amber-100 dark:bg-amber-900/40 border-b border-amber-200 dark:border-amber-800">
      <div className="mx-auto max-w-7xl px-4 py-2 flex items-center justify-between gap-4 text-sm">
        <p className="text-amber-800 dark:text-amber-200">
          {sent
            ? 'Email verifikasi telah dikirim. Cek inbox kamu (Mailpit: localhost:8025).'
            : 'Verifikasi email kamu untuk mengamankan akun.'}
        </p>
        <div className="flex items-center gap-3 shrink-0">
          {!sent && (
            <button type="button"
              onClick={() => resend.mutate()}
              disabled={resend.isPending}
              className="text-amber-800 dark:text-amber-200 underline hover:no-underline disabled:opacity-50"
            >
              {resend.isPending ? 'Mengirim...' : 'Kirim ulang'}
            </button>
          )}
          <button type="button"
            onClick={() => {
              localStorage.setItem('vc_verify_dismissed', '1')
              setHidden(true)
            }}
            className="text-amber-800 dark:text-amber-200 hover:opacity-70"
            aria-label="Tutup"
          >
            ✕
          </button>
        </div>
      </div>
    </div>
  )
}
