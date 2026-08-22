export function Rating({ value, count, size = 'text-sm' }: { value: number; count?: number; size?: string }) {
  return (
    <span className={`inline-flex items-center gap-1 ${size}`}>
      <span className="text-amber-500" aria-hidden>
        {'★'.repeat(Math.round(value))}
        <span className="text-gray-300">{'★'.repeat(5 - Math.round(value))}</span>
      </span>
      {typeof count === 'number' && <span className="text-gray-500">({count})</span>}
    </span>
  )
}
