import { Link } from 'react-router-dom'
import { Sparkline } from '@/components/ui/Sparkline'
import type { Staple } from '@/types'

interface StaplesTableProps {
  staples: Staple[]
}

const EM_DASH = '—'

function formatCurrency(n: number): string {
  return `$${n.toFixed(2)}`
}

function formatProjection(n: number | null): string {
  if (n == null) return EM_DASH
  return formatCurrency(n)
}

function formatCadence(days: number): string {
  // cadence_days is already rounded to 1 decimal by the server; trim trailing .0
  const rounded = Math.round(days * 10) / 10
  return rounded === Math.trunc(rounded)
    ? `every ${Math.trunc(rounded)} days`
    : `every ${rounded.toFixed(1)} days`
}

function StaplesTable({ staples }: StaplesTableProps) {
  if (!staples || staples.length === 0) {
    return (
      <div className="bg-white rounded-2xl border border-neutral-200 p-8 text-center">
        <p className="text-body text-neutral-400">
          No staples yet. Scan a few more receipts and we'll spot your regulars.
        </p>
      </div>
    )
  }

  // Derive "months of history" from the first row that has projections.
  // weekly_spend is populated iff weekly = total_spent / (span_days/7), and
  // spanDays = total_spent / (weekly/7). Rather than reverse-engineer that,
  // we use a conservative heuristic: projections exist ⇒ span >= 60d ⇒ >= 2mo.
  // If *no* row has projections, hide the footer entirely (keeps copy honest).
  const anyProjections = staples.some((s) => s.weekly_spend != null)
  const footerMonths = (() => {
    if (!anyProjections) return null
    // Approximate span from the row with the largest total_spent and a
    // non-null weekly_spend: span ≈ (total_spent / weekly_spend) * 7.
    const row = staples.find(
      (s) => s.weekly_spend != null && s.weekly_spend > 0 && s.total_spent > 0
    )
    if (!row || row.weekly_spend == null) return null
    const spanDays = (row.total_spent / row.weekly_spend) * 7
    return Math.max(2, Math.floor(spanDays / 30))
  })()

  const projectionTitle = anyProjections
    ? undefined
    : 'Need more history to project spending'

  return (
    <div className="bg-white rounded-2xl border border-neutral-200 overflow-hidden">
      <div className="overflow-x-auto">
        <table className="min-w-[900px] w-full">
          <thead>
            <tr className="border-b border-neutral-200 bg-neutral-50">
              <th className="text-left text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Product
              </th>
              <th className="text-left text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Category
              </th>
              <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Bought
              </th>
              <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Cadence
              </th>
              <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Total
              </th>
              <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Avg
              </th>
              <th className="text-left text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Sparkline
              </th>
              <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Weekly
              </th>
              <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Monthly
              </th>
              <th className="text-right text-caption font-semibold text-neutral-400 uppercase px-4 py-3">
                Yearly
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-neutral-200">
            {staples.map((s) => (
              <tr key={s.product_id} className="hover:bg-neutral-50">
                <td className="px-4 py-3">
                  <Link
                    to={`/products/${s.product_id}`}
                    className="text-body text-brand hover:underline"
                  >
                    {s.name}
                  </Link>
                </td>
                <td className="px-4 py-3 text-body text-neutral-600">
                  {s.category || <span className="text-neutral-400">{EM_DASH}</span>}
                </td>
                <td className="px-4 py-3 text-right text-body font-medium text-neutral-900">
                  {s.times_bought}
                </td>
                <td className="px-4 py-3 text-right text-small text-neutral-600 whitespace-nowrap">
                  {formatCadence(s.cadence_days)}
                </td>
                <td className="px-4 py-3 text-right text-body font-medium text-neutral-900">
                  {formatCurrency(s.total_spent)}
                </td>
                <td className="px-4 py-3 text-right text-body text-neutral-600">
                  {formatCurrency(s.avg_price)}
                </td>
                <td className="px-4 py-3">
                  {s.sparkline_points.length >= 2 ? (
                    <Sparkline data={s.sparkline_points} />
                  ) : (
                    <span className="text-small text-neutral-400">{EM_DASH}</span>
                  )}
                </td>
                <td
                  className="px-4 py-3 text-right text-body text-neutral-600"
                  title={s.weekly_spend == null ? projectionTitle : undefined}
                >
                  {formatProjection(s.weekly_spend)}
                </td>
                <td
                  className="px-4 py-3 text-right text-body text-neutral-600"
                  title={s.monthly_spend == null ? projectionTitle : undefined}
                >
                  {formatProjection(s.monthly_spend)}
                </td>
                <td
                  className="px-4 py-3 text-right text-body font-medium text-neutral-900"
                  title={s.yearly_projection == null ? projectionTitle : undefined}
                >
                  {formatProjection(s.yearly_projection)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {footerMonths != null && (
        <div className="border-t border-neutral-100 px-4 py-2 text-small text-neutral-400">
          Projections based on {footerMonths} {footerMonths === 1 ? 'month' : 'months'} of history
        </div>
      )}
    </div>
  )
}

export { StaplesTable }
export default StaplesTable
