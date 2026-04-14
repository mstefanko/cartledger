import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts'
import type { Trip } from '@/types'

interface TripCostChartProps {
  trips: Trip[]
}

function TripCostChart({ trips }: TripCostChartProps) {
  const chartData = [...trips]
    .sort((a, b) => a.date.localeCompare(b.date))
    .map((trip) => ({
      date: new Date(trip.date).toLocaleDateString('en-US', { month: 'short', day: 'numeric' }),
      total: parseFloat(trip.total) || 0,
      store: trip.store_name,
    }))

  if (chartData.length === 0) {
    return (
      <div className="flex items-center justify-center h-64 text-body text-neutral-400">
        No trip data to chart yet.
      </div>
    )
  }

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
          formatter={(value: number) => [`$${(Number(value) || 0).toFixed(2)}`, 'Total']}
          contentStyle={{
            borderRadius: 12,
            border: '1px solid #dedee5',
            fontSize: 14,
          }}
        />
        <Line
          type="monotone"
          dataKey="total"
          stroke="#7132f5"
          strokeWidth={2}
          dot={{ fill: '#7132f5', r: 3 }}
          activeDot={{ r: 5 }}
        />
      </LineChart>
    </ResponsiveContainer>
  )
}

export { TripCostChart }
