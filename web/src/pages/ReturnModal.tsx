import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../lib/api'

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
  const [form, setForm] = useState({ reason: 'defective', description: '' })

  const submit = useMutation({
    mutationFn: async () =>
      api.post('/returns', {
        order_id: orderId,
        order_item_id: itemId,
        reason: form.reason,
        description: form.description,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['returns'] })
      onClose()
    },
  })

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div className="relative bg-white dark:bg-gray-900 rounded-2xl shadow-xl p-6 w-full max-w-md m-4">
        <h2 className="font-bold text-lg mb-1">Ajukan Retur</h2>
        <p className="text-sm text-gray-500 mb-4">{itemName}</p>
        <div className="space-y-3">
          <select
            value={form.reason}
            onChange={(e) => setForm({ ...form, reason: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
          >
            <option value="defective">Barang rusak/cacat</option>
            <option value="wrong_item">Barang tidak sesuai</option>
            <option value="not_as_described">Tidak sesuai deskripsi</option>
            <option value="other">Lainnya</option>
          </select>
          <textarea
            rows={4}
            placeholder="Jelaskan masalahnya..."
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none dark:bg-gray-800"
          />
          {submit.isError && <p className="text-sm text-red-600">Gagal mengajukan retur.</p>}
          <div className="flex gap-2">
            <button
              onClick={() => submit.mutate()}
              disabled={submit.isPending || !form.description.trim()}
              className="flex-1 py-3 rounded-xl bg-amber-500 text-white font-medium disabled:opacity-50"
            >
              {submit.isPending ? 'Mengirim...' : 'Ajukan Retur'}
            </button>
            <button onClick={onClose} className="px-5 py-3 rounded-xl border text-sm">
              Batal
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}


