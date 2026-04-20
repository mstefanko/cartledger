import type { UploadSheet } from '@/api/import-spreadsheet'

interface SheetPickerProps {
  sheets: UploadSheet[]
  selected: string
  onSelect: (name: string) => void
}

function SheetPicker({ sheets, selected, onSelect }: SheetPickerProps) {
  return (
    <div>
      <label htmlFor="sheet-picker" className="block text-caption font-medium text-neutral-900 mb-1.5">
        Sheet
      </label>
      <select
        id="sheet-picker"
        value={selected}
        onChange={(e) => onSelect(e.target.value)}
        className="w-full max-w-md px-3 py-2.5 rounded-xl border border-neutral-200 text-body font-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
      >
        {sheets.map((s) => (
          <option key={s.name} value={s.name}>
            {s.name} — {s.row_count} rows
          </option>
        ))}
      </select>
    </div>
  )
}

export default SheetPicker
