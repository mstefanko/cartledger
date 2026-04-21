import type { Savings } from '@/types'

interface SavingsCardProps {
  data: Savings | null
}

function SavingsCard({ data }: SavingsCardProps) {
  if (!data) return null

  const allZero = data.month_to_date === 0 && data.last_30d === 0 && data.year_to_date === 0
  const mtdZero = data.month_to_date === 0
  const ytdNonZero = data.year_to_date > 0

  return (
    <div className="bg-white rounded-2xl border border-neutral-200 p-6">
      {/* Hero Container — primary savings amount with hierarchy */}
      <div className="flex flex-col gap-6">
        {/* Primary Amount Section */}
        <div className="flex items-baseline gap-2">
          <div className="flex flex-col gap-1">
            <span className="font-display text-xs font-medium text-neutral-400 uppercase tracking-wider">
              You've Saved
            </span>
            <span className="font-display text-section font-semibold text-neutral-900">
              ${data.month_to_date.toFixed(2)}
            </span>
            <span className="text-xs text-neutral-400">this month</span>
          </div>

          {/* Accent vertical line */}
          <div className="w-0.5 h-16 bg-gradient-to-b from-neutral-200 via-emerald-300 to-neutral-200 ml-4" />

          {/* Secondary Metrics — right column */}
          <div className="flex flex-col gap-3 ml-2">
            <div className="flex items-baseline gap-2">
              <span className="text-body text-neutral-600">Last 30 days</span>
              <span className="font-semibold text-neutral-900 ml-auto">
                ${data.last_30d.toFixed(2)}
              </span>
            </div>
            <div className="flex items-baseline gap-2">
              <span className="text-body text-neutral-600">This year</span>
              <span className="font-semibold text-neutral-900 ml-auto">
                ${data.year_to_date.toFixed(2)}
              </span>
            </div>
          </div>
        </div>

        {/* Zero-state messaging — nuanced copy */}
        {allZero && (
          <div className="text-small text-neutral-400 border-t border-neutral-100 pt-4">
            No discounts recorded yet. Start scanning receipts to see your savings add up.
          </div>
        )}

        {mtdZero && ytdNonZero && (
          <div className="text-small text-neutral-400 border-t border-neutral-100 pt-4">
            No discounts this month, but you've saved {' '}
            <span className="font-semibold text-neutral-900">
              ${data.year_to_date.toFixed(2)}
            </span>
            {' '}
            so far this year.
          </div>
        )}
      </div>
    </div>
  )
}

export default SavingsCard
