import { useMemo, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { NavLink, useNavigate } from 'react-router-dom'
import { listReceipts, deleteReceipt } from '@/api/receipts'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { Modal } from '@/components/ui/Modal'
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
  const queryClient = useQueryClient()
  const [deleteTarget, setDeleteTarget] = useState<Receipt | null>(null)

  const { data: receipts, isLoading, error } = useQuery({
    queryKey: ['receipts'],
    queryFn: listReceipts,
  })

  const deleteMutation = useMutation({
    mutationFn: deleteReceipt,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['receipts'] })
      setDeleteTarget(null)
    },
  })

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
                <th className="h-[36px] px-3 py-1 w-10 border-b border-neutral-200"></th>
              </tr>
            </thead>
            <tbody>
              {sortedReceipts.map((receipt) => {
                const config = statusConfig[receipt.status]
                const storeName = receipt.store_name ?? 'Unknown'
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
                    <td className="h-[44px] px-1 py-2 border-b border-neutral-200">
                      <button
                        type="button"
                        className="p-1.5 text-neutral-300 hover:text-red-500 rounded-lg hover:bg-neutral-50 transition-colors cursor-pointer"
                        onClick={(e) => { e.stopPropagation(); setDeleteTarget(receipt) }}
                        aria-label="Delete receipt"
                      >
                        <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
                          <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                        </svg>
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      <Modal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        title="Delete Receipt"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setDeleteTarget(null)}>
              Cancel
            </Button>
            <Button
              className="bg-red-600 text-white hover:bg-red-700"
              size="sm"
              onClick={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </>
        }
      >
        {deleteTarget && (
          <p className="text-body text-neutral-600">
            Delete the receipt from{' '}
            <span className="font-medium">{deleteTarget.store_name ?? 'Unknown'}</span>
            {' '}on {formatDate(deleteTarget.receipt_date)}? This will also remove all line items
            and price history for this receipt. This cannot be undone.
          </p>
        )}
      </Modal>
    </div>
  )
}

export default ReceiptsPage
