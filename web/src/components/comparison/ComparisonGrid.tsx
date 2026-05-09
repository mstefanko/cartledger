import { Link } from 'react-router-dom'
import { Star } from 'lucide-react'
import { Badge } from '@/components/ui/Badge'
import type { CompareAppearance, CompareProduct, CompareReceipt } from '@/api/comparison'

interface ComparisonGridProps {
  receipts: CompareReceipt[]
  products: CompareProduct[]
}

function formatCurrency(value: string | null | undefined): string {
  if (value == null) return '\u2014'
  const num = Number(value)
  if (!Number.isFinite(num)) return '\u2014'
  return `$${num.toFixed(2)}`
}

function formatUnit(unit: string): string {
  return unit.replace(/_/g, ' ')
}

function formatSize(appearance: CompareAppearance): string | null {
  if (appearance.quantity && appearance.unit) {
    return `${appearance.quantity} ${formatUnit(appearance.unit)}`
  }
  if (appearance.quantity) return appearance.quantity
  return null
}

function ReceiptColumnHeader({ receipt }: { receipt: CompareReceipt }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="truncate text-caption font-semibold text-neutral-900">
        {receipt.store_name ?? 'Unknown store'}
      </span>
      <span className="truncate text-small text-neutral-500">
        {receipt.receipt_date} · {formatCurrency(receipt.total)}
      </span>
    </div>
  )
}

function ComparisonCell({
  appearance,
  isBest,
}: {
  appearance?: CompareAppearance
  isBest: boolean
}) {
  if (!appearance) {
    return (
      <span className="text-body text-neutral-300" aria-label="No matching item">
        {'\u2014'}
      </span>
    )
  }

  const size = formatSize(appearance)
  const unitPrice =
    appearance.size_known && appearance.normalized_price && appearance.normalized_unit
      ? `${formatCurrency(appearance.normalized_price)}/${formatUnit(appearance.normalized_unit)}`
      : null

  return (
    <div className="relative min-h-[72px] pr-6">
      {isBest && (
        <Star
          aria-label="Best unit price"
          className="absolute right-0 top-0 h-4 w-4 fill-success text-success"
        />
      )}
      <div className="flex flex-wrap items-baseline gap-x-1 gap-y-0.5">
        <span className="font-medium tabular-nums text-neutral-900">
          {formatCurrency(appearance.total_price)}
        </span>
        {size && (
          <span className="max-w-full break-words text-small text-neutral-500">
            ({size})
          </span>
        )}
      </div>
      {unitPrice ? (
        <div className="mt-1 break-words text-small font-medium tabular-nums text-success-dark">
          {unitPrice}
        </div>
      ) : (
        <Badge variant="warning" className="mt-2">
          size unknown
        </Badge>
      )}
      <div className="mt-1 line-clamp-2 break-words text-small text-neutral-400">
        {appearance.raw_name}
      </div>
    </div>
  )
}

export function ComparisonGrid({ receipts, products }: ComparisonGridProps) {
  return (
    <div className="overflow-x-auto rounded-lg border border-neutral-200 bg-white">
      <table className="min-w-full border-collapse">
        <thead className="bg-neutral-50">
          <tr>
            <th className="sticky left-0 z-20 min-w-[190px] max-w-[220px] bg-neutral-50 px-3 py-2 text-left text-caption font-semibold text-neutral-600 border-b border-r border-neutral-200">
              Product
            </th>
            {receipts.map((receipt) => (
              <th
                key={receipt.id}
                className="min-w-[168px] px-3 py-2 text-left border-b border-neutral-200"
              >
                <ReceiptColumnHeader receipt={receipt} />
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {products.map((product) => {
            const appearancesByReceipt = new Map(
              product.appearances.map((appearance) => [appearance.receipt_id, appearance]),
            )
            return (
              <tr key={product.comparison_key} className="align-top">
                <th className="sticky left-0 z-10 min-w-[190px] max-w-[220px] bg-white px-3 py-3 text-left border-b border-r border-neutral-200">
                  <div className="flex min-w-0 flex-col gap-1">
                    <Link
                      to={`/products/${product.product_id}`}
                      className="break-words text-body-medium font-semibold text-neutral-900 hover:text-brand"
                    >
                      {product.name}
                    </Link>
                    {product.category && (
                      <Badge variant="neutral" className="w-fit">
                        {product.category}
                      </Badge>
                    )}
                    <span className="text-small text-neutral-400">
                      {product.appearances.length} receipt
                      {product.appearances.length === 1 ? '' : 's'}
                    </span>
                  </div>
                </th>
                {receipts.map((receipt) => {
                  const appearance = appearancesByReceipt.get(receipt.id)
                  return (
                    <td
                      key={receipt.id}
                      className="min-w-[168px] px-3 py-3 text-left border-b border-neutral-200"
                    >
                      <ComparisonCell
                        appearance={appearance}
                        isBest={
                          !!appearance &&
                          product.best_appearance_id === appearance.line_item_id
                        }
                      />
                    </td>
                  )
                })}
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
