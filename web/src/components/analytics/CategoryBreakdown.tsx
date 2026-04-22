import { PieChart, Pie, Cell, Tooltip, ResponsiveContainer } from 'recharts'
import type { CategoryBreakdown as CategoryBreakdownData } from '@/types'

// Color palette mirrors STORE_COLORS in TripCostChart.tsx.
// Slate tones reserved for Uncategorized/Unmatched so they read as "cleanup" buckets.
const CATEGORY_COLORS = [
  '#7132f5', // brand purple
  '#0ea5e9', // sky blue
  '#10b981', // emerald green
  '#f59e0b', // amber
  '#ef4444', // red
  '#ec4899', // pink
  '#8b5cf6', // violet
  '#06b6d4', // cyan
]
const COLOR_UNCATEGORIZED = '#64748b'
const COLOR_UNMATCHED = '#94a3b8'

function sliceColor(name: string, index: number): string {
  if (name === 'Uncategorized') return COLOR_UNCATEGORIZED
  if (name === 'Unmatched') return COLOR_UNMATCHED
  return CATEGORY_COLORS[index % CATEGORY_COLORS.length] ?? '#7132f5'
}

function fmt(n: number): string {
  return `$${n.toFixed(2)}`
}

// CenterLabel renders the total inside the donut hole via Recharts customized label.
function CenterLabel({
  viewBox,
  total,
}: {
  viewBox?: { cx: number; cy: number }
  total: number
}) {
  if (!viewBox) return null
  const { cx, cy } = viewBox
  return (
    <g>
      <text
        x={cx}
        y={cy - 8}
        textAnchor="middle"
        dominantBaseline="middle"
        className="fill-neutral-400"
        style={{ fontSize: 11, fontFamily: 'Helvetica Neue, Helvetica, Arial, sans-serif' }}
      >
        30-day total
      </text>
      <text
        x={cx}
        y={cy + 12}
        textAnchor="middle"
        dominantBaseline="middle"
        className="fill-neutral-900"
        style={{ fontSize: 17, fontWeight: 700, fontFamily: 'IBM Plex Sans, Helvetica, Arial, sans-serif' }}
      >
        {fmt(total)}
      </text>
    </g>
  )
}

interface CategoryBreakdownProps {
  data: CategoryBreakdownData
  selectedCategory?: string | null
  onSelect?: (category: string | null) => void
}

/** Pie chart + table of spend by category. Clicking a slice or row toggles `selectedCategory` via `onSelect` (client-side state only — no API call). */
function CategoryBreakdown({ data, selectedCategory = null, onSelect }: CategoryBreakdownProps) {
  const { categories, total } = data

  if (categories.length === 0) {
    return (
      <p className="text-body text-neutral-400 py-8 text-center">
        No spending in the last 30 days yet.
      </p>
    )
  }

  // Build top-3 names for aria-label
  const top3 = categories
    .slice(0, 3)
    .map((c) => `${c.name} ${fmt(c.current)}`)
    .join(', ')
  const ariaLabel = `Category spending donut. Top categories: ${top3}.`

  // Assign consistent index for non-special categories
  let colorIdx = 0
  const coloredCategories = categories.map((cat) => {
    const color = sliceColor(cat.name, colorIdx)
    if (cat.name !== 'Uncategorized' && cat.name !== 'Unmatched') colorIdx++
    return { ...cat, color }
  })

  return (
    <div className="flex flex-col sm:flex-row gap-6 items-start">
      {/* Donut */}
      <div
        className="shrink-0 w-full sm:w-48"
        role="img"
        aria-label={ariaLabel}
      >
        <ResponsiveContainer width="100%" height={192}>
          <PieChart>
            <Pie
              data={coloredCategories}
              dataKey="current"
              nameKey="name"
              cx="50%"
              cy="50%"
              innerRadius={56}
              outerRadius={88}
              paddingAngle={2}
              strokeWidth={0}
              labelLine={false}
              label={<CenterLabel total={total} />}
              style={{ cursor: onSelect ? 'pointer' : undefined }}
              onClick={(entry) => {
                if (!onSelect) return
                const name = entry.name as string
                onSelect(name === selectedCategory ? null : name)
              }}
            >
              {coloredCategories.map((entry) => (
                <Cell
                  key={entry.name}
                  fill={entry.color}
                  stroke={entry.name === selectedCategory ? '#111827' : undefined}
                  strokeWidth={entry.name === selectedCategory ? 2 : 0}
                />
              ))}
            </Pie>
            <Tooltip
              formatter={(value: number, name: string) => [fmt(value), name]}
              contentStyle={{ borderRadius: 10, border: '1px solid #dedee5', fontSize: 13 }}
            />
          </PieChart>
        </ResponsiveContainer>
      </div>

      {/* Legend table */}
      <div className="flex-1 min-w-0 overflow-x-auto">
        <table className="w-full text-caption">
          <thead>
            <tr className="border-b border-neutral-200">
              <th className="text-left font-semibold text-neutral-400 uppercase pb-2 pr-3">Category</th>
              <th className="text-right font-semibold text-neutral-400 uppercase pb-2 px-3">Current</th>
              <th className="text-right font-semibold text-neutral-400 uppercase pb-2 px-3">Share</th>
              <th className="text-right font-semibold text-neutral-400 uppercase pb-2 pl-3">vs. Prior</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-neutral-200">
            {coloredCategories.map((cat) => {
              const deltaSign = cat.delta_pct == null
                ? null
                : cat.delta_pct > 0
                ? 'up'
                : cat.delta_pct < 0
                ? 'down'
                : 'flat'

              const deltaClass =
                deltaSign === 'up'
                  ? 'text-expensive'
                  : deltaSign === 'down'
                  ? 'text-success'
                  : 'text-neutral-400'

              const deltaLabel =
                cat.delta_pct == null
                  ? '—'
                  : deltaSign === 'up'
                  ? `▲ ${cat.delta_pct.toFixed(1)}%`
                  : deltaSign === 'down'
                  ? `▼ ${Math.abs(cat.delta_pct).toFixed(1)}%`
                  : '0%'

              const isSelected = cat.name === selectedCategory
              return (
                <tr
                  key={cat.name}
                  className={`hover:bg-neutral-50 ${isSelected ? 'bg-neutral-100' : ''} ${onSelect ? 'cursor-pointer' : ''}`}
                  onClick={() => {
                    if (!onSelect) return
                    onSelect(cat.name === selectedCategory ? null : cat.name)
                  }}
                >
                  <td className="py-2 pr-3">
                    <span className="inline-flex items-center gap-1.5">
                      <span
                        className="inline-block w-2.5 h-2.5 rounded-full shrink-0"
                        style={{ backgroundColor: cat.color }}
                        aria-hidden="true"
                      />
                      <span className="text-neutral-900 font-medium truncate">{cat.name}</span>
                    </span>
                  </td>
                  <td className="py-2 px-3 text-right text-neutral-900 font-medium tabular-nums">
                    {fmt(cat.current)}
                  </td>
                  <td className="py-2 px-3 text-right text-neutral-500 tabular-nums">
                    {cat.pct_of_total.toFixed(1)}%
                  </td>
                  <td className={`py-2 pl-3 text-right font-medium tabular-nums ${deltaClass}`}>
                    {deltaLabel}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export { CategoryBreakdown }
