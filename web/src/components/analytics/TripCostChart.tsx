import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from 'recharts'
import type { Trip } from '@/types'

const STORE_COLORS = ['#7132f5', '#0ea5e9', '#10b981', '#f59e0b', '#ef4444', '#8b5cf6', '#ec4899']

interface TripCostChartProps {
  trips: Trip[]
}

function TripCostChart({ trips }: TripCostChartProps) {
  if (trips.length === 0) {
    return (
      <div className="flex items-center justify-center h-64 text-body text-neutral-400">
        No trip data to chart yet.
      </div>
    )
  }

  const sorted = [...trips].sort((a, b) => a.date.localeCompare(b.date))
  const stores = [...new Set(sorted.map((t) => t.store_name))]

  const years = new Set(sorted.map((t) => t.date.slice(0, 4)))
  const multiYear = years.size > 1

  const formatLabel = (date: string) =>
    new Date(date + 'T00:00:00').toLocaleDateString('en-US', {
      month: 'short',
      day: 'numeric',
      ...(multiYear ? { year: '2-digit' } : {}),
    })

  // Pivot: date label → { [store]: total }
  const dateMap = new Map<string, Record<string, number>>()
  for (const trip of sorted) {
    const label = formatLabel(trip.date)
    if (!dateMap.has(label)) dateMap.set(label, {})
    const entry = dateMap.get(label)!
    const val = parseFloat(String(trip.total)) || 0
    entry[trip.store_name] = (entry[trip.store_name] ?? 0) + val
  }

  const chartData = [...dateMap.entries()].map(([date, values]) => ({ date, ...values }))

  return (
    <ResponsiveContainer width="100%" height={300}>
      <LineChart data={chartData} margin={{ top: 8, right: 16, left: 8, bottom: 8 }}>
        <CartesianGrid strokeDasharray="3 3" stroke="#dedee5" />
        <XAxis dataKey="date" tick={{ fontSize: 12, fill: '#9497a9' }} />
        <YAxis
          tickFormatter={(v: number) => `$${v}`}
          tick={{ fontSize: 12, fill: '#9497a9' }}
        />
        <Tooltip
          formatter={(value: number, name: string) => [
            `$${(Number(value) || 0).toFixed(2)}`,
            name,
          ]}
          labelFormatter={(label) => {
            // Always show full year in tooltip regardless of axis format
            const trip = sorted.find((t) => formatLabel(t.date) === label)
            if (!trip) return label
            return new Date(trip.date + 'T00:00:00').toLocaleDateString('en-US', {
              month: 'short',
              day: 'numeric',
              year: 'numeric',
            })
          }}
          contentStyle={{ borderRadius: 12, border: '1px solid #dedee5', fontSize: 14 }}
        />
        <Legend wrapperStyle={{ fontSize: 13 }} />
        {stores.map((store, i) => (
          <Line
            key={store}
            type="monotone"
            dataKey={store}
            stroke={STORE_COLORS[i % STORE_COLORS.length]}
            strokeWidth={2}
            dot={{ fill: STORE_COLORS[i % STORE_COLORS.length], r: 3 }}
            activeDot={{ r: 5 }}
            connectNulls={false}
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  )
}

export { TripCostChart }
