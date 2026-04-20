import { useState } from 'react'
import type { UnitOptions } from '@/api/import-spreadsheet'

interface UnitPriceOptionsPanelProps {
  value: UnitOptions
  onChange: (next: UnitOptions) => void
}

function UnitPriceOptionsPanel({ value, onChange }: UnitPriceOptionsPanelProps) {
  const [open, setOpen] = useState(false)
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="w-full flex items-center justify-between text-caption font-medium text-neutral-900 cursor-pointer"
        aria-expanded={open}
      >
        <span>Unit &amp; price options</span>
        <span className="text-small text-neutral-400">{open ? 'Hide' : 'Show'}</span>
      </button>

      {open && (
        <div className="mt-3 grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label htmlFor="unit-multiplier" className="block text-small text-neutral-500 mb-1">
              Price multiplier
            </label>
            <input
              id="unit-multiplier"
              type="number"
              step="0.01"
              value={value.price_multiplier}
              onChange={(e) => onChange({ ...value, price_multiplier: Number(e.target.value) || 0 })}
              className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-caption bg-white focus:outline-none focus:ring-2 focus:ring-brand"
            />
            <p className="text-small text-neutral-400 mt-1">
              Use 0.01 when prices are stored as integer cents.
            </p>
          </div>
          <div>
            <label htmlFor="unit-strategy" className="block text-small text-neutral-500 mb-1">
              Total / unit-price strategy
            </label>
            <select
              id="unit-strategy"
              value={value.total_unit_price_strategy}
              onChange={(e) => onChange({ ...value, total_unit_price_strategy: e.target.value })}
              className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-caption bg-white focus:outline-none focus:ring-2 focus:ring-brand"
            >
              <option value="trust_total">Trust total price</option>
              <option value="compute_missing">Compute missing side</option>
              <option value="ignore_qty">Ignore quantity</option>
            </select>
          </div>
          <div>
            <label htmlFor="unit-neg" className="block text-small text-neutral-500 mb-1">
              Negative prices
            </label>
            <select
              id="unit-neg"
              value={value.negative_handling}
              onChange={(e) => onChange({ ...value, negative_handling: e.target.value })}
              className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-caption bg-white focus:outline-none focus:ring-2 focus:ring-brand"
            >
              <option value="discount">Apply as discount</option>
              <option value="refund">Treat as refund</option>
              <option value="reject">Reject row</option>
            </select>
          </div>
          <div>
            <label htmlFor="unit-default" className="block text-small text-neutral-500 mb-1">
              Default unit
            </label>
            <input
              id="unit-default"
              type="text"
              value={value.default_unit}
              onChange={(e) => onChange({ ...value, default_unit: e.target.value })}
              className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-caption bg-white focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </div>
        </div>
      )}
    </div>
  )
}

export default UnitPriceOptionsPanel
