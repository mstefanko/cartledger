import type { PriceMoves as PriceMovesData, PriceMove } from '@/types'

interface PriceMovesProps {
  data: PriceMovesData | null | undefined
}

function formatPrice(n: number): string {
  return `$${n.toFixed(2)}`
}

function formatPct(n: number): string {
  const sign = n > 0 ? '+' : ''
  return `${sign}${n.toFixed(1)}%`
}

interface CardProps {
  title: string
  items: PriceMove[]
  direction: 'up' | 'down'
  emptyMessage: string
}

function PriceCard({ title, items, direction, emptyMessage }: CardProps) {
  const isUp = direction === 'up'
  // Semantic colors: red for price increases (bad for consumer), green for decreases (good)
  const arrowGlyph = isUp ? '↑' : '↓'
  const pctColorClass = isUp ? 'text-expensive' : 'text-success'
  const arrowBgClass = isUp ? 'bg-expensive-subtle' : 'bg-success-subtle'
  const borderAccentClass = isUp ? 'border-l-expensive' : 'border-l-success'

  return (
    <div className={`bg-white rounded-2xl border border-neutral-200 border-l-4 ${borderAccentClass} overflow-hidden`}>
      <div className="px-5 py-4 border-b border-neutral-100">
        <div className="flex items-center gap-2">
          <span
            className={`inline-flex items-center justify-center w-6 h-6 rounded-full text-small font-bold ${arrowBgClass} ${pctColorClass}`}
            aria-hidden="true"
          >
            {arrowGlyph}
          </span>
          <h3 className="font-display text-body-medium font-semibold text-neutral-900">
            {title}
          </h3>
        </div>
      </div>

      {items.length === 0 ? (
        <div className="px-5 py-6">
          <p className="text-small text-neutral-400">{emptyMessage}</p>
        </div>
      ) : (
        <ul className="divide-y divide-neutral-100">
          {items.map((item) => (
            <li key={item.product_id} className="px-5 py-3 flex items-center justify-between gap-3 hover:bg-neutral-50 transition-colors">
              <span className="text-body text-neutral-900 truncate flex-1 min-w-0" title={item.name}>
                {item.name}
              </span>
              <div className="flex items-center gap-2 shrink-0">
                <span className="text-small text-neutral-400 whitespace-nowrap">
                  {formatPrice(item.avg_30_90d)}
                  <span className="mx-1 text-neutral-300">→</span>
                  {formatPrice(item.avg_0_30d)}
                </span>
                <span className={`text-caption font-semibold whitespace-nowrap ${pctColorClass}`}>
                  {formatPct(item.pct_change)}
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function PriceMoves({ data }: PriceMovesProps) {
  const up = data?.up ?? []
  const down = data?.down ?? []

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
      <PriceCard
        title="Going up"
        items={up}
        direction="up"
        emptyMessage="No big moves in this direction yet."
      />
      <PriceCard
        title="Going down"
        items={down}
        direction="down"
        emptyMessage="No big moves in this direction yet."
      />
    </div>
  )
}

export { PriceMoves }
export default PriceMoves
