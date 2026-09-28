import {
  useCallback,
  useEffect,
  useId,
  useRef,
  type MouseEvent,
  type ReactNode,
} from 'react'

/**
 * Every focusable thing inside the dialog. `[tabindex]:not([tabindex="-1"])`
 * keeps the panel itself (tabIndex -1, only there so Escape/Tab have a
 * fallback target) out of the cycle.
 */
const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function focusablesIn(panel: HTMLElement): HTMLElement[] {
  return Array.from(panel.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (el) => !el.hidden && el.getAttribute('aria-hidden') !== 'true',
  )
}

export interface ModalProps {
  open: boolean
  onClose: () => void
  /** Rendered as the `<h2>` that labels the dialog. */
  title: ReactNode
  description?: ReactNode
  children?: ReactNode
  footer?: ReactNode
  /**
   * `center`  — centred card (default)
   * `page`    — full-height scrollable sheet, content capped (seller/admin forms)
   * `right`   — edge drawer, full height (cart)
   * `anchored`— popover pinned under the top bar (notification bell)
   */
  variant?: 'center' | 'page' | 'right' | 'anchored'
  panelClassName?: string
  /** Hides the built-in ✕ when the caller renders its own close control. */
  hideCloseButton?: boolean
}

const PANEL: Record<NonNullable<ModalProps['variant']>, string> = {
  center: 'relative bg-white dark:bg-gray-900 rounded-2xl shadow-xl p-6 w-full max-w-md',
  page: 'relative mx-auto my-8 bg-white dark:bg-gray-900 rounded-2xl shadow-xl p-6 w-full max-w-2xl',
  right: 'absolute right-0 top-0 h-full w-full max-w-xs sm:w-96 bg-white dark:bg-gray-900 shadow-xl flex flex-col',
  anchored:
    'absolute right-0 top-12 w-[calc(100vw-2rem)] sm:w-96 max-w-full bg-white dark:bg-gray-900 rounded-xl shadow-xl border border-gray-200 dark:border-gray-700',
}

const SHELL: Record<NonNullable<ModalProps['variant']>, string> = {
  center: 'fixed inset-0 z-50 flex items-center justify-center p-4',
  page: 'fixed inset-0 z-50 overflow-y-auto',
  right: 'fixed inset-0 z-50',
  anchored: 'fixed inset-0 z-50',
}

/**
 * A real dialog: `role="dialog"` + `aria-modal`, labelled by its heading,
 * focus moved in on open and returned to the trigger on close, Escape to
 * dismiss, and Tab cycling inside the panel.
 *
 * The ad-hoc overlays this replaces were plain `<div>`s, so a keyboard or
 * screen-reader user could tab straight out of an open dialog into the page
 * behind it and had no way to dismiss it except by hunting for the ✕.
 */
export function Modal({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  variant = 'center',
  panelClassName = '',
  hideCloseButton = false,
}: ModalProps) {
  const panelRef = useRef<HTMLDivElement>(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose
  const titleId = useId()
  const descId = `${titleId}-desc`

  const requestClose = useCallback(() => closeRef.current(), [])

  // Focus in on open, focus back to the trigger on close.
  useEffect(() => {
    if (!open) return
    const trigger = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const panel = panelRef.current
    const first = panel ? (focusablesIn(panel)[0] ?? panel) : null
    first?.focus()
    return () => {
      if (trigger && document.contains(trigger)) trigger.focus()
    }
  }, [open])

  // Escape closes; Tab cycles focus inside the panel only.
  useEffect(() => {
    if (!open) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        requestClose()
        return
      }
      if (e.key !== 'Tab') return
      const panel = panelRef.current
      if (!panel) return
      const items = focusablesIn(panel)
      const active = document.activeElement
      if (items.length === 0) {
        e.preventDefault()
        panel.focus()
        return
      }
      const first = items[0]
      const last = items[items.length - 1]
      // Focus escaped the dialog (or is on the panel): pull it back in.
      if (!active || !panel.contains(active)) {
        e.preventDefault()
        ;(e.shiftKey ? last : first).focus()
        return
      }
      if (e.shiftKey && active === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && active === last) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', onKeyDown, true)
    return () => document.removeEventListener('keydown', onKeyDown, true)
  }, [open, requestClose])

  // Stop the page behind from scrolling under the dialog.
  useEffect(() => {
    if (!open) return
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previous
    }
  }, [open])

  if (!open) return null

  const onBackdrop = (e: MouseEvent<HTMLDivElement>) => {
    if (e.target === e.currentTarget) requestClose()
  }

  return (
    <div
      className={`${SHELL[variant]} bg-black/40`}
      onMouseDown={onBackdrop}
    >
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descId : undefined}
        tabIndex={-1}
        className={`${PANEL[variant]} outline-none ${panelClassName}`}
      >
        <div className="flex justify-between items-start gap-3 mb-4">
          <div>
            <h2 id={titleId} className="font-bold text-lg">
              {title}
            </h2>
            {description && (
              <p id={descId} className="text-sm text-gray-500">
                {description}
              </p>
            )}
          </div>
          {!hideCloseButton && (
            <button
              type="button"
              aria-label="Tutup"
              onClick={requestClose}
              className="shrink-0 text-gray-400 hover:text-gray-700 dark:hover:text-gray-200"
            >
              ✕
            </button>
          )}
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
        {footer && <div className="mt-4 flex justify-end gap-2">{footer}</div>}
      </div>
    </div>
  )
}
