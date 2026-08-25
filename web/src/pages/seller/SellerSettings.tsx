import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../lib/api'
import { FileUpload } from '../../components/FileUpload'

export function SellerSettings() {
  const queryClient = useQueryClient()
  const { data: store } = useQuery({
    queryKey: ['my-store'],
    queryFn: async () => (await api.get<{ store: { id: string; name: string; description: string; logo_url?: string; banner_url?: string; status: string } }>('/seller/store')).data.store,
  })
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [logoUrl, setLogoUrl] = useState('')
  const [bannerUrl, setBannerUrl] = useState('')

  const { data: kyc } = useQuery({
    queryKey: ['my-kyc'],
    queryFn: async () => {
      try {
        return (await api.get<{ kyc: { status: string; bank_name: string; bank_account: string } }>('/seller/kyc')).data.kyc
      } catch {
        return null
      }
    },
  })
  const [kycForm, setKycForm] = useState({ owner_name: '', id_number: '', bank_name: '', bank_account: '' })

  const saveKyc = useMutation({
    mutationFn: async () => api.post('/seller/kyc', kycForm),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['my-kyc'] })
      alert('KYC terkirim, menunggu review admin')
    },
  })

  const save = useMutation({
    mutationFn: async () =>
      api.put('/seller/store', {
        name: name || undefined,
        description,
        logo_url: logoUrl || undefined,
        banner_url: bannerUrl || undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['my-store'] })
      alert('Toko diperbarui')
    },
  })

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-bold">Pengaturan Toko</h1>

      <div className="bg-white border border-gray-200 rounded-xl p-6 space-y-4">
        <div>
          <label className="text-sm font-medium block mb-1">Nama Toko</label>
          <input
            key={`name-${store?.id ?? 'loading'}`}
            defaultValue={store?.name}
            onChange={(e) => setName(e.target.value)}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
        </div>
        <div>
          <label className="text-sm font-medium block mb-1">Deskripsi</label>
          <textarea
            key={`desc-${store?.id ?? 'loading'}`}
            defaultValue={store?.description}
            onChange={(e) => setDescription(e.target.value)}
            rows={4}
            className="w-full px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label className="text-sm font-medium block mb-1">Logo Toko</label>
            <FileUpload value={logoUrl || store?.logo_url || ''} onChange={setLogoUrl} label="Unggah logo" />
          </div>
          <div>
            <label className="text-sm font-medium block mb-1">Banner Toko</label>
            <FileUpload value={bannerUrl || store?.banner_url || ''} onChange={setBannerUrl} label="Unggah banner" />
          </div>
        </div>
        <button
          onClick={() => save.mutate()}
          disabled={save.isPending}
          className="px-6 py-3 rounded-xl bg-amber-500 text-white font-semibold hover:bg-amber-600 disabled:opacity-50"
        >
          Simpan
        </button>
      </div>

      <div className="bg-white border border-gray-200 rounded-xl p-6">
        <h2 className="font-bold text-sm mb-3">Verifikasi Penjual (KYC)</h2>
        {kyc?.status === 'approved' ? (
          <p className="text-sm text-green-600">
            ✓ KYC disetujui — dana dapat dicairkan ke {kyc.bank_name} {kyc.bank_account}.
          </p>
        ) : kyc?.status === 'pending' ? (
          <p className="text-sm text-amber-600">KYC sedang menunggu review admin ({kyc.status}).</p>
        ) : (
          <p className="text-sm text-gray-600 mb-4">
            Kirim data identitas untuk verifikasi sebelum pencairan dana.
          </p>
        )}
        <div className="grid grid-cols-2 gap-3">
          <input
            placeholder="Nama pemilik"
            value={kycForm.owner_name}
            onChange={(e) => setKycForm({ ...kycForm, owner_name: e.target.value })}
            className="px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <input
            placeholder="No. KTP"
            value={kycForm.id_number}
            onChange={(e) => setKycForm({ ...kycForm, id_number: e.target.value })}
            className="px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <input
            placeholder="Bank"
            value={kycForm.bank_name}
            onChange={(e) => setKycForm({ ...kycForm, bank_name: e.target.value })}
            className="px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
          <input
            placeholder="No. rekening"
            value={kycForm.bank_account}
            onChange={(e) => setKycForm({ ...kycForm, bank_account: e.target.value })}
            className="px-4 py-3 border rounded-xl text-sm outline-none focus:border-amber-400"
          />
        </div>
        <button
          onClick={() => saveKyc.mutate()}
          disabled={saveKyc.isPending}
          className="mt-4 px-6 py-3 rounded-xl bg-gray-900 text-white text-sm hover:bg-gray-800 disabled:opacity-50"
        >
          {saveKyc.isPending ? 'Mengirim...' : 'Kirim KYC'}
        </button>
      </div>
    </div>
  )
}
