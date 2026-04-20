import type { DateFormat } from '@/api/import-spreadsheet'

interface DateFormatSelectProps {
  value: DateFormat
  onChange: (value: DateFormat) => void
}

const OPTIONS: { value: DateFormat; label: string; sample: string }[] = [
  { value: 'YYYY-MM-DD', label: 'YYYY-MM-DD', sample: '2026-03-12' },
  { value: 'MM/DD/YYYY', label: 'MM/DD/YYYY', sample: '03/12/2026' },
  { value: 'DD/MM/YYYY', label: 'DD/MM/YYYY', sample: '12/03/2026' },
  { value: 'YYYY/MM/DD', label: 'YYYY/MM/DD', sample: '2026/03/12' },
  { value: 'excel_serial', label: 'Excel serial', sample: '45732' },
  { value: 'ISO-8601', label: 'ISO-8601 timestamp', sample: '2026-03-12T14:22:00Z' },
]

function DateFormatSelect({ value, onChange }: DateFormatSelectProps) {
  return (
    <div>
      <label htmlFor="date-format" className="block text-caption font-medium text-neutral-900 mb-1.5">
        Date format
      </label>
      <select
        id="date-format"
        value={value}
        onChange={(e) => onChange(e.target.value as DateFormat)}
        className="w-full max-w-md px-3 py-2.5 rounded-xl border border-neutral-200 text-body font-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
      >
        {OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label} — {o.sample}
          </option>
        ))}
      </select>
    </div>
  )
}

export default DateFormatSelect
