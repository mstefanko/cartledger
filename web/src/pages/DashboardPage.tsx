import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { useAuth } from '@/hooks/useAuth'
import { getOverview, getBuyAgain, getDeals, getTrips } from '@/api/analytics'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import type { BuyAgainItem } from '@/types'

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

function formatCurrency(value: string | undefined): string {
  if (!value) return '$0.00'
  const num = parseFloat(value)
  if (isNaN(num)) return '$0.00'
  return `$${num.toFixed(2)}`
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
            every {Math.round(item.avg_days_between)} days, last bought{' '}
            {Math.round(item.days_since_last)} days ago
            {item.last_store ? ` at ${item.last_store}` : ''}
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

function DashboardPage() {
  const { user } = useAuth()

  const { data: overview } = useQuery({
    queryKey: ['analytics', 'overview'],
    queryFn: getOverview,
  })

  const { data: buyAgainItems } = useQuery({
    queryKey: ['analytics', 'buy-again'],
    queryFn: getBuyAgain,
  })

  const { data: deals } = useQuery({
    queryKey: ['analytics', 'deals'],
    queryFn: getDeals,
  })

  const { data: trips } = useQuery({
    queryKey: ['analytics', 'trips'],
    queryFn: getTrips,
  })

  const recentTrips = trips?.slice(0, 5)
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

      {/* Deals Section */}
      <div className="mt-8">
        <h2 className="font-display text-feature font-semibold text-neutral-900">
          Current Deals
        </h2>
        <div className="mt-3 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {deals && deals.length > 0 ? (
            deals.map((deal) => (
              <div
                key={deal.product_id}
                className="p-4 bg-white rounded-2xl border border-neutral-200"
              >
                <Link
                  to={`/products/${deal.product_id}`}
                  className="text-body-medium font-medium text-neutral-900 hover:text-brand"
                >
                  {deal.product_name}
                </Link>
                <p className="text-small text-neutral-400 mt-1">{deal.store_name}</p>
                <div className="flex items-center gap-2 mt-2">
                  <span className="text-body font-bold text-neutral-900">
                    {formatCurrency(deal.current_price)}
                  </span>
                  <span className="text-small text-neutral-400 line-through">
                    avg {formatCurrency(deal.avg_price)}
                  </span>
                  <Badge variant="success">-{(deal.savings_percent ?? 0).toFixed(0)}%</Badge>
                </div>
              </div>
            ))
          ) : (
            <p className="text-body text-neutral-400 col-span-full py-4 text-center">
              No deals detected yet.
            </p>
          )}
        </div>
      </div>

      {/* Recent Trips */}
      <div className="mt-8">
        <div className="flex items-center justify-between">
          <h2 className="font-display text-feature font-semibold text-neutral-900">
            Recent Trips
          </h2>
          <Link to="/analytics" className="text-caption text-brand hover:underline">
            View all
          </Link>
        </div>
        <div className="mt-3 bg-white rounded-2xl border border-neutral-200 divide-y divide-neutral-200">
          {recentTrips && recentTrips.length > 0 ? (
            recentTrips.map((trip) => (
              <div key={trip.receipt_id} className="flex items-center justify-between p-4">
                <div>
                  <Link
                    to={`/stores/${trip.store_id}`}
                    className="text-body-medium font-medium text-neutral-900 hover:text-brand"
                  >
                    {trip.store_name}
                  </Link>
                  <p className="text-small text-neutral-400">
                    {new Date(trip.date).toLocaleDateString()} &middot; {trip.item_count} items
                  </p>
                </div>
                <span className="text-body font-bold text-neutral-900">
                  {formatCurrency(trip.total)}
                </span>
              </div>
            ))
          ) : (
            <p className="text-body text-neutral-400 py-4 text-center">
              No trips yet. Scan a receipt to get started!
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

export default DashboardPage
