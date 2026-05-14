import { useMemo } from 'react'
import { useQueries } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { getReceipt, type ReceiptDetail } from '@/api/receipts'
import type { CompareReceipt } from '@/api/comparison'
import type { LineItem } from '@/types'

interface FullReceiptsViewProps {
  receipts: CompareReceipt[]
}

function formatCurrency(value: string | null | undefined): string {
  if (value == null) return '\u2014'
  const num = Number(value)
  if (!Number.isFinite(num)) return '\u2014'
  return `$${num.toFixed(2)}`
}

function formatPurchased(item: LineItem): string {
  const unit = item.unit?.replace(/_/g, ' ')
  if (item.quantity && unit) return `${item.quantity} ${unit}`
  if (item.quantity) return item.quantity
  return '\u2014'
}

function otherReceiptCount(item: LineItem, currentReceiptID: string, receipts: ReceiptDetail[]): number {
  if (!item.product_id) return 0
  return receipts.filter((receipt) => {
    if (receipt.id === currentReceiptID) return false
    return receipt.line_items.some((lineItem) => lineItem.product_id === item.product_id)
  }).length
}

export function FullReceiptsView({ receipts }: FullReceiptsViewProps) {
  const queries = useQueries({
    queries: receipts.map((receipt) => ({
      queryKey: ['receipt', receipt.id],
      queryFn: () => getReceipt(receipt.id),
    })),
  })

  const loadedReceipts = useMemo(
    () => queries.map((query) => query.data).filter((receipt): receipt is ReceiptDetail => !!receipt),
    [queries],
  )

  return (
    <div className="overflow-x-auto pb-3">
      <div className="flex min-w-max gap-4">
        {receipts.map((summary, index) => {
          const query = queries[index]
          const receipt = query?.data
          return (
            <section
              key={summary.id}
              className="min-w-[300px] max-w-[360px] rounded-lg border border-neutral-200 bg-white shadow-micro"
            >
              <div className="border-b border-neutral-200 px-4 py-3">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="truncate text-body-medium font-semibold text-neutral-900">
                      {summary.store_name ?? 'Unknown store'}
                    </p>
                    <p className="text-small text-neutral-500">
                      {summary.receipt_date}
                    </p>
                  </div>
                  <Link
                    to={`/receipts/${summary.id}`}
                    className="shrink-0 text-small font-medium text-brand hover:underline"
                  >
                    Open
                  </Link>
                </div>
              </div>

              {query?.isLoading ? (
                <div className="px-4 py-10 text-center text-caption text-neutral-400">
                  Loading receipt...
                </div>
              ) : query?.isError || !receipt ? (
                <div className="px-4 py-10 text-center text-caption text-expensive">
                  Failed to load receipt.
                </div>
              ) : (
                <>
                  <div className="max-h-[560px] overflow-y-auto">
                    {receipt.line_items.map((item) => {
                      const others = otherReceiptCount(item, receipt.id, loadedReceipts)
                      return (
                        <div
                          key={item.id}
                          className="border-b border-neutral-200 px-4 py-3 last:border-b-0"
                        >
                          <div className="flex items-start justify-between gap-3">
                            <div className="min-w-0">
                              <p className="break-words text-caption font-medium text-neutral-900">
                                {item.product_name ?? item.raw_name}
                              </p>
                              <p className="mt-0.5 break-words text-small text-neutral-400">
                                {item.raw_name}
                              </p>
                            </div>
                            <span className="shrink-0 text-caption font-semibold tabular-nums text-neutral-900">
                              {formatCurrency(item.total_price)}
                            </span>
                          </div>
                          <div className="mt-2 flex flex-wrap items-center gap-2">
                            <span className="rounded-md bg-neutral-50 px-2 py-0.5 text-small text-neutral-600">
                              Purchased {formatPurchased(item)}
                            </span>
                            {others > 0 && (
                              <span className="rounded-md bg-success-subtle px-2 py-0.5 text-small font-medium text-success-dark">
                                +{others} other{others === 1 ? '' : 's'}
                              </span>
                            )}
                          </div>
                        </div>
                      )
                    })}
                  </div>
                  <div className="border-t border-neutral-200 px-4 py-3">
                    <div className="flex items-center justify-between text-caption text-neutral-600">
                      <span>{receipt.line_items.length} line items</span>
                      <span className="font-semibold tabular-nums text-neutral-900">
                        {formatCurrency(receipt.total)}
                      </span>
                    </div>
                  </div>
                </>
              )}
            </section>
          )
        })}
      </div>
    </div>
  )
}
