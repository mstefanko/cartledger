import type { CSVOptions } from '@/api/import-spreadsheet'

interface CsvOptionsPanelProps {
  value: CSVOptions
  onChange: (next: CSVOptions) => void
  disabled?: boolean
  hidden?: boolean
}

// Map rune code points <-> select options.
const DELIMITER_OPTIONS: { label: string; code: number }[] = [
  { label: 'Comma (,)', code: 44 },
  { label: 'Tab (\\t)', code: 9 },
  { label: 'Semicolon (;)', code: 59 },
  { label: 'Pipe (|)', code: 124 },
]

function CsvOptionsPanel({ value, onChange, disabled, hidden }: CsvOptionsPanelProps) {
  if (hidden) return null

  return (
    <div>
      <div className="text-caption font-medium text-neutral-900 mb-2">CSV options</div>
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <div>
          <label htmlFor="csv-delim" className="block text-small text-neutral-500 mb-1">Delimiter</label>
          <select
            id="csv-delim"
            disabled={disabled}
            value={value.delimiter}
            onChange={(e) => onChange({ ...value, delimiter: Number(e.target.value) })}
            className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-caption bg-white focus:outline-none focus:ring-2 focus:ring-brand"
          >
            {DELIMITER_OPTIONS.map((o) => (
              <option key={o.code} value={o.code}>{o.label}</option>
            ))}
          </select>
        </div>
        <div className="flex items-end">
          <label className="inline-flex items-center gap-2 text-caption text-neutral-900 cursor-pointer">
            <input
              type="checkbox"
              checked={value.has_header}
              onChange={(e) => onChange({ ...value, has_header: e.target.checked })}
              className="accent-brand"
            />
            First row is a header
          </label>
        </div>
        <div>
          <label htmlFor="csv-skip-start" className="block text-small text-neutral-500 mb-1">Skip rows at start</label>
          <input
            id="csv-skip-start"
            type="number"
            min={0}
            value={value.skip_start}
            onChange={(e) => onChange({ ...value, skip_start: Math.max(0, Number(e.target.value)) })}
            className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-caption bg-white focus:outline-none focus:ring-2 focus:ring-brand"
          />
        </div>
        <div>
          <label htmlFor="csv-skip-end" className="block text-small text-neutral-500 mb-1">Skip rows at end</label>
          <input
            id="csv-skip-end"
            type="number"
            min={0}
            value={value.skip_end}
            onChange={(e) => onChange({ ...value, skip_end: Math.max(0, Number(e.target.value)) })}
            className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-caption bg-white focus:outline-none focus:ring-2 focus:ring-brand"
          />
        </div>
      </div>
    </div>
  )
}

export default CsvOptionsPanel
