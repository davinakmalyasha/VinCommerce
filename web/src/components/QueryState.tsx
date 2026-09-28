import type { ReactNode } from 'react'

/**
 * The slice of a react-query result this component needs. Declared
 * structurally so any `useQuery(...)` return value can be passed through
 * without a cast or a generic.
 */
export interface QueryStateLike {
  isLoading: boolean
  isError: boolean
  error: unknown
  refetch: () => unknown
}

export interface QueryStateProps {
  query: QueryStateLike
  /** What is being loaded, in Indonesian, e.g. "produk" or "keranjang". */
  label: string
  /** Optional — omitting it makes QueryState usable as a pure gate. */
  children?: ReactNode
  /** Extra classes for the loading and error blocks. */
  className?: string
}

function errorMessage(error: unknown, label: string): string {
  if (error instanceof Error && error.message) return error.message
  if (typeof error === 'string' && error.trim()) return error
  return `Gagal memuat ${label}.`
}

/**
 * Distinguishes "still loading" from "the request failed".
 *
 * The pages this replaces all rendered `if (!data) return <div>Memuat...</div>`
 * for BOTH states, so a 500, a 401, or a request that never left (offline
 * start) left the visitor staring at a skeleton forever with no way to retry.
 * `ErrorBoundary` cannot help here: a failed request is not a render throw.
 */
export function QueryState({ query, label, children, className = '' }: QueryStateProps) {
  if (query.isLoading) {
    return (
      <div
        role="status"
        aria-busy="true"
        aria-live="polite"
        className={`mx-auto max-w-7xl px-4 py-16 text-center text-gray-500 animate-pulse ${className}`}
      >
        <p>Memuat {label}...</p>
      </div>
    )
  }

  if (query.isError) {
    return (
      <div
        role="alert"
        aria-live="assertive"
        className={`mx-auto max-w-7xl px-4 py-16 text-center ${className}`}
      >
        <p className="text-5xl mb-4" aria-hidden="true">
          📡
        </p>
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Gagal memuat {label}
        </h2>
        <p className="mt-2 text-sm text-gray-600 dark:text-gray-400 break-words">
          {errorMessage(query.error, label)}
        </p>
        <button
          type="button"
          onClick={() => void query.refetch()}
          className="mt-6 rounded-lg bg-amber-500 px-4 py-2 text-sm font-semibold text-white hover:bg-amber-600 disabled:opacity-50"
        >
          Coba lagi
        </button>
      </div>
    )
  }

  return <>{children}</>
}
