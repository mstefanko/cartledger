import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { ReceiptText, SlidersHorizontal, Table2, X } from 'lucide-react'
import { useQuery } from '@tanstack/react-query'
import { useCompareReceipts, type CompareReceipt } from '@/api/comparison'
import { ApiClientError } from '@/api/client'
import { listReceipts } from '@/api/receipts'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { ComparisonGrid } from '@/components/comparison/ComparisonGrid'
import { FullReceiptsView } from '@/components/comparison/FullReceiptsView'
import { ReceiptPickerModal } from '@/components/comparison/ReceiptPickerModal'
import {
  RECEIPT_COMPARE_LIMIT,
  dedupeReceiptIds,
} from '@/components/comparison/receiptSelection'
import type { Receipt } from '@/types'

type ViewMode = 'normalized' | 'full'

type SelectedReceiptMeta = Pick<
  CompareReceipt,
  'id' | 'store_name' | 'receipt_date' | 'total' | 'status'
>

function parseIdsParam(raw: string): { ids: string[]; truncated: boolean } {
  const ids = dedupeReceiptIds(raw.split(','))
  return {
    ids: ids.slice(0, RECEIPT_COMPARE_LIMIT),
    truncated: ids.length > RECEIPT_COMPARE_LIMIT,
  }
}

function formatCurrency(value: string | null | undefined): string {
  if (value == null) return '\u2014'
  const num = Number(value)
  if (!Number.isFinite(num)) return '\u2014'
  return `$${num.toFixed(2)}`
}

function compareErrorMessage(error: unknown): string {
  if (error instanceof ApiClientError) {
    if (error.status === 404) return 'One or more receipts could not be found.'
    if (error.status === 409) return 'One or more receipts are no longer comparable.'
    return error.message
  }
  return 'Failed to compare receipts.'
}

function ReceiptChip({
  receipt,
  onRemove,
}: {
  receipt: SelectedReceiptMeta
  onRemove: () => void
}) {
  return (
    <span className="inline-flex max-w-full items-center gap-2 rounded-lg border border-neutral-200 bg-white px-2.5 py-1.5 shadow-micro">
      <span className="min-w-0">
        <span className="block max-w-[18ch] truncate text-caption font-medium text-neutral-900">
          {receipt.store_name ?? 'Unknown store'}
        </span>
        <span className="block truncate text-small text-neutral-500">
          {receipt.receipt_date} · {formatCurrency(receipt.total)}
        </span>
      </span>
      <button
        type="button"
        className="rounded-md p-1 text-neutral-400 hover:bg-neutral-50 hover:text-neutral-900"
        aria-label={`Remove receipt from ${receipt.store_name ?? 'Unknown store'} on ${receipt.receipt_date}`}
        onClick={onRemove}
      >
        <X className="h-3.5 w-3.5" />
      </button>
    </span>
  )
}

function EmptyComparison({ onChangeReceipts }: { onChangeReceipts: () => void }) {
  return (
    <div className="rounded-lg border border-neutral-200 bg-neutral-50 px-6 py-12 text-center">
      <p className="text-body-medium text-neutral-900">Choose at least two receipts.</p>
      <div className="mt-4">
        <Button size="sm" onClick={onChangeReceipts}>
          Change receipts
        </Button>
      </div>
    </div>
  )
}

function ReceiptComparisonPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const rawIds = searchParams.get('ids') ?? ''
  const urlTruncated = searchParams.get('truncated') === '1'
  const parsed = useMemo(() => parseIdsParam(rawIds), [rawIds])
  const [selectedIds, setSelectedIds] = useState<string[]>(parsed.ids)
  const [viewMode, setViewMode] = useState<ViewMode>('normalized')
  const [minOverlap, setMinOverlap] = useState(2)
  const [pickerOpen, setPickerOpen] = useState(false)
  const [notice, setNotice] = useState<string | null>(
    parsed.truncated || urlTruncated
      ? `Only the first ${RECEIPT_COMPARE_LIMIT} receipts are being compared.`
      : null,
  )

  useEffect(() => {
    setSelectedIds(parsed.ids)
    if (parsed.truncated || urlTruncated) {
      setNotice(`Only the first ${RECEIPT_COMPARE_LIMIT} receipts are being compared.`)
      if (parsed.truncated) {
        const params = new URLSearchParams(searchParams)
        params.set('ids', parsed.ids.join(','))
        params.set('truncated', '1')
        setSearchParams(params, { replace: true })
      }
    }
  }, [parsed.ids, parsed.truncated, searchParams, setSearchParams, urlTruncated])

  const effectiveMinOverlap =
    selectedIds.length >= 2 ? Math.min(minOverlap, selectedIds.length) : 2

  useEffect(() => {
    if (selectedIds.length >= 2 && minOverlap > selectedIds.length) {
      setMinOverlap(selectedIds.length)
    }
  }, [minOverlap, selectedIds.length])

  const { data: allReceipts = [] } = useQuery({
    queryKey: ['receipts'],
    queryFn: listReceipts,
  })

  const compareQuery = useCompareReceipts(selectedIds, effectiveMinOverlap)
  const compare = compareQuery.data

  const receiptMeta = useMemo(() => {
    const map = new Map<string, SelectedReceiptMeta>()
    for (const receipt of allReceipts) {
      map.set(receipt.id, receipt)
    }
    for (const receipt of compare?.receipts ?? []) {
      map.set(receipt.id, receipt)
    }
    return selectedIds
      .map((id) => map.get(id))
      .filter((receipt): receipt is SelectedReceiptMeta => !!receipt)
  }, [allReceipts, compare?.receipts, selectedIds])

  const overlapOptions = useMemo(() => {
    if (selectedIds.length < 2) return []
    return Array.from({ length: selectedIds.length - 1 }, (_unused, index) => index + 2)
  }, [selectedIds.length])

  const hasComparableRows =
    (compare?.products ?? []).some(
      (product) => product.comparable_unit != null && product.best_appearance_id != null,
    )

  function updateSelectedIds(ids: string[], truncated = false) {
    const next = dedupeReceiptIds(ids).slice(0, RECEIPT_COMPARE_LIMIT)
    setSelectedIds(next)
    const params = new URLSearchParams()
    if (next.length > 0) params.set('ids', next.join(','))
    if (truncated) params.set('truncated', '1')
    setSearchParams(params)
    setNotice(
      truncated ? `Only the first ${RECEIPT_COMPARE_LIMIT} receipts are being compared.` : null,
    )
  }

  function removeReceipt(id: string) {
    updateSelectedIds(selectedIds.filter((selectedId) => selectedId !== id))
  }

  return (
    <div className="py-6">
      <div className="mb-4 flex items-center justify-between gap-3">
        <Link to="/receipts" className="text-caption text-brand hover:underline">
          {'\u2190'} Back to Receipts
        </Link>
        <Button size="sm" variant="outlined" onClick={() => setPickerOpen(true)}>
          Change receipts
        </Button>
      </div>

      <div className="mb-5 flex flex-col gap-4">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <h1 className="font-display text-subhead font-bold tracking-tight text-neutral-900">
              Receipt Comparison
            </h1>
            <div className="mt-2 flex flex-wrap gap-2">
              {receiptMeta.map((receipt) => (
                <ReceiptChip
                  key={receipt.id}
                  receipt={receipt}
                  onRemove={() => removeReceipt(receipt.id)}
                />
              ))}
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <label className="inline-flex items-center gap-2 rounded-lg border border-neutral-200 bg-white px-3 py-2 text-caption text-neutral-700">
              <SlidersHorizontal className="h-4 w-4 text-neutral-400" />
              Min overlap
              <select
                value={effectiveMinOverlap}
                disabled={selectedIds.length < 2}
                onChange={(event) => setMinOverlap(Number(event.target.value))}
                className="rounded-md border border-neutral-200 bg-white px-2 py-1 text-caption text-neutral-900 focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
              >
                {overlapOptions.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </label>

            <div className="inline-flex rounded-lg border border-neutral-200 bg-white p-1">
              <button
                type="button"
                className={[
                  'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-caption font-medium',
                  viewMode === 'normalized'
                    ? 'bg-brand text-white'
                    : 'text-neutral-600 hover:bg-neutral-50',
                ].join(' ')}
                onClick={() => setViewMode('normalized')}
              >
                <Table2 className="h-4 w-4" />
                Normalized
              </button>
              <button
                type="button"
                className={[
                  'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-caption font-medium',
                  viewMode === 'full'
                    ? 'bg-brand text-white'
                    : 'text-neutral-600 hover:bg-neutral-50',
                ].join(' ')}
                onClick={() => setViewMode('full')}
              >
                <ReceiptText className="h-4 w-4" />
                Full
              </button>
            </div>
          </div>
        </div>

        {notice && (
          <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-caption text-amber-900">
            {notice}
          </div>
        )}
      </div>

      {selectedIds.length < 2 ? (
        <EmptyComparison onChangeReceipts={() => setPickerOpen(true)} />
      ) : compareQuery.isLoading ? (
        <div className="rounded-lg border border-neutral-200 bg-neutral-50 px-6 py-12 text-center text-body text-neutral-400">
          Loading comparison...
        </div>
      ) : compareQuery.isError ? (
        <div className="rounded-lg border border-expensive/30 bg-expensive-subtle px-4 py-3 text-body text-expensive">
          {compareErrorMessage(compareQuery.error)}
        </div>
      ) : compare ? (
        <div className="flex flex-col gap-4">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="neutral">{compare.products.length} overlapping products</Badge>
            {compare.missing_unit_count > 0 && (
              <Badge variant="warning">{compare.missing_unit_count} size unknown</Badge>
            )}
            {compare.products.length > 0 && !hasComparableRows && (
              <Badge variant="warning">no comparable unit prices</Badge>
            )}
          </div>

          {compare.products.length === 0 ? (
            <div className="rounded-lg border border-neutral-200 bg-neutral-50 px-6 py-12 text-center text-body text-neutral-500">
              No products overlap at the current minimum.
            </div>
          ) : viewMode === 'normalized' ? (
            <ComparisonGrid receipts={compare.receipts} products={compare.products} />
          ) : (
            <FullReceiptsView receipts={compare.receipts} />
          )}
        </div>
      ) : null}

      <ReceiptPickerModal
        open={pickerOpen}
        receipts={allReceipts as Receipt[]}
        selectedIds={selectedIds}
        onClose={() => setPickerOpen(false)}
        onConfirm={(ids) => {
          updateSelectedIds(ids)
          setPickerOpen(false)
        }}
      />
    </div>
  )
}

export default ReceiptComparisonPage
