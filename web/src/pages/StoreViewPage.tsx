import { useParams, Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { getStoreSummary } from '@/api/analytics'

function formatAddress(store: { address: string | null; city: string | null; state: string | null; zip: string | null }) {
  const parts = [store.address, store.city, store.state].filter(Boolean)
  if (parts.length === 0) return null
  let addr = parts.join(', ')
  if (store.zip) addr += ` ${store.zip}`
  return addr
}

function formatCurrency(value: string | number | undefined): string {
  if (value == null) return '$0.00'
  const num = typeof value === 'number' ? value : parseFloat(value)
  if (isNaN(num)) return '$0.00'
  return `$${num.toFixed(2)}`
}

function StoreViewPage() {
  const { id } = useParams<{ id: string }>()

  const { data: summary, isLoading, error } = useQuery({
    queryKey: ['analytics', 'store', id],
    queryFn: () => getStoreSummary(id!),
    enabled: !!id,
  })

  if (isLoading) {
    return (
      <div className="py-8">
        <p className="text-body text-neutral-400">Loading store details...</p>
      </div>
    )
  }

  if (error || !summary) {
    return (
      <div className="py-8">
        <h1 className="font-display text-subhead font-bold text-neutral-900">Store Not Found</h1>
        <p className="mt-2 text-body text-neutral-400">
          Could not load store details.{' '}
          <Link to="/" className="text-brand hover:underline">
            Back to dashboard
          </Link>
        </p>
      </div>
    )
  }

  return (
    <div className="py-8">
      <div className="mb-4">
        <Link to="/" className="text-caption text-brand hover:underline">
          &larr; Back to Dashboard
        </Link>
      </div>
      <div className="flex items-center gap-3">
        {summary.store.icon && <span className="text-section">{summary.store.icon}</span>}
        <div>
          <div className="flex items-center gap-2">
            <h1 className="font-display text-section font-bold text-neutral-900 tracking-tight">
              {summary.store.name}
            </h1>
            {summary.store.store_number && (
              <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-neutral-100 text-neutral-600">
                Store #{summary.store.store_number}
              </span>
            )}
          </div>
          {summary.store.nickname && (
            <p className="text-small text-neutral-500">{summary.store.nickname}</p>
          )}
          {formatAddress(summary.store) && (
            <p className="text-small text-neutral-400">{formatAddress(summary.store)}</p>
          )}
        </div>
      </div>

      {/* Stats Cards */}
      <div className="mt-6 grid grid-cols-1 sm:grid-cols-3 gap-4">
        <div className="p-4 rounded-2xl border border-neutral-200 bg-white shadow-subtle">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Total Spent</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">
            {formatCurrency(summary.total_spent)}
          </p>
        </div>
        <div className="p-4 rounded-2xl border border-neutral-200 bg-white shadow-subtle">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Trips</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">
            {summary.trip_count}
          </p>
        </div>
        <div className="p-4 rounded-2xl border border-neutral-200 bg-white shadow-subtle">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Avg Trip Cost</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">
            {formatCurrency(summary.avg_trip_cost)}
          </p>
        </div>
      </div>

      {/* Price Leaders */}
      <div className="mt-8">
        <h2 className="font-display text-feature font-semibold text-neutral-900">
          Price Leaders
        </h2>
        <p className="text-small text-neutral-400 mt-1">
          Products where this store has the best price
        </p>
        <div className="mt-3 bg-white rounded-2xl border border-neutral-200 divide-y divide-neutral-200">
          {summary.price_leaders.length > 0 ? (
            summary.price_leaders.map((leader, i) => (
              <div key={i} className="flex items-center justify-between p-4">
                <span className="text-body text-neutral-900">{leader.product_name}</span>
                <span className="text-body font-bold text-neutral-900">
                  {formatCurrency(leader.avg_price)}
                </span>
              </div>
            ))
          ) : (
            <p className="text-body text-neutral-400 py-4 text-center">
              No price comparison data yet.
            </p>
          )}
        </div>
      </div>

      {/* Recent Trips */}
      <div className="mt-8">
        <h2 className="font-display text-feature font-semibold text-neutral-900">
          Recent Trips
        </h2>
        <div className="mt-3 bg-white rounded-2xl border border-neutral-200 divide-y divide-neutral-200">
          {summary.recent_trips.length > 0 ? (
            summary.recent_trips.map((trip) => (
              <Link
                key={trip.receipt_id}
                to={`/receipts/${trip.receipt_id}`}
                className="flex items-center justify-between p-4 hover:bg-neutral-50 transition-colors"
              >
                <div>
                  <p className="text-body text-neutral-900">
                    {new Date(trip.date).toLocaleDateString('en-US', {
                      weekday: 'short',
                      month: 'short',
                      day: 'numeric',
                    })}
                  </p>
                  <p className="text-small text-neutral-400">{trip.item_count} items</p>
                </div>
                <span className="text-body font-bold text-neutral-900">
                  {formatCurrency(trip.total)}
                </span>
              </Link>
            ))
          ) : (
            <p className="text-body text-neutral-400 py-4 text-center">
              No trips recorded at this store yet.
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

export default StoreViewPage
