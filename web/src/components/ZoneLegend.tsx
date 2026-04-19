import { useEffect, useRef, useState, useCallback } from 'react'
import { Info } from 'lucide-react'
import { ZONES, ZONE_COLOR, ZONE_LABEL, ZONE_DESCRIPTION } from '@/lib/zones'

// ZoneLegend
//
// Info-button popover listing the four storage zones with their swatch colors,
// labels, and descriptions. Mounted in the ShoppingListPage toolbar next to the
// multi-store toggle so users can decode the colored left-border on each row.
//
// Pattern choice: the codebase has no dedicated popover primitive (Radix /
// Headless UI are not deps — see web/package.json). Modal.tsx uses a
// useState + ref + outside-click + Escape pattern; this component mirrors that
// shape, but anchors the panel to the trigger instead of rendering a full
// overlay — a popover, not a dialog, because the content is non-blocking.
//
// Accessibility:
//   - trigger: button with aria-label and aria-expanded
//   - panel: role="dialog" with aria-label, so screen readers announce it
//   - Escape closes; outside-click closes; Tab cycles within the document
//     naturally (no focus trap — popover is non-blocking by design)
function ZoneLegend() {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)

  const close = useCallback(() => setOpen(false), [])

  useEffect(() => {
    if (!open) return
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        close()
        // Return focus to trigger for keyboard users.
        triggerRef.current?.focus()
      }
    }
    function onClickOutside(e: MouseEvent) {
      if (!rootRef.current) return
      if (!rootRef.current.contains(e.target as Node)) {
        close()
      }
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('mousedown', onClickOutside)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('mousedown', onClickOutside)
    }
  }, [open, close])

  return (
    <div ref={rootRef} className="relative inline-block">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-label="Show zone legend"
        aria-expanded={open}
        aria-haspopup="dialog"
        className="inline-flex items-center justify-center w-8 h-8 rounded-md text-neutral-500 hover:text-neutral-900 hover:bg-neutral-50 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand transition-colors"
      >
        <Info className="w-4 h-4" aria-hidden="true" />
      </button>

      {open && (
        <div
          role="dialog"
          aria-label="Shopping zones legend"
          className="absolute right-0 top-full mt-2 z-30 w-72 bg-white border border-neutral-200 rounded-xl shadow-subtle p-3"
        >
          <p className="text-small font-semibold text-neutral-900 mb-2">
            Shopping zones
          </p>
          <ul className="flex flex-col gap-2">
            {ZONES.map((zone) => (
              <li key={zone} className="flex items-start gap-2">
                <span
                  aria-hidden="true"
                  className="shrink-0 mt-0.5 w-3 h-3 rounded-sm"
                  style={{ backgroundColor: ZONE_COLOR[zone] }}
                />
                <div className="min-w-0">
                  <p className="text-caption font-medium text-neutral-900 leading-tight">
                    {ZONE_LABEL[zone]}
                  </p>
                  <p className="text-small text-neutral-500 leading-snug">
                    {ZONE_DESCRIPTION[zone]}
                  </p>
                </div>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

export { ZoneLegend }
