interface ListTotalsBarProps {
  itemCount: number
  grandTotal: number
  hidden?: boolean
}

/**
 * Sticky footer bar showing the item count and estimated grand total for
 * multi-store mode. Phase 5 will render <BatchActionBar> in the same slot
 * when one or more rows are selected — callers should hide this bar then.
 */
export function ListTotalsBar({ itemCount, grandTotal, hidden }: ListTotalsBarProps) {
  if (hidden) return null
  return (
    <div
      className="sticky bottom-0 left-0 right-0 z-30 bg-white border-t border-neutral-200 px-4 py-3 shadow-subtle"
      role="status"
      aria-live="polite"
    >
      <div className="max-w-2xl mx-auto flex items-center justify-between">
        <span className="text-body-medium font-medium text-neutral-900">
          {itemCount} {itemCount === 1 ? 'item' : 'items'}
        </span>
        <span className="font-display text-body-medium font-bold text-brand">
          Est. ${grandTotal.toFixed(2)}
        </span>
      </div>
    </div>
  )
}
