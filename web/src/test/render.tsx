import { act, type ReactNode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { vi } from 'vitest'

// React 19 refuses to run `act` unless this flag is set up-front.
;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

export interface RenderResult {
  html: () => string
  text: () => string
  find: (selector: string) => HTMLElement | null
  findAll: (selector: string) => HTMLElement[]
  click: (el: HTMLElement | null) => Promise<void>
  rerender: (ui: ReactNode) => Promise<void>
  unmount: () => Promise<void>
}

/**
 * Minimal React 19 render helper for jsdom. The project deliberately has no
 * testing-library dependency and adding one is out of scope, so this wires
 * `react-dom/client` + `act` directly. Each call gets a FRESH container —
 * reusing one across tests makes `createRoot` throw and silently detaches the
 * tree from `document`, which breaks every focus assertion.
 */
export async function render(ui: ReactNode): Promise<RenderResult> {
  const host = document.createElement('div')
  document.body.appendChild(host)
  let root: Root = createRoot(host)

  await act(async () => {
    root.render(ui)
  })

  return {
    html: () => host.innerHTML,
    text: () => host.textContent ?? '',
    find: (selector) => host.querySelector<HTMLElement>(selector),
    findAll: (selector) => Array.from(host.querySelectorAll<HTMLElement>(selector)),
    click: async (el) => {
      if (!el) throw new Error('click: element not found')
      await act(async () => {
        el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }))
      })
    },
    rerender: async (next) => {
      await act(async () => {
        root.render(next)
      })
    },
    unmount: async () => {
      await act(async () => {
        root.unmount()
      })
      host.remove()
    },
  }
}

/** Advance fake timers AND flush the React work they schedule. */
export async function advance(ms: number): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

export function fireKeyDown(target: EventTarget, key: string, init?: KeyboardEventInit) {
  const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init })
  target.dispatchEvent(event)
  return event
}
