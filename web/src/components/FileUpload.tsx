import { useState } from 'react'
import { api } from '../lib/api'

export function FileUpload({
  value,
  onChange,
  label = 'Unggah gambar',
  id,
}: {
  value: string
  onChange: (url: string) => void
  label?: string
  id?: string
}) {
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState('')

  const handle = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    setUploading(true)
    setError('')
    try {
      const form = new FormData()
      form.append('file', file)
      const res = await api.post<{ url: string }>('/media/upload', form, {
        headers: { 'Content-Type': 'multipart/form-data' },
      })
      onChange(res.data.url)
    } catch {
      // An alert() blocks the whole UI thread and a screen reader never
      // announces it; a live region does both.
      setError('Gagal mengunggah file')
    } finally {
      setUploading(false)
      e.target.value = ''
    }
  }

  return (
    <div className="flex items-center gap-3 flex-wrap">
      {value && (
        <img src={value} alt="" className="w-16 h-16 rounded-lg object-cover bg-gray-100 border border-gray-200" />
      )}
      <label htmlFor={id} className="px-4 py-2 rounded-lg border border-gray-300 text-sm cursor-pointer hover:bg-gray-50">
        {uploading ? 'Mengunggah...' : value ? 'Ganti' : label}
        <input id={id} type="file" accept="image/*" onChange={handle} className="hidden" />
      </label>
      {value && (
        <button type="button" onClick={() => onChange('')} className="text-xs text-red-500 hover:underline">
          Hapus
        </button>
      )}
      {error && (
        <p role="alert" className="w-full text-xs text-red-600">{error}</p>
      )}
    </div>
  )
}
