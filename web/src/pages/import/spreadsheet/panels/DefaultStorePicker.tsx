import { useQuery } from '@tanstack/react-query'
import { listStores } from '@/api/stores'

interface DefaultStorePickerProps {
  value: string
  onChange: (storeId: string) => void
}

function DefaultStorePicker({ value, onChange }: DefaultStorePickerProps) {
  const storesQuery = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })
  const stores = storesQuery.data ?? []

  return (
    <div className="rounded-xl border border-brand-subtle bg-brand-subtle/30 p-4">
      <label htmlFor="default-store" className="block text-caption font-medium text-neutral-900 mb-1.5">
        Default store
      </label>
      <p className="text-small text-neutral-500 mb-2">
        Stamped on every row because no column is mapped to Store.
      </p>
      <select
        id="default-store"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full max-w-md px-3 py-2.5 rounded-xl border border-neutral-200 text-body font-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
      >
        <option value="">Pick a store…</option>
        {stores.map((s) => (
          <option key={s.id} value={s.id}>{s.name}</option>
        ))}
      </select>
      {!stores.length && !storesQuery.isLoading && (
        <p className="text-small text-neutral-400 mt-2">
          No stores yet. Create one from the Stores menu first.
        </p>
      )}
    </div>
  )
}

export default DefaultStorePicker
