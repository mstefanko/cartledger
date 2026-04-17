import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { BarChart, Bar, XAxis, YAxis, Cell, ResponsiveContainer } from 'recharts'
import { Sparkline } from '@/components/ui/Sparkline'
import { fetchProductTrend, fetchGroupTrend } from '@/api/analytics'
import type { ListItemWithPrice } from '@/types'

interface ItemPriceDetailProps {
  item: ListItemWithPrice
}

export function ItemPriceDetail({ item }: ItemPriceDetailProps) {
  const targetId = item.product_group_id
    ? null
    : (item.cheapest_product_id ?? item.product_id)

  const productQuery = useQuery({
    queryKey: ['product-trend', targetId],
    queryFn: () => fetchProductTrend(targetId!),
    enabled: !!targetId && !item.product_group_id,
    staleTime: 5 * 60 * 1000,
  })

  const groupQuery = useQuery({
    queryKey: ['group-trend', item.product_group_id],
    queryFn: () => fetchGroupTrend(item.product_group_id!),
    enabled: !!item.product_group_id,
    staleTime: 5 * 60 * 1000,
  })

  const trend = item.product_group_id ? groupQuery.data : productQuery.data
  const isLoading = item.product_group_id ? groupQuery.isLoading : productQuery.isLoading

  // Sparkline: min price per date (handles multiple entries per date for groups)
  const sparklineData = useMemo(() => {
    if (!trend) return []
    const byDate = new Map<string, number>()
    for (const pt of trend.price_history) {
      const cur = byDate.get(pt.date)
      if (cur === undefined || pt.normalized_price < cur) {
        byDate.set(pt.date, pt.normalized_price)
      }
    }
    return Array.from(byDate.values())
  }, [trend])

  // Per-store bars: latest price per store (min across members at that store-date)
  const storeData = useMemo(() => {
    if (!trend) return []
    const byStore = new Map<string, { date: string; price: number }>()
    for (const pt of trend.price_history) {
      const cur = byStore.get(pt.store)
      if (!cur || pt.date > cur.date || (pt.date === cur.date && pt.normalized_price < cur.price)) {
        byStore.set(pt.store, { date: pt.date, price: pt.normalized_price })
      }
    }
    return Array.from(byStore.entries())
      .map(([name, { price }]) => ({ name, price }))
      .sort((a, b) => a.price - b.price)
  }, [trend])

  const minPrice = storeData.length > 0 ? storeData[0]!.price : null

  if (isLoading) {
    return <div className="py-2 text-xs text-neutral-400">Loading prices...</div>
  }

  if (!trend || sparklineData.length === 0) {
    return <div className="py-2 text-xs text-neutral-400">No price history available.</div>
  }

  return (
    <div className="mt-2 space-y-2">
      <div className="flex items-center gap-3">
        {sparklineData.length >= 2 ? (
          <>
            <span className="text-xs text-neutral-400">90d trend</span>
            <Sparkline data={sparklineData} width={100} height={28} />
            <span className="text-xs text-neutral-400">
              avg ${trend.avg_price.toFixed(2)}
            </span>
          </>
        ) : (
          <span className="text-xs text-neutral-400">Only 1 observation so far</span>
        )}
      </div>
      {storeData.length > 0 && (
        <ResponsiveContainer width="100%" height={Math.min(storeData.length * 22 + 10, 100)}>
          <BarChart data={storeData} layout="vertical" margin={{ top: 0, right: 8, bottom: 0, left: 0 }}>
            <XAxis type="number" hide domain={[0, 'dataMax']} />
            <YAxis type="category" dataKey="name" width={72} tick={{ fontSize: 10 }} tickLine={false} axisLine={false} />
            <Bar dataKey="price" radius={[0, 3, 3, 0]} maxBarSize={14}>
              {storeData.map((entry, index) => (
                <Cell
                  key={index}
                  fill={entry.price === minPrice ? '#16a34a' : '#7132f5'}
                />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      )}
      {item.product_id && (
        <a
          href={`/products/${item.product_id}`}
          className="text-xs text-brand underline"
        >
          View price history &rarr;
        </a>
      )}
    </div>
  )
}
