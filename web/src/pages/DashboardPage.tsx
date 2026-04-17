import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { useAuth } from '@/hooks/useAuth'
import { getOverview, getBuyAgain, getTrips } from '@/api/analytics'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import type { BuyAgainItem, Trip } from '@/types'

function urgencyEmoji(ratio: number): string {
  if (ratio > 1.0) return '\u{1F534}'
  if (ratio > 0.8) return '\u{1F7E1}'
  if (ratio > 0.6) return '\u{1F7E2}'
  return '\u26AA'
}

function urgencyLabel(ratio: number): string {
  if (ratio > 1.0) return 'Overdue'
  if (ratio > 0.8) return 'Running low'
  if (ratio > 0.6) return 'On the horizon'
  return 'Well stocked'
}

function formatCurrency(value: number | string | null | undefined): string {
  if (value == null) return '$0.00'
  const num = typeof value === 'number' ? value : parseFloat(value)
  if (isNaN(num)) return '$0.00'
  return `$${num.toFixed(2)}`
}

function toIsoDay(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function BuyAgainRow({ item }: { item: BuyAgainItem }) {
  return (
    <div className="flex items-center justify-between py-3 border-b border-neutral-200 last:border-b-0">
      <div className="flex items-center gap-3">
        <span role="img" aria-label={urgencyLabel(item.urgency_ratio)} className="text-body">{urgencyEmoji(item.urgency_ratio)}</span>
        <div>
          <Link
            to={`/products/${item.product_id}`}
            className="text-body-medium font-medium text-neutral-900 hover:text-brand"
          >
            {item.product_name}
          </Link>
          <p className="text-small text-neutral-400">
            every {Math.round(item.avg_days_between ?? item.avg_days_per_unit ?? 0)} days, last bought{' '}
            {Math.round(item.days_since_last)} days ago
            {item.last_store_name ? ` at ${item.last_store_name}` : item.last_store ? ` at ${item.last_store}` : ''}
          </p>
        </div>
      </div>
      <div className="flex items-center gap-2">
        <span className="text-small text-neutral-400">{urgencyLabel(item.urgency_ratio)}</span>
        <Button size="sm" variant="subtle">
          Add to list
        </Button>
      </div>
    </div>
  )
}

const HEATMAP_MONTHS = 4
const DAYS_IN_WEEK = 7
const MONTH_LABELS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

function ActivityHeatmap({
  trips,
  selectedDate,
  onSelectDate,
}: {
  trips: Trip[]
  selectedDate: string | null
  onSelectDate: (date: string | null) => void
}) {
  const { weeks, monthHeaders, max } = useMemo(() => {
    const counts = new Map<string, number>()
    for (const t of trips) counts.set(t.date, (counts.get(t.date) ?? 0) + 1)

    const today = new Date()
    today.setHours(0, 0, 0, 0)
    // End: Saturday of the current week (so today is always visible, no future weeks).
    const end = new Date(today)
    end.setDate(end.getDate() + (6 - end.getDay()))
    // Start: Sunday of the week HEATMAP_MONTHS before today.
    const start = new Date(today)
    start.setMonth(start.getMonth() - HEATMAP_MONTHS)
    start.setDate(start.getDate() - start.getDay())

    const weeks: { date: Date; iso: string; count: number }[][] = []
    const cursor = new Date(start)
    while (cursor <= end) {
      const week: { date: Date; iso: string; count: number }[] = []
      for (let i = 0; i < DAYS_IN_WEEK; i++) {
        const iso = toIsoDay(cursor)
        week.push({ date: new Date(cursor), iso, count: counts.get(iso) ?? 0 })
        cursor.setDate(cursor.getDate() + 1)
      }
      weeks.push(week)
    }

    const monthHeaders = weeks.map((w, i) => {
      const firstDay = w[0]?.date
      const prev = i > 0 ? weeks[i - 1]?.[0]?.date : null
      if (!firstDay) return ''
      if (!prev || firstDay.getMonth() !== prev.getMonth()) {
        return MONTH_LABELS[firstDay.getMonth()] ?? ''
      }
      return ''
    })

    const max = Math.max(1, ...Array.from(counts.values()))
    return { weeks, monthHeaders, max }
  }, [trips])

  function intensity(count: number): string {
    if (count === 0) return 'bg-neutral-100'
    const ratio = count / max
    if (ratio > 0.75) return 'bg-brand'
    if (ratio > 0.5) return 'bg-brand/70'
    if (ratio > 0.25) return 'bg-brand/45'
    return 'bg-brand/20'
  }

  return (
    <div className="overflow-x-auto">
      <div className="inline-block">
        <div className="flex gap-[3px] pl-7 mb-1">
          {monthHeaders.map((label, i) => (
            <div key={i} className="w-3 text-[10px] text-neutral-400 leading-none">
              {label}
            </div>
          ))}
        </div>
        <div className="flex gap-[3px]">
          <div className="flex flex-col gap-[3px] mr-1 text-[10px] text-neutral-400 leading-none">
            <div className="h-3" />
            <div className="h-3">Mon</div>
            <div className="h-3" />
            <div className="h-3">Wed</div>
            <div className="h-3" />
            <div className="h-3">Fri</div>
            <div className="h-3" />
          </div>
          {weeks.map((week, wi) => (
            <div key={wi} className="flex flex-col gap-[3px]">
              {week.map((cell) => {
                const isSelected = selectedDate === cell.iso
                const isFuture = cell.date > new Date()
                return (
                  <button
                    key={cell.iso}
                    type="button"
                    disabled={isFuture}
                    onClick={() => onSelectDate(isSelected ? null : cell.iso)}
                    title={`${cell.iso}: ${cell.count} trip${cell.count === 1 ? '' : 's'}`}
                    className={`w-3 h-3 rounded-sm transition ${intensity(cell.count)} ${
                      isSelected ? 'ring-2 ring-offset-1 ring-brand' : ''
                    } ${isFuture ? 'opacity-0 pointer-events-none' : 'hover:ring-1 hover:ring-brand/60'}`}
                  />
                )
              })}
            </div>
          ))}
        </div>
        <div className="flex items-center gap-2 mt-2 text-[10px] text-neutral-400">
          <span>Less</span>
          <div className="w-3 h-3 rounded-sm bg-neutral-100" />
          <div className="w-3 h-3 rounded-sm bg-brand/20" />
          <div className="w-3 h-3 rounded-sm bg-brand/45" />
          <div className="w-3 h-3 rounded-sm bg-brand/70" />
          <div className="w-3 h-3 rounded-sm bg-brand" />
          <span>More</span>
        </div>
      </div>
    </div>
  )
}

function DashboardPage() {
  const { user } = useAuth()
  const [selectedDate, setSelectedDate] = useState<string | null>(null)

  const { data: overview } = useQuery({
    queryKey: ['analytics', 'overview'],
    queryFn: getOverview,
  })

  const { data: buyAgainItems } = useQuery({
    queryKey: ['analytics', 'buy-again'],
    queryFn: getBuyAgain,
  })

  const { data: trips } = useQuery({
    queryKey: ['analytics', 'trips'],
    queryFn: getTrips,
  })

  const activityTrips = useMemo(() => {
    if (!trips) return []
    if (selectedDate) return trips.filter((t) => t.date === selectedDate)
    return trips.slice(0, 10)
  }, [trips, selectedDate])

  const changePositive = (overview?.percent_change ?? 0) <= 0

  return (
    <div className="py-8">
      <h1 className="font-display text-section font-bold text-neutral-900 tracking-tight">
        {user ? `Welcome, ${user.name}` : 'Welcome to CartLedger'}
      </h1>

      {/* Overview Cards */}
      <div className="mt-6 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <div className="p-4 rounded-2xl border border-neutral-200 bg-white shadow-subtle">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Spent This Month</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">
            {formatCurrency(overview?.spent_this_month)}
          </p>
          {overview && overview.percent_change != null && (
            <Badge variant={changePositive ? 'success' : 'warning'} className="mt-1">
              {overview.percent_change > 0 ? '+' : ''}
              {overview.percent_change.toFixed(1)}% vs last month
            </Badge>
          )}
        </div>
        <div className="p-4 rounded-2xl border border-neutral-200 bg-white shadow-subtle">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Trips</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">
            {overview?.trip_count ?? 0}
          </p>
        </div>
        <div className="p-4 rounded-2xl border border-neutral-200 bg-white shadow-subtle">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Avg Trip Cost</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">
            {formatCurrency(overview?.avg_trip_cost)}
          </p>
        </div>
        <div className="p-4 rounded-2xl border border-neutral-200 bg-white shadow-subtle">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Last Month</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">
            {formatCurrency(overview?.spent_last_month)}
          </p>
        </div>
      </div>

      {/* Buy Again Section */}
      <div className="mt-8">
        <h2 className="font-display text-feature font-semibold text-neutral-900">
          Likely Need Soon
        </h2>
        <div className="mt-3 bg-white rounded-2xl border border-neutral-200 p-4">
          {buyAgainItems && buyAgainItems.length > 0 ? (
            buyAgainItems.map((item) => (
              <BuyAgainRow key={item.product_id} item={item} />
            ))
          ) : (
            <p className="text-body text-neutral-400 py-4 text-center">
              Not enough purchase history yet. Keep scanning receipts!
            </p>
          )}
        </div>
      </div>

      {/* Heatmap */}
      <div className="mt-8">
        <div className="flex items-center justify-between">
          <h2 className="font-display text-feature font-semibold text-neutral-900">
            Receipt Activity
          </h2>
          {selectedDate && (
            <button
              type="button"
              onClick={() => setSelectedDate(null)}
              className="text-caption text-brand hover:underline"
            >
              Clear filter
            </button>
          )}
        </div>
        <div className="mt-3 bg-white rounded-2xl border border-neutral-200 p-4">
          <ActivityHeatmap
            trips={trips ?? []}
            selectedDate={selectedDate}
            onSelectDate={setSelectedDate}
          />
        </div>
      </div>

      {/* Activity */}
      <div className="mt-8">
        <div className="flex items-center justify-between">
          <h2 className="font-display text-feature font-semibold text-neutral-900">
            Activity
            {selectedDate && (
              <span className="ml-2 text-small font-normal text-neutral-400">
                on {new Date(`${selectedDate}T00:00:00`).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })}
              </span>
            )}
          </h2>
          <Link to="/analytics" className="text-caption text-brand hover:underline">
            View all
          </Link>
        </div>
        <div className="mt-3 bg-white rounded-2xl border border-neutral-200 divide-y divide-neutral-200">
          {activityTrips.length > 0 ? (
            activityTrips.map((trip) => (
              <div key={trip.receipt_id} className="flex items-center justify-between p-4">
                <div>
                  {trip.store_id ? (
                    <Link
                      to={`/stores/${trip.store_id}`}
                      className="text-body-medium font-medium text-neutral-900 hover:text-brand"
                    >
                      {trip.store_name}
                    </Link>
                  ) : (
                    <span className="text-body-medium font-medium text-neutral-900">
                      {trip.store_name}
                    </span>
                  )}
                  <p className="text-small text-neutral-400">
                    {new Date(`${trip.date}T00:00:00`).toLocaleDateString()} &middot; {trip.item_count} items
                  </p>
                </div>
                <Link
                  to={`/receipts/${trip.receipt_id}`}
                  className="text-body font-bold text-neutral-900 hover:text-brand"
                >
                  {formatCurrency(trip.total)}
                </Link>
              </div>
            ))
          ) : (
            <p className="text-body text-neutral-400 py-4 text-center">
              {selectedDate
                ? 'No trips on this day.'
                : 'No trips yet. Scan a receipt to get started!'}
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

export default DashboardPage
