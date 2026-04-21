import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { getTrips, getProductsWithTrends, getRhythm, getCategoryBreakdown, getSavings, getStaples, getPriceMoves } from '@/api/analytics'
import { TripCostChart } from '@/components/analytics/TripCostChart'
import { ShoppingRhythmStrip } from '@/components/analytics/ShoppingRhythmStrip'
import { CategoryBreakdown } from '@/components/analytics/CategoryBreakdown'
import SavingsCard from '@/components/analytics/SavingsCard'
import { StaplesTable } from '@/components/analytics/StaplesTable'
import { PriceMoves } from '@/components/analytics/PriceMoves'
import { Sparkline } from '@/components/ui/Sparkline'
import { Badge } from '@/components/ui/Badge'

function AnalyticsPage() {
  const [productSearch, setProductSearch] = useState('')

  const { data: trips, isLoading: tripsLoading } = useQuery({
    queryKey: ['analytics', 'trips'],
    queryFn: getTrips,
  })

  const { data: productsWithTrends, isLoading: productsLoading } = useQuery({
    queryKey: ['analytics', 'products-trends'],
    queryFn: () => getProductsWithTrends({ sort: 'price_change', order: 'desc' }),
  })

  const { data: rhythm, isLoading: rhythmLoading } = useQuery({
    queryKey: ['analytics', 'rhythm'],
    queryFn: getRhythm,
  })

  const { data: categoryBreakdown, isLoading: categoryLoading } = useQuery({
    queryKey: ['analytics', 'category-breakdown'],
    queryFn: getCategoryBreakdown,
  })

  const { data: savings, isLoading: savingsLoading } = useQuery({
    queryKey: ['analytics', 'savings'],
    queryFn: getSavings,
  })

  const { data: staples, isLoading: staplesLoading } = useQuery({
    queryKey: ['analytics', 'staples'],
    queryFn: getStaples,
  })

  const { data: priceMoves, isLoading: priceMovesLoading } = useQuery({
    queryKey: ['analytics', 'price-moves'],
    queryFn: getPriceMoves,
  })

  const filteredProducts = productsWithTrends?.filter((p) =>
    p.name.toLowerCase().includes(productSearch.toLowerCase())
  ) ?? []

  return (
    <div className="py-8">
      <h1 className="font-display text-section font-bold text-neutral-900 tracking-tight">
        Analytics
      </h1>

      {/* Shopping Rhythm */}
      <div className="mt-6">
        <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">
          Shopping Rhythm
        </h2>
        {rhythmLoading ? (
          <div className="flex items-center justify-center h-24 text-body text-neutral-400">
            Loading...
          </div>
        ) : (
          rhythm && <ShoppingRhythmStrip data={rhythm} />
        )}
      </div>

      {/* Savings Card */}
      <div className="mt-8">
        <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">
          Savings
        </h2>
        {savingsLoading ? (
          <div className="h-32 flex items-center justify-center text-body text-neutral-400 bg-white rounded-2xl border border-neutral-200">
            Loading savings...
          </div>
        ) : savings ? (
          <SavingsCard data={savings} />
        ) : null}
      </div>

      {/* Staples */}
      <div className="mt-8">
        <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">
          Staples
        </h2>
        {staplesLoading ? (
          <div className="h-32 flex items-center justify-center text-body text-neutral-400 bg-white rounded-2xl border border-neutral-200">
            Loading staples...
          </div>
        ) : (
          <StaplesTable staples={staples ?? []} />
        )}
      </div>

      {/* Price Moves */}
      <div className="mt-8">
        <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">
          Price Moves
        </h2>
        {priceMovesLoading ? (
          <div className="h-32 flex items-center justify-center text-body text-neutral-400 bg-white rounded-2xl border border-neutral-200">
            Loading price moves...
          </div>
        ) : (
          <PriceMoves data={priceMoves} />
        )}
      </div>

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

      {/* Category Spending Breakdown */}
      <div className="mt-8">
        <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">
          Spending by Category
        </h2>
        <div className="bg-white rounded-2xl border border-neutral-200 p-4">
          {categoryLoading ? (
            <div className="h-48 flex items-center justify-center text-body text-neutral-400">
              Loading categories...
            </div>
          ) : categoryBreakdown ? (
            <CategoryBreakdown data={categoryBreakdown} />
          ) : null}
        </div>
      </div>

      {/* Products with Trends Table */}
      <div className="mt-8">
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-display text-feature font-semibold text-neutral-900">
            Product Price Trends
          </h2>
          <div className="relative w-52">
            <input
              type="text"
              value={productSearch}
              onChange={(e) => setProductSearch(e.target.value)}
              placeholder="Filter products..."
              className="text-body border border-neutral-200 rounded-lg px-3 py-1.5 w-full focus:outline-none focus:ring-2 focus:ring-brand/30 focus:border-brand placeholder:text-neutral-400"
            />
            {productSearch && (
              <button
                onClick={() => setProductSearch('')}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-neutral-400 hover:text-neutral-600"
                aria-label="Clear filter"
              >
                ×
              </button>
            )}
          </div>
        </div>
        <div className="bg-white rounded-2xl border border-neutral-200 overflow-hidden">
          {productsLoading ? (
            <p className="text-body text-neutral-400 py-8 text-center">Loading products...</p>
          ) : filteredProducts.length > 0 ? (
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
                  {filteredProducts.map((item) => {
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
              {productSearch ? `No products matching "${productSearch}".` : 'No product trend data yet.'}
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

export default AnalyticsPage
