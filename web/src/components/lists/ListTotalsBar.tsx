interface ListTotalsBarProps {
  itemCount: number
  grandTotal: number
  hidden?: boolean
  potentialSavings?: number
  canOptimize?: boolean
  onOptimize?: () => void
  optimizing?: boolean
  /**
   * True when all non-best items are in the "unknown" badge state (zero
   * confirmed-worse rows). In that case we hide the "Optimize price" CTA
   * and swap the copy to a neutral notice — we can't honestly claim there
   * are savings available if we have no prices at the selected store to
   * compare against.
   */
  hasUnknownOnly?: boolean
}

/**
 * Sticky footer bar showing the item count and estimated grand total for
 * multi-store mode. Phase 5 will render <BatchActionBar> in the same slot
 * when one or more rows are selected — callers should hide this bar then.
 *
 * When `canOptimize` is true and `potentialSavings > 0`, a second line
 * nudges the user with a "save $X.XX" CTA wired to `onOptimize`. When the
 * list is already at its best pricing, the same slot shows a subtle
 * "Estimate is the best possible" confirmation.
 */
export function ListTotalsBar({
  itemCount,
  grandTotal,
  hidden,
  potentialSavings,
  canOptimize,
  onOptimize,
  optimizing,
  hasUnknownOnly,
}: ListTotalsBarProps) {
  if (hidden) return null
  const showSavingsCta = canOptimize && onOptimize
  // Show the "no price history at selected store" notice only when there's
  // nothing to optimize AND the reason is missing data, not a genuinely
  // best-case list. Keeps the existing "Estimate is the best possible"
  // line for the all-best case.
  const showUnknownNotice = !showSavingsCta && hasUnknownOnly
  const showBestConfirmation =
    !showSavingsCta &&
    !showUnknownNotice &&
    itemCount > 0 &&
    potentialSavings !== undefined
  return (
    <div
      className="sticky bottom-0 left-0 right-0 z-30 bg-white border-t border-neutral-200 px-4 py-3 shadow-subtle"
      role="status"
      aria-live="polite"
    >
      <div className="max-w-2xl mx-auto flex flex-col gap-1">
        <div className="flex items-center justify-between">
          <span className="text-body-medium font-medium text-neutral-900">
            {itemCount} {itemCount === 1 ? 'item' : 'items'}
          </span>
          <span className="font-display text-body-medium font-bold text-brand">
            Est. ${grandTotal.toFixed(2)}
          </span>
        </div>
        {showSavingsCta && (
          <div className="flex items-center justify-between gap-2">
            <span className="text-small text-neutral-600">
              {(potentialSavings ?? 0) > 0
                ? `Save $${potentialSavings!.toFixed(2)} by shopping at different stores.`
                : 'Some items are cheaper at other stores.'}
            </span>
            <button
              type="button"
              onClick={onOptimize}
              disabled={optimizing}
              className="text-small font-medium text-brand hover:underline disabled:opacity-50 disabled:cursor-not-allowed shrink-0"
            >
              {optimizing ? 'Optimizing…' : 'Optimize price'}
            </button>
          </div>
        )}
        {showUnknownNotice && (
          <span className="text-small text-neutral-500">
            Some items have no price history at the selected store.
          </span>
        )}
        {showBestConfirmation && (
          <span className="text-small text-neutral-500">
            Estimate is the best possible.
          </span>
        )}
      </div>
    </div>
  )
}
