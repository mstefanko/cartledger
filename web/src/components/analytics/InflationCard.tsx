import { useState } from 'react'
import type { InflationIndex } from '@/types'

interface InflationCardProps {
  data: InflationIndex | null | undefined
}

interface MetricRowProps {
  label: string
  sublabel: string
  value: number | null | undefined
}

function MetricRow({ label, sublabel, value }: MetricRowProps) {
  if (value == null) {
    return (
      <div className="flex items-center justify-between py-5 px-6 group">
        <div className="flex flex-col gap-0.5">
          <span className="font-display text-body-medium font-semibold text-neutral-900 tracking-tight">
            {label}
          </span>
          <span className="text-small text-neutral-400">{sublabel}</span>
        </div>
        <span className="text-caption text-neutral-400 italic">Not enough data</span>
      </div>
    )
  }

  const isUp = value > 0
  const isDown = value < 0
  const isFlat = value === 0

  const sign = isUp ? '+' : ''
  const formatted = `${sign}${value.toFixed(2)}%`

  const arrowGlyph = isUp ? '↑' : isDown ? '↓' : '→'
  const valueColorClass = isUp
    ? 'text-expensive'
    : isDown
      ? 'text-success'
      : 'text-neutral-500'
  const badgeBgClass = isUp
    ? 'bg-expensive-subtle'
    : isDown
      ? 'bg-success-subtle'
      : 'bg-neutral-50'
  const accentLineClass = isUp
    ? 'bg-expensive'
    : isDown
      ? 'bg-success'
      : 'bg-neutral-200'

  return (
    <div className="flex items-center justify-between py-5 px-6 relative">
      {/* Left accent line */}
      <div className={`absolute left-0 top-4 bottom-4 w-0.5 rounded-full ${accentLineClass} opacity-60`} />

      <div className="flex flex-col gap-0.5 pl-3">
        <span className="font-display text-body-medium font-semibold text-neutral-900 tracking-tight">
          {label}
        </span>
        <span className="text-small text-neutral-400">{sublabel}</span>
      </div>

      <div className="flex items-center gap-3 shrink-0">
        {/* Arrow badge */}
        {!isFlat && (
          <span
            className={`inline-flex items-center justify-center w-7 h-7 rounded-full text-body font-bold ${badgeBgClass} ${valueColorClass}`}
            aria-hidden="true"
          >
            {arrowGlyph}
          </span>
        )}
        {/* Big number */}
        <span
          className={`font-display font-bold tabular-nums ${valueColorClass}`}
          style={{ fontSize: '28px', lineHeight: 1, letterSpacing: '-0.5px' }}
        >
          {formatted}
        </span>
      </div>
    </div>
  )
}

function InfoTooltip() {
  const [open, setOpen] = useState(false)

  return (
    <div className="relative inline-flex">
      <button
        type="button"
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        className="w-4 h-4 rounded-full bg-neutral-100 text-neutral-400 hover:bg-neutral-200 hover:text-neutral-600 transition-colors flex items-center justify-center"
        aria-label="About the inflation index"
      >
        <svg width="10" height="10" viewBox="0 0 10 10" fill="currentColor">
          <path d="M5 0a5 5 0 1 0 0 10A5 5 0 0 0 5 0zm.5 7.5h-1v-3h1v3zm0-4h-1v-1h1v1z" />
        </svg>
      </button>
      {open && (
        <div
          className="absolute bottom-full right-0 mb-2 w-64 bg-neutral-900 text-white text-small rounded-lg px-3 py-2 shadow-subtle z-10 leading-relaxed"
          role="tooltip"
        >
          Your regularly-bought basket, priced at today's window vs 90 / 180 days ago.
          <div className="absolute top-full right-3 w-0 h-0" style={{
            borderLeft: '4px solid transparent',
            borderRight: '4px solid transparent',
            borderTop: '4px solid #101114',
          }} />
        </div>
      )}
    </div>
  )
}

function InflationCard({ data }: InflationCardProps) {
  if (!data) {
    return (
      <div className="bg-white rounded-2xl border border-neutral-200 overflow-hidden">
        <div className="h-32 flex items-center justify-center text-body text-neutral-400">
          Loading inflation index...
        </div>
      </div>
    )
  }

  if (data.suppressed) {
    return (
      <div className="bg-white rounded-2xl border border-neutral-200 overflow-hidden">
        <div className="flex flex-col items-center justify-center py-10 px-6 gap-2">
          <span
            className="inline-flex items-center justify-center w-8 h-8 rounded-full bg-neutral-50 text-neutral-400 mb-1"
            aria-hidden="true"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
              <path d="M8 1a7 7 0 1 0 0 14A7 7 0 0 0 8 1zm.75 10.5h-1.5v-5h1.5v5zm0-6.5h-1.5V3.5h1.5V5z" />
            </svg>
          </span>
          <p className="text-body text-neutral-500 text-center">
            {data.suppression_reason ?? 'Not enough data yet.'}
          </p>
          {data.basket_size > 0 && (
            <p className="text-small text-neutral-400 text-center">
              {data.basket_size} staple{data.basket_size !== 1 ? 's' : ''} in basket
            </p>
          )}
        </div>
      </div>
    )
  }

  return (
    <div className="bg-white rounded-2xl border border-neutral-200 overflow-hidden">
      {/* Card header */}
      <div className="px-6 py-4 border-b border-neutral-100 flex items-center justify-between">
        <div className="flex items-center gap-2">
          {/* Tiny index icon — three stacked ledger lines */}
          <div className="flex flex-col gap-0.5 shrink-0" aria-hidden="true">
            <div className="w-4 h-0.5 rounded-full bg-neutral-300" />
            <div className="w-3 h-0.5 rounded-full bg-neutral-300" />
            <div className="w-4 h-0.5 rounded-full bg-neutral-300" />
          </div>
          <h3 className="font-display text-body-medium font-semibold text-neutral-900">
            Basket Inflation
          </h3>
          {data.basket_size > 0 && (
            <span className="text-small text-neutral-400">
              · {data.basket_size} item{data.basket_size !== 1 ? 's' : ''}
            </span>
          )}
        </div>
        <InfoTooltip />
      </div>

      {/* Metrics */}
      <div className="divide-y divide-neutral-100">
        <MetricRow
          label="3-month change"
          sublabel="vs. 90 days ago"
          value={data.change_3mo_pct}
        />
        <MetricRow
          label="6-month change"
          sublabel="vs. 180 days ago"
          value={data.change_6mo_pct}
        />
      </div>
    </div>
  )
}

export { InflationCard }
export default InflationCard
