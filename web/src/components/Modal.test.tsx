import { afterEach, describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { Modal } from './Modal'
import { fireKeyDown, render } from '../test/render'

afterEach(() => {
  document.body.style.overflow = ''
})

const body = (
  <>
    <button type="button">triggers</button>
    <Modal open onClose={() => {}} title="Laporkan Produk" description="Pilih alasan">
      <input aria-label="Alasan" />
      <button type="button">Kirim</button>
    </Modal>
  </>
)

describe('Modal', () => {
  it('exposes dialog semantics and is labelled by its heading', async () => {
    const view = await render(body)
    const dialog = view.find('[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(dialog?.getAttribute('aria-modal')).toBe('true')

    const labelledBy = dialog?.getAttribute('aria-labelledby')
    expect(labelledBy).toBeTruthy()
    const heading = view.find(`#${CSS.escape(labelledBy!)}`)
    expect(heading?.textContent).toBe('Laporkan Produk')

    const descId = dialog?.getAttribute('aria-describedby')
    expect(descId).toBeTruthy()
    expect(view.find(`#${CSS.escape(descId!)}`)?.textContent).toBe('Pilih alasan')
    await view.unmount()
  })

  it('renders nothing when closed', async () => {
    const view = await render(
      <Modal open={false} onClose={() => {}} title="Tutup">
        <p>isi</p>
      </Modal>,
    )
    expect(view.find('[role="dialog"]')).toBeNull()
    await view.unmount()
  })

  it('moves focus inside on open and restores it to the trigger on close', async () => {
    const Harness = () => {
      const [open, setOpen] = useState(false)
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            triggers
          </button>
          <Modal open={open} onClose={() => setOpen(false)} title="Judul">
            <button type="button">dalam</button>
          </Modal>
        </>
      )
    }

    const view = await render(<Harness />)
    const trigger = view.findAll('button')[0]
    // A real click puts focus on the trigger; dispatchEvent alone does not.
    trigger.focus()
    expect(document.activeElement).toBe(trigger)

    await view.click(trigger)
    const dialog = view.find('[role="dialog"]')
    expect(dialog).not.toBeNull()
    expect(dialog?.contains(document.activeElement)).toBe(true)

    await view.click(view.find('button[aria-label="Tutup"]'))
    expect(document.activeElement).toBe(trigger)
    await view.unmount()
  })

  it('closes on Escape', async () => {
    const onClose = vi.fn()
    const view = await render(
      <Modal open onClose={onClose} title="Judul">
        <p>isi</p>
      </Modal>,
    )
    fireKeyDown(document, 'Escape')
    expect(onClose).toHaveBeenCalledTimes(1)
    await view.unmount()
  })

  it('traps Tab: the last focusable wraps to the first', async () => {
    const view = await render(
      <Modal open onClose={() => {}} title="Judul" hideCloseButton>
        <button type="button">satu</button>
        <button type="button">dua</button>
      </Modal>,
    )
    const [satu, dua] = view.findAll('button')
    dua.focus()
    const event = fireKeyDown(document, 'Tab')
    expect(event.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(satu)
    await view.unmount()
  })

  it('traps Shift+Tab: the first focusable wraps to the last', async () => {
    const view = await render(
      <Modal open onClose={() => {}} title="Judul" hideCloseButton>
        <button type="button">satu</button>
        <button type="button">dua</button>
      </Modal>,
    )
    const [satu, dua] = view.findAll('button')
    satu.focus()
    const event = fireKeyDown(document, 'Tab', { shiftKey: true })
    expect(event.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(dua)
    await view.unmount()
  })

  it('pulls focus back when it has escaped the panel', async () => {
    const outside = document.createElement('button')
    document.body.appendChild(outside)
    outside.focus()

    const view = await render(
      <Modal open onClose={() => {}} title="Judul" hideCloseButton>
        <button type="button">satu</button>
        <button type="button">dua</button>
      </Modal>,
    )
    outside.focus()
    fireKeyDown(document, 'Tab')
    expect(document.activeElement).not.toBe(outside)
    expect(view.find('[role="dialog"]')?.contains(document.activeElement)).toBe(true)
    await view.unmount()
  })

  it('locks background scrolling while open and restores it after', async () => {
    const view = await render(
      <Modal open onClose={() => {}} title="Judul">
        <p>isi</p>
      </Modal>,
    )
    expect(document.body.style.overflow).toBe('hidden')
    await view.unmount()
    expect(document.body.style.overflow).not.toBe('hidden')
  })

  it('renders the footer slot and honours the variant class', async () => {
    const view = await render(
      <Modal open onClose={() => {}} title="Judul" variant="right" footer={<button type="button">Aksi</button>}>
        <p>isi</p>
      </Modal>,
    )
    expect(view.find('[role="dialog"]')?.className).toContain('max-w-xs')
    expect(view.text()).toContain('Aksi')
    await view.unmount()
  })
})
