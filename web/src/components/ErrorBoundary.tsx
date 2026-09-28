import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  children: ReactNode
  /** Shown in the fallback so a report identifies the failing area. */
  label?: string
  /** When false, re-throw instead of rendering the fallback. */
  isolate?: boolean
}

interface State {
  error: Error | null
}

/**
 * Catches render-time throws so a single bad component cannot white-screen
 * the whole app.
 *
 * This is not hypothetical. `Rating.tsx` computed
 * `'★'.repeat(5 - Math.round(value))`, which throws a RangeError for any
 * value outside 0..5 — and `Rating` is rendered by every `ProductCard`, so
 * one bad aggregate rating took down the entire product grid with no way
 * back except a manual reload.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Surface to the console in development and to any installed reporter in
    // production; never swallow it silently.
    console.error('[ui] render error', {
      label: this.props.label,
      message: error.message,
      stack: error.stack,
      componentStack: info.componentStack,
    })
  }

  private reset = () => {
    this.setState({ error: null })
  }

  render() {
    const { error } = this.state
    if (!error) return <>{this.props.children}</>

    if (this.props.isolate === false) {
      // Let an outer boundary handle it.
      throw error
    }

    return (
      <div
        role="alert"
        aria-live="assertive"
        className="mx-auto max-w-lg px-4 py-16 text-center"
      >
        <div
          aria-hidden="true"
          className="mx-auto mb-4 grid h-12 w-12 place-items-center rounded-full bg-red-100 text-2xl dark:bg-red-950"
        >
          !
        </div>
        <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Terjadi kesalahan di halaman ini
        </h2>
        <p className="mt-2 text-sm text-gray-500 dark:text-gray-400">
          Data kamu tidak hilang. Coba muat ulang bagian ini.
        </p>
        {this.props.label && (
          <p className="mt-1 text-xs text-gray-400">Area: {this.props.label}</p>
        )}
        <details className="mx-auto mt-4 max-w-sm text-left text-xs text-gray-400">
          <summary className="cursor-pointer select-none">Detail teknis</summary>
          <pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-words rounded bg-gray-50 p-3 dark:bg-gray-800">
            {error.message}
          </pre>
        </details>
        <div className="mt-6 flex justify-center gap-3">
          <button
            type="button"
            onClick={this.reset}
            className="rounded-lg bg-amber-500 px-4 py-2 text-sm font-semibold text-gray-900 hover:bg-amber-400 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-600"
          >
            Coba lagi
          </button>
          <button
            type="button"
            onClick={() => window.location.reload()}
            className="rounded-lg border border-gray-300 px-4 py-2 text-sm font-semibold text-gray-700 hover:bg-gray-50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-600 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-gray-800"
          >
            Muat ulang halaman
          </button>
        </div>
      </div>
    )
  }
}

/** Fallback for a lazily-loaded route chunk that is still in flight. */
export function RouteSkeleton({ label }: { label?: string }) {
  return (
    <div
      className="mx-auto max-w-7xl animate-pulse px-4 py-10"
      role="status"
      aria-busy="true"
      aria-live="polite"
    >
      <div className="h-7 w-48 rounded bg-gray-200 dark:bg-gray-700" />
      <div className="mt-4 grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
        {Array.from({ length: 8 }).map((_, i) => (
          <div key={i} className="space-y-2">
            <div className="aspect-square rounded-lg bg-gray-200 dark:bg-gray-700" />
            <div className="h-3 w-4/5 rounded bg-gray-200 dark:bg-gray-700" />
            <div className="h-3 w-1/3 rounded bg-gray-200 dark:bg-gray-700" />
          </div>
        ))}
      </div>
      <span className="sr-only">Memuat {label ?? 'halaman'}...</span>
    </div>
  )
}
