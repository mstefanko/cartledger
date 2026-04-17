import type { Store } from '@/types'

interface StoreAssignDropdownProps {
  value: string | null
  onChange: (storeId: string | null) => void
  stores: Store[]
  // If provided, the matching option gets a "Best" marker appended to its label.
  // Phase 3 defers per-store price hints — only the single cheapest store is marked.
  cheapestStoreId?: string | null
  disabled?: boolean
}

export function StoreAssignDropdown({
  value,
  onChange,
  stores,
  cheapestStoreId,
  disabled,
}: StoreAssignDropdownProps) {
  return (
    <select
      value={value ?? ''}
      onChange={(e) => {
        // Empty string = Unassigned. Server treats "" as NULL clear.
        const next = e.target.value
        onChange(next === '' ? null : next)
      }}
      onClick={(e) => e.stopPropagation()}
      disabled={disabled}
      aria-label="Assign store"
      className="text-xs border border-neutral-200 rounded-lg px-2 py-1 bg-white text-neutral-900 cursor-pointer disabled:opacity-50 focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand max-w-[140px] truncate"
    >
      <option value="">Unassigned</option>
      {stores.map((store) => {
        const label = store.nickname ?? store.name
        const isBest = cheapestStoreId != null && store.id === cheapestStoreId
        return (
          <option key={store.id} value={store.id}>
            {isBest ? `${label} — Best` : label}
          </option>
        )
      })}
    </select>
  )
}
