interface IncrementalDateFilterProps {
  value: string
  onChange: (value: string) => void
}

function IncrementalDateFilter({ value, onChange }: IncrementalDateFilterProps) {
  return (
    <div>
      <label htmlFor="since-date" className="block text-caption font-medium text-neutral-900 mb-1.5">
        Only import rows since
      </label>
      <div className="flex items-center gap-3">
        <input
          id="since-date"
          type="date"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          className="max-w-xs px-3 py-2.5 rounded-xl border border-neutral-200 text-body font-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
        />
        {value && (
          <button
            type="button"
            onClick={() => onChange('')}
            className="text-caption text-neutral-500 hover:text-neutral-900 cursor-pointer"
          >
            Clear
          </button>
        )}
      </div>
      <p className="text-small text-neutral-400 mt-1">
        Rows with a parsed date before this are dropped. Leave blank to import every row.
      </p>
    </div>
  )
}

export default IncrementalDateFilter
