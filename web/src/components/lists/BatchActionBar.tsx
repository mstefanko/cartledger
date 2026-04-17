import { useState } from 'react'
import type { Store } from '@/types'

interface BatchActionBarProps {
  count: number
  stores: Store[]
  onAssignStore: (storeId: string | null) => void
  onDelete: () => void
  onCancel: () => void
}

/**
 * Sticky footer that takes over the ListTotalsBar slot while the selection
 * set is non-empty. Shows "N selected" on the left and the three primary
 * batch actions on the right. Uses the same sticky/max-width/shadow pattern
 * as ListTotalsBar so the two swap cleanly.
 *
 * Assign-store uses a native <select> (matches StoreAssignDropdown) so it
 * inherits the same "Unassigned" + store-list semantics without re-building
 * a popover from scratch. Selecting an option fires onAssignStore once and
 * the bar resets its local select value back to empty — the parent is
 * expected to clear selection on success.
 */
export function BatchActionBar({
  count,
  stores,
  onAssignStore,
  onDelete,
  onCancel,
}: BatchActionBarProps) {
  // Controlled select so we can reset it back to "" after each assignment —
  // otherwise picking the same store twice in a row wouldn't re-fire onChange.
  const [assignValue, setAssignValue] = useState<string>('')

  return (
    <div
      className="sticky bottom-0 left-0 right-0 z-30 bg-brand-subtle border-t border-brand/30 px-4 py-3 shadow-subtle"
      role="toolbar"
      aria-label="Batch actions"
    >
      <div className="max-w-2xl mx-auto flex items-center justify-between gap-3">
        <span className="text-body-medium font-medium text-brand whitespace-nowrap">
          {count} selected
        </span>
        <div className="flex items-center gap-2">
          <select
            value={assignValue}
            onChange={(e) => {
              const next = e.target.value
              setAssignValue('') // reset so same store can be picked again
              if (next === '__unassigned__') {
                onAssignStore(null)
              } else if (next !== '') {
                onAssignStore(next)
              }
            }}
            aria-label="Assign store to selected items"
            className="text-small border border-brand/40 rounded-lg px-2 py-1.5 bg-white text-neutral-900 cursor-pointer focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand max-w-[160px] truncate"
          >
            <option value="" disabled>
              Assign store…
            </option>
            <option value="__unassigned__">Unassigned</option>
            {stores.map((store) => (
              <option key={store.id} value={store.id}>
                {store.nickname ?? store.name}
              </option>
            ))}
          </select>
          <button
            type="button"
            onClick={onDelete}
            className="text-small font-semibold text-expensive px-3 py-1.5 rounded-lg border border-expensive/30 bg-white hover:bg-expensive/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-expensive"
          >
            Delete
          </button>
          <button
            type="button"
            onClick={onCancel}
            className="text-small font-medium text-neutral-900 px-3 py-1.5 rounded-lg border border-neutral-200 bg-white hover:bg-neutral-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand"
          >
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}
