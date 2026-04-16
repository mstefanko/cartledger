import { Store } from '@/types'

interface StorePickerProps {
  preferredStoreId: string | null
  stores: Store[]
  onChange: (storeId: string | null) => void
  disabled?: boolean
}

export function StorePicker({ preferredStoreId, stores, onChange, disabled }: StorePickerProps) {
  return (
    <select
      value={preferredStoreId ?? ''}
      onChange={(e) => onChange(e.target.value || null)}
      disabled={disabled}
      className="text-xs border border-border rounded px-2 py-0.5 bg-surface text-content-secondary cursor-pointer disabled:opacity-50"
    >
      <option value="">All stores</option>
      {stores.map((store) => (
        <option key={store.id} value={store.id}>
          {store.nickname ?? store.name}
        </option>
      ))}
    </select>
  )
}
