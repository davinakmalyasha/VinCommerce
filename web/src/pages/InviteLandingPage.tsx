import { useParams, Link } from 'react-router-dom'
import { Seo } from '../components/Seo'

/**
 * Public invite landing: explains the referral bonus and deep-links into
 * registration with the referrer's code pre-attached.
 */
export function InviteLandingPage() {
  const { code = '' } = useParams()

  return (
    <>
      <Seo
        title="Kamu Diundang ke VinCommerce!"
        description="Daftar lewat tautan ini dan dapatkan poin bonus untuk pembelian pertamamu."
        path={`/invite/${code}`}
      />
      <div className="mx-auto max-w-lg px-4 py-16">
        <div className="bg-gradient-to-br from-amber-500 to-orange-600 rounded-3xl p-8 text-white text-center shadow-xl">
          <p className="text-5xl mb-4">🎁</p>
          <h1 className="text-2xl font-extrabold mb-2">Kamu Diundang!</h1>
          <p className="text-sm text-amber-100 mb-6">
            Daftar dengan kode undangan di bawah dan kamu berdua sama-sama mendapat
            <b> 500 poin bonus</b> — bisa ditukar diskon saat checkout.
          </p>
          <div className="bg-white/15 rounded-xl px-4 py-3 font-mono text-lg font-bold tracking-widest mb-6">
            {code || '—'}
          </div>
          <Link
            to={`/register?ref=${encodeURIComponent(code)}`}
            className="block w-full py-3.5 rounded-xl bg-white text-orange-600 font-bold hover:bg-amber-50"
          >
            Daftar &amp; Klaim Bonus
          </Link>
          <Link to="/" className="inline-block mt-4 text-xs text-white/80 hover:underline">
            Jelajahi dulu tanpa daftar →
          </Link>
        </div>
        <div className="grid grid-cols-3 gap-3 mt-8 text-center">
          {[
            { icon: '🛡️', label: 'Escrow aman' },
            { icon: '⚡', label: 'Midtrans QRIS' },
            { icon: '🏪', label: 'Ribuan toko' },
          ].map((f) => (
            <div key={f.label} className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-xl p-3">
              <p className="text-xl">{f.icon}</p>
              <p className="text-xs text-gray-500 mt-1">{f.label}</p>
            </div>
          ))}
        </div>
      </div>
    </>
  )
}
