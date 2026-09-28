import type { Product } from '../types'

/**
 * Shared localStorage-backed compare list.
 *
 * ComparePage stored items under `vc_compare` and ProductCard wrote to the
 * same key, but nothing ever navigated to /compare, so the route was
 * unreachable. The list now lives here so the writer (ProductCard), the reader
 * (ComparePage) and the floating CompareBar all agree on one shape, and every
 * mutation notifies subscribers.
 */

export const COMPARE_KEY = 'vc_compare'
export const COMPARE_MAX = 4

/** A product as stored for comparison: the full snapshot plus a timestamp. */
export type CompareItem = Product & { compared_at: number }

const listeners = new Set<(count: number) => void>()

function notify() {
  const count = readCompareCount()
  for (const fn of listeners) fn(count)
}

export function readCompare(): CompareItem[] {
  try {
    return JSON.parse(localStorage.getItem(COMPARE_KEY) ?? '[]') as CompareItem[]
  } catch {
    return []
  }
}

export function readCompareCount(): number {
  return readCompare().length
}

export function compareIds(): string[] {
  return readCompare().map((p) => p.id)
}

export function writeCompare(items: CompareItem[]): void {
  localStorage.setItem(COMPARE_KEY, JSON.stringify(items))
  notify()
}

export function addCompareItem(product: Product): void {
  const list = readCompare()
  if (list.some((p) => p.id === product.id) || list.length >= COMPARE_MAX) return
  writeCompare([...list, { ...product, compared_at: Date.now() }])
}

export function removeCompareItem(id: string): void {
  writeCompare(readCompare().filter((p) => p.id !== id))
}

export function clearCompare(): void {
  writeCompare([])
}

/** Subscribe to compare-list changes made anywhere in the app (or another tab). */
export function subscribeCompare(fn: (count: number) => void): () => void {
  listeners.add(fn)
  const onStorage = (e: StorageEvent) => {
    if (e.key === COMPARE_KEY) fn(readCompareCount())
  }
  window.addEventListener('storage', onStorage)
  return () => {
    listeners.delete(fn)
    window.removeEventListener('storage', onStorage)
  }
}
