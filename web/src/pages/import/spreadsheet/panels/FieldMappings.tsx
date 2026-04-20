import { Badge } from '@/components/ui/Badge'
import type { UploadSheet } from '@/api/import-spreadsheet'

interface FieldMappingsProps {
  sheet: UploadSheet | undefined
  mapping: Record<string, number>
  usingDefaultStore: boolean
  onMappingChange: (mapping: Record<string, number>) => void
  onUseDefaultStore: () => void
}

const ROLES: { key: string; label: string }[] = [
  { key: 'date', label: 'Date' },
  { key: 'store', label: 'Store' },
  { key: 'item', label: 'Item' },
  { key: 'qty', label: 'Qty' },
  { key: 'unit', label: 'Unit' },
  { key: 'unit_price', label: 'Unit Price' },
  { key: 'total_price', label: 'Total Price' },
  { key: 'trip_id', label: 'Trip ID' },
  { key: 'notes', label: 'Notes' },
]

const USE_DEFAULT_STORE_SENTINEL = -999

function FieldMappings({
  sheet,
  mapping,
  usingDefaultStore,
  onMappingChange,
  onUseDefaultStore,
}: FieldMappingsProps) {
  const columnCount = sheet?.headers.length ?? 0

  return (
    <div>
      <div className="text-caption font-medium text-neutral-900 mb-2">Map columns to roles</div>
      <div className="space-y-2">
        {ROLES.map((role) => {
          const currentIdx = mapping[role.key]
          const isStore = role.key === 'store'
          const selectValue =
            isStore && usingDefaultStore
              ? String(USE_DEFAULT_STORE_SENTINEL)
              : currentIdx === undefined
                ? ''
                : String(currentIdx)

          return (
            <div
              key={role.key}
              className="grid grid-cols-[140px_1fr] gap-3 items-center"
            >
              <label
                htmlFor={`mapping-${role.key}`}
                className="text-caption font-medium text-neutral-900"
              >
                {role.label}
              </label>
              <select
                id={`mapping-${role.key}`}
                value={selectValue}
                onChange={(e) => {
                  const v = e.target.value
                  if (isStore && v === String(USE_DEFAULT_STORE_SENTINEL)) {
                    onUseDefaultStore()
                    return
                  }
                  if (v === '') {
                    const next = { ...mapping }
                    delete next[role.key]
                    onMappingChange(next)
                    return
                  }
                  onMappingChange({ ...mapping, [role.key]: Number(v) })
                }}
                className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-caption bg-white font-body text-neutral-900 focus:outline-none focus:ring-2 focus:ring-brand"
              >
                <option value="">Choose field…</option>
                {isStore && (
                  <option value={String(USE_DEFAULT_STORE_SENTINEL)}>
                    Use default store (set below)
                  </option>
                )}
                {Array.from({ length: columnCount }).map((_, idx) => {
                  const samples = sheet?.column_samples[String(idx)] ?? []
                  const cov = sheet?.type_coverage[String(idx)]
                  const header = sheet?.headers[idx] ?? `Col ${idx + 1}`
                  const sampleStr = samples.slice(0, 3).filter(Boolean).join(' · ')
                  const covLabel = cov ? pickCoverageLabel(cov) : ''
                  return (
                    <option key={idx} value={String(idx)}>
                      {header}
                      {sampleStr ? ` — ${sampleStr}` : ''}
                      {covLabel ? ` [${covLabel}]` : ''}
                    </option>
                  )
                })}
              </select>
            </div>
          )
        })}
      </div>

      {/* Type-coverage legend rendered for the currently mapped columns. */}
      <div className="mt-3 flex flex-wrap gap-2">
        {ROLES.map((role) => {
          const idx = mapping[role.key]
          if (idx === undefined || idx < 0) return null
          const cov = sheet?.type_coverage[String(idx)]
          if (!cov) return null
          const badge = pickCoverageBadge(cov)
          if (!badge) return null
          return (
            <Badge key={role.key} variant={badge.variant}>
              {role.label}: {badge.label}
            </Badge>
          )
        })}
      </div>
    </div>
  )
}

function pickCoverageLabel(cov: {
  nonblank_pct: number
  date_pct: number
  money_pct: number
  integer_pct: number
  text_pct: number
}): string {
  const entries: [string, number][] = [
    ['money', cov.money_pct],
    ['date', cov.date_pct],
    ['integer', cov.integer_pct],
    ['text', cov.text_pct],
  ]
  entries.sort((a, b) => b[1] - a[1])
  const top = entries[0]
  if (!top) return ''
  const [label, pct] = top
  return `${label} ${Math.round(pct * 100)}%`
}

function pickCoverageBadge(cov: {
  nonblank_pct: number
  date_pct: number
  money_pct: number
  integer_pct: number
  text_pct: number
}): { label: string; variant: 'success' | 'warning' | 'neutral' } | null {
  const label = pickCoverageLabel(cov)
  const pct = Math.max(cov.money_pct, cov.date_pct, cov.integer_pct, cov.text_pct)
  const variant = pct >= 0.95 ? 'success' : pct >= 0.7 ? 'warning' : 'neutral'
  return { label, variant }
}

export default FieldMappings
