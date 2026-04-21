import type { Rhythm } from '@/types'

interface StatCardProps {
  label: string
  value: string
  hint: string
  hintColor?: 'positive' | 'negative' | 'neutral'
  accent?: boolean
}

function StatCard({ label, value, hint, hintColor = 'neutral', accent = false }: StatCardProps) {
  const hintClass =
    hintColor === 'positive'
      ? 'text-success'
      : hintColor === 'negative'
        ? 'text-expensive'
        : 'text-neutral-500'

  return (
    <div
      className={`bg-white rounded-2xl border border-neutral-200 p-4 flex flex-col gap-1${accent ? ' border-t-2 border-t-brand' : ''}`}
    >
      <p className="text-xs text-neutral-500">{label}</p>
      <p className="font-display text-feature font-semibold text-neutral-900">{value}</p>
      {hint && <p className={`text-xs ${hintClass}`}>{hint}</p>}
    </div>
  )
}

function formatDelta(deltaPct: number | null): { text: string; color: 'positive' | 'negative' | 'neutral' } {
  if (deltaPct === null) return { text: '—', color: 'neutral' }
  const sign = deltaPct >= 0 ? '+' : ''
  const color = deltaPct > 0 ? 'positive' : deltaPct < 0 ? 'negative' : 'neutral'
  return { text: `${sign}${deltaPct.toFixed(1)}% vs prior 30d`, color }
}

export function ShoppingRhythmStrip({ data }: { data: Rhythm }) {
  const delta = formatDelta(data.trips.delta_pct)

  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
      <StatCard
        label="Trips (30d)"
        value={String(data.trips.current)}
        hint={delta.text}
        hintColor={delta.color}
        accent
      />
      <StatCard
        label="Avg basket"
        value={`$${data.avg_basket.current.toFixed(2)}`}
        hint={`Prior 30d: $${data.avg_basket.prior.toFixed(2)}`}
      />
      <StatCard
        label="Avg items / trip"
        value={data.avg_items_per_trip.toFixed(1)}
        hint="current window"
      />
      <StatCard
        label="Most-shopped day"
        value={data.most_shopped_dow ?? '—'}
        hint={data.history_days < 14 ? 'Need 2+ weeks data' : ''}
      />
    </div>
  )
}
