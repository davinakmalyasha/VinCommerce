import { safeUrl } from '../lib/sanitize'

/**
 * Thumbnails for the photos a buyer attached to a return claim.
 *
 * The server now rejects anything that is not an uploaded file, but this also
 * runs every value through `safeUrl`: the same component renders on a seller
 * and an admin screen, and a stored URL that slipped past validation must not
 * become a live `javascript:` link on a staff page. A row with no usable URL
 * renders nothing rather than an empty frame.
 */
export function EvidenceThumbs({
  urls,
  label = 'Foto bukti dari pembeli',
}: {
  urls?: string[]
  label?: string
}) {
  const safe = (urls ?? [])
    .map((url) => safeUrl(url))
    .filter((url): url is string => url !== null)
    .slice(0, 3)

  if (safe.length === 0) return null

  return (
    <div className="mt-2">
      <p className="text-xs text-gray-500 mb-1">{label}</p>
      <div className="flex gap-2 flex-wrap">
        {safe.map((url, index) => (
          <a
            key={url}
            href={url}
            target="_blank"
            rel="noopener noreferrer"
            className="block w-16 h-16 rounded-lg overflow-hidden border border-gray-200 bg-gray-50"
          >
            <img
              src={url}
              alt={`${label} ${index + 1}`}
              loading="lazy"
              referrerPolicy="no-referrer"
              className="w-full h-full object-cover"
            />
          </a>
        ))}
      </div>
    </div>
  )
}
