import { useState } from 'react'

/**
 * ShareButton uses the Web Share API when available (mobile), falling back to
 * a WhatsApp deep link + clipboard copy on desktop.
 */
export function ShareButton({
  title,
  url,
  className = '',
}: {
  title: string
  url?: string
  className?: string
}) {
  const [copied, setCopied] = useState(false)
  const shareUrl = url ?? window.location.href

  const share = async () => {
    if (navigator.share) {
      try {
        await navigator.share({ title, text: `${title} — cek di VinCommerce!`, url: shareUrl })
        return
      } catch {
        // user dismissed; fall through to copy
      }
    }
    try {
      await navigator.clipboard.writeText(shareUrl)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      window.open(`https://wa.me/?text=${encodeURIComponent(`${title} — ${shareUrl}`)}`, '_blank')
    }
  }

  return (
    <button
      onClick={share}
      className={className}
      title="Bagikan"
    >
      {copied ? '✓ Tersalin!' : '↗ Bagikan'}
    </button>
  )
}
