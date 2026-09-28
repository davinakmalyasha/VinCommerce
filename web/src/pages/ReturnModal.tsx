import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'
import { Modal } from '../components/Modal'

export function ReturnModal({
  orderId,
  itemId,
  itemName,
  onClose,
}: {
  orderId: string
  itemId: string
  itemName: string
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const [form, setForm] = useState({ issueType: 'return', reason: 'defective', description: '' })

  const submit = useMutation({
    mutationFn: async () =>
      api.post('/returns', {
        order_id: orderId,
        order_item_id: itemId,
        issue_type: form.issueType,
        reason: form.reason,
        description: form.description,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['returns'] })
      onClose()
    },
  })

  return (
    <Modal
      open
      onClose={onClose}
      title="Ajukan Retur / Komplain"
      description={itemName}
      footer={
        <>
          <button
            type="button"
            onClick={() => submit.mutate()}
            disabled={submit.isPending || !form.description.trim()}
            className="flex-1 py-3 rounded-xl bg-amber-500 text-white font-medium disabled:opacity-50"
          >
            {submit.isPending ? 'Mengirim...' : 'Ajukan Retur'}
          </button>
          <button type="button" onClick={onClose} className="px-5 py-3 rounded-xl border text-sm">
            Batal
          </button>
        </>
      }
    >
      <div className="space-y-3">
        <div>
          <label htmlFor="return-issue" className="block text-sm font-medium mb-1">Jenis masalah</label>
          <select
            id="return-issue"
            value={form.issueType}
            onChange={(e) => setForm({ ...form, issueType: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
          >
            <option value="return">↩️ Retur barang</option>
            <option value="item_not_received">📦 Barang tidak sampai</option>
          </select>
        </div>
        {form.issueType === 'return' && (
          <div>
            <label htmlFor="return-reason" className="block text-sm font-medium mb-1">Alasan</label>
            <select
              id="return-reason"
              value={form.reason}
              onChange={(e) => setForm({ ...form, reason: e.target.value })}
              className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
            >
              <option value="defective">Barang rusak/cacat</option>
              <option value="wrong_item">Barang tidak sesuai</option>
              <option value="not_as_described">Tidak sesuai deskripsi</option>
              <option value="other">Lainnya</option>
            </select>
          </div>
        )}
        <div>
          <label htmlFor="return-desc" className="block text-sm font-medium mb-1">Penjelasan</label>
          <textarea
            id="return-desc"
            rows={4}
            placeholder="Jelaskan masalahnya..."
            aria-describedby={submit.isError ? 'return-error' : undefined}
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
          />
        </div>
        {submit.isError && (
          <p id="return-error" role="alert" className="text-sm text-red-600">
            Gagal mengajukan retur: {submit.error.message}
          </p>
        )}
      </div>
    </Modal>
  )
}



