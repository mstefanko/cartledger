import type { Grouping, GroupingStrategy } from '@/api/import-spreadsheet'

interface GroupingPanelProps {
  value: Grouping
  onChange: (next: Grouping) => void
}

const OPTIONS: { value: GroupingStrategy; label: string; hint: string }[] = [
  { value: 'date_store', label: 'Same date + store', hint: 'Rows with the same (store, date) form one receipt.' },
  { value: 'date_store_total', label: 'Same date + store + total', hint: 'Split when subtotals hint at separate receipts.' },
  { value: 'trip_id_column', label: 'Trip ID column', hint: 'Use the column mapped to Trip ID to set receipt boundaries.' },
  { value: 'explicit_marker', label: 'Explicit row marker', hint: 'Advanced — emits one group per explicit range.' },
]

function GroupingPanel({ value, onChange }: GroupingPanelProps) {
  return (
    <div>
      <label htmlFor="grouping-strategy" className="block text-caption font-medium text-neutral-900 mb-1.5">
        Group rows into receipts by
      </label>
      <select
        id="grouping-strategy"
        value={value.strategy}
        onChange={(e) => onChange({ ...value, strategy: e.target.value as GroupingStrategy })}
        className="w-full max-w-md px-3 py-2.5 rounded-xl border border-neutral-200 text-body font-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
      >
        {OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>{o.label}</option>
        ))}
      </select>
      <p className="text-small text-neutral-400 mt-1">
        {OPTIONS.find((o) => o.value === value.strategy)?.hint}
      </p>
    </div>
  )
}

export default GroupingPanel
