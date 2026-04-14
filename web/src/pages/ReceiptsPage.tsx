import { useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { NavLink, useNavigate } from 'react-router-dom'
import { listReceipts } from '@/api/receipts'
import { listStores } from '@/api/stores'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import type { Receipt } from '@/types'
import type { BadgeVariant } from '@/components/ui/Badge'

const statusConfig: Record<Receipt['status'], { label: string; variant: BadgeVariant }> = {
  pending: { label: 'Pending', variant: 'warning' },
  matched: { label: 'Matched', variant: 'neutral' },
  reviewed: { label: 'Reviewed', variant: 'success' },
  processing: { label: 'Processing', variant: 'neutral' },
  error: { label: 'Error', variant: 'error' },
}

function ReceiptsPage() {
  const navigate = useNavigate()

  const { data: receipts, isLoading, error } = useQuery({
    queryKey: ['receipts'],
    queryFn: listReceipts,
  })

  const { data: stores } = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  const storeMap = useMemo(() => {
    const map = new Map<string, string>()
    if (stores) {
      for (const store of stores) {
        map.set(store.id, store.name)
      }
    }
    return map
  }, [stores])

  const sortedReceipts = useMemo(() => {
    if (!receipts) return []
    return [...receipts].sort(
      (a, b) => new Date(b.receipt_date).getTime() - new Date(a.receipt_date).getTime(),
    )
  }, [receipts])

  function formatDate(dateStr: string): string {
    const date = new Date(dateStr)
    return date.toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })
  }

  function formatCurrency(value: string | null): string {
    if (!value) return '\u2014'
    const num = parseFloat(value)
    if (isNaN(num)) return '\u2014'
    return `$${num.toFixed(2)}`
  }

  if (error) {
    return (
      <div className="py-8">
        <p className="text-body text-red-600">Failed to load receipts.</p>
      </div>
    )
  }

  return (
    <div className="py-8">
      <div className="flex items-center justify-between mb-6">
        <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight">
          Receipts
        </h1>
        <NavLink to="/scan">
          <Button size="sm">Scan Receipt</Button>
        </NavLink>
      </div>

      {isLoading ? (
        <p className="text-body text-neutral-400">Loading receipts...</p>
      ) : sortedReceipts.length === 0 ? (
        <div className="text-center py-16">
          <p className="text-body text-neutral-400">No receipts yet.</p>
          <div className="mt-4">
            <NavLink to="/scan">
              <Button>Scan Your First Receipt</Button>
            </NavLink>
          </div>
        </div>
      ) : (
        <div className="overflow-auto border border-neutral-200 rounded-lg">
          <table className="w-full border-collapse">
            <thead className="bg-neutral-50">
              <tr>
                <th className="h-[36px] px-3 py-1 text-caption font-semibold text-neutral-600 text-left border-b border-neutral-200">
                  Store
                </th>
                <th className="h-[36px] px-3 py-1 text-caption font-semibold text-neutral-600 text-left border-b border-neutral-200">
                  Date
                </th>
                <th className="h-[36px] px-3 py-1 text-caption font-semibold text-neutral-600 text-right border-b border-neutral-200">
                  Total
                </th>
                <th className="h-[36px] px-3 py-1 text-caption font-semibold text-neutral-600 text-center border-b border-neutral-200">
                  Status
                </th>
              </tr>
            </thead>
            <tbody>
              {sortedReceipts.map((receipt) => {
                const config = statusConfig[receipt.status]
                const storeName = receipt.store_id ? storeMap.get(receipt.store_id) ?? 'Unknown' : 'Unknown'
                return (
                  <tr
                    key={receipt.id}
                    onClick={() => navigate(`/receipts/${receipt.id}`)}
                    className="hover:bg-neutral-50 transition-colors cursor-pointer"
                  >
                    <td className="h-[44px] px-3 py-2 text-body text-neutral-900 border-b border-neutral-200">
                      {storeName}
                    </td>
                    <td className="h-[44px] px-3 py-2 text-body text-neutral-600 border-b border-neutral-200">
                      {formatDate(receipt.receipt_date)}
                    </td>
                    <td className="h-[44px] px-3 py-2 text-body text-neutral-900 text-right font-medium border-b border-neutral-200">
                      {formatCurrency(receipt.total)}
                    </td>
                    <td className="h-[44px] px-3 py-2 text-center border-b border-neutral-200">
                      <Badge variant={config.variant}>{config.label}</Badge>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export default ReceiptsPage
