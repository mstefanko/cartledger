import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { getTrips, getProductsWithTrends } from '@/api/analytics'
import { TripCostChart } from '@/components/analytics/TripCostChart'
import { Sparkline } from '@/components/ui/Sparkline'
import { Badge } from '@/components/ui/Badge'

function AnalyticsPage() {
  const { data: trips, isLoading: tripsLoading } = useQuery({
    queryKey: ['analytics', 'trips'],
    queryFn: getTrips,
  })

  const { data: productsWithTrends, isLoading: productsLoading } = useQuery({
    queryKey: ['analytics', 'products-trends'],
    queryFn: () => getProductsWithTrends({ sort: 'price_change', order: 'desc' }),
  })

  return (
    <div className="py-8">
      <h1 className="font-display text-section font-bold text-neutral-900 tracking-tight">
        Analytics
      </h1>

      {/* Trip Cost Chart */}
      <div className="mt-6">
        <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">
          Trip Costs Over Time
        </h2>
        <div className="bg-white rounded-2xl border border-neutral-200 p-4">
          {tripsLoading ? (
            <div className="flex items-center justify-center h-64 text-body text-neutral-400">
              Loading chart...
            </div>
          ) : (
            <TripCostChart trips={trips ?? []} />
          )}
        </div>
      </div>

      {/* Products with Trends Table */}
      <div className="mt-8">
        <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">
          Product Price Trends
        </h2>
        <div className="bg-white rounded-2xl border border-neutral-200 overflow-hidden">
          {productsLoading ? (
            <p className="text-body text-neutral-400 py-8 text-center">Loading products...</p>
          ) : productsWithTrends && productsWithTrends.length > 0 ? (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="border-b border-neutral-200 bg-neutral-50">
                    <th className="text-left text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                      Product
                    </th>
                    <th className="text-left text-caption font-semibold text-neutral-400 uppercase px-4 py-3" title="Green = sale price">
                      Trend
                    </th>
                    <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                      Current
                    </th>
                    <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                      Change
                    </th>
                    <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                      Min / Max
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-neutral-200">
                  {productsWithTrends.map((item) => {
                    const sparkPoints = item.sparkline ?? []
                    const sparkData = sparkPoints.map((p) => parseFloat(p.price)).filter((n) => !isNaN(n))
                    const sparkHighlights = sparkPoints.map((p) => p.is_sale)
                    const pctChange = item.percent_change ?? 0
                    const changeVariant = pctChange > 0 ? 'warning' : pctChange < 0 ? 'success' : 'neutral'

                    return (
                      <tr key={item.id} className="hover:bg-neutral-50">
                        <td className="px-4 py-3">
                          <Link
                            to={`/products/${item.id}`}
                            className="text-body text-brand hover:underline"
                          >
                            {item.name}
                          </Link>
                          {item.category && (
                            <p className="text-small text-neutral-400">{item.category}</p>
                          )}
                        </td>
                        <td className="px-4 py-3">
                          {sparkData.length >= 2 ? (
                            <Sparkline data={sparkData} highlights={sparkHighlights} />
                          ) : (
                            <span className="text-small text-neutral-400">&mdash;</span>
                          )}
                        </td>
                        <td className="px-4 py-3 text-right text-body font-medium text-neutral-900">
                          {item.latest_price ? `$${item.latest_price.toFixed(2)}` : '\u2014'}
                        </td>
                        <td className="px-4 py-3 text-right">
                          <Badge variant={changeVariant}>
                            {pctChange > 0 ? '+' : ''}
                            {pctChange.toFixed(1)}%
                          </Badge>
                        </td>
                        <td className="px-4 py-3 text-right text-small text-neutral-400">
                          {item.min_price != null && item.max_price != null
                            ? `$${item.min_price.toFixed(2)} / $${item.max_price.toFixed(2)}`
                            : '\u2014'}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          ) : (
            <p className="text-body text-neutral-400 py-8 text-center">
              No product trend data yet.
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

export default AnalyticsPage
