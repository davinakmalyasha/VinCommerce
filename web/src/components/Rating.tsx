/**
 * Clamp any numeric rating into the 0..5 integer range.
 *
 * `String.prototype.repeat` throws a RangeError for a negative count, and this
 * component is rendered by every ProductCard. A single aggregate rating
 * outside 0..5 — from a bad import, a legacy row, or a bad API value — used
 * to throw during render and white-screen the entire product grid.
 */
function clampStars(value: unknown): number {
  const n = typeof value === 'number' && Number.isFinite(value) ? value : 0
  return Math.min(5, Math.max(0, Math.round(n)))
}

export function Rating({
  value,
  count,
  size = 'text-sm',
}: {
  value: number
  count?: number
  size?: string
}) {
  const filled = clampStars(value)
  const empty = 5 - filled

  return (
    <span className={`inline-flex items-center gap-1 ${size}`}>
      <span className="text-amber-500" aria-hidden>
        {'★'.repeat(filled)}
        <span className="text-gray-300 dark:text-gray-600">{'★'.repeat(empty)}</span>
      </span>
      {/* The star row is decorative; expose the value once, accessibly. */}
      <span className="sr-only">
        {filled} dari 5 bintang
        {typeof count === 'number' ? `, ${count} ulasan` : ''}
      </span>
      {typeof count === 'number' && (
        <span className="text-gray-500" aria-hidden>
          ({count})
        </span>
      )}
    </span>
  )
}

export default Rating
