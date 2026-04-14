import { useState, useMemo, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { type ColumnDef } from '@tanstack/react-table'
import { EditableTable, type AutocompleteOption } from '@/components/ui/EditableTable'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
// CreateRuleModal replaced by inline batch rule modal
import { getReceipt, updateLineItem, type ReceiptDetail } from '@/api/receipts'
import { listProducts } from '@/api/products'
import { matchLineItem } from '@/api/matching'
import type { LineItem, Product } from '@/types'

interface ReceiptReviewProps {
  receiptId: string
  onScrollToImage?: () => void
}

/** Row data for the editable table — extends LineItem with resolved product name */
interface LineItemRow extends LineItem {
  product_name: string
}

function ReceiptReview({ receiptId, onScrollToImage }: ReceiptReviewProps) {
  const queryClient = useQueryClient()

  // --- Data fetching ---
  const {
    data: receipt,
    isLoading,
    isError,
  } = useQuery<ReceiptDetail>({
    queryKey: ['receipt', receiptId],
    queryFn: () => getReceipt(receiptId),
  })

  const [productSearch, setProductSearch] = useState('')

  const { data: products = [] } = useQuery<Product[]>({
    queryKey: ['products', productSearch],
    queryFn: () => listProducts({ search: productSearch }),
    enabled: productSearch.length > 0,
  })

  // --- Mutations ---
  const matchMutation = useMutation({
    mutationFn: ({
      lineItemId,
      productId,
    }: {
      lineItemId: string
      productId: string
    }) => matchLineItem(lineItemId, { product_id: productId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({
      itemId,
      field,
      value,
    }: {
      itemId: string
      field: string
      value: string
    }) => {
      const payload: Record<string, string> = { [field]: value }
      return updateLineItem(receiptId, itemId, payload)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
    },
  })

  const confirmMutation = useMutation({
    mutationFn: () => {
      // Mark all unmatched items as reviewed by confirming the receipt
      // The backend PUT /receipts/:id with status: 'reviewed'
      return import('@/api/client').then((client) =>
        client.put<ReceiptDetail>(
          `/receipts/${encodeURIComponent(receiptId)}`,
          { status: 'reviewed' },
        ),
      )
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
    },
  })

  // --- Pending matches for batch rule creation ---
  const [pendingRuleMatches, setPendingRuleMatches] = useState<
    { rawName: string; productName: string; productId: string; selected: boolean }[]
  >([])
  const [batchRuleModalOpen, setBatchRuleModalOpen] = useState(false)

  // --- Raw JSON modal ---
  const [rawJsonOpen, setRawJsonOpen] = useState(false)

  // --- Build product lookup map ---
  const productMap = useMemo(() => {
    const map = new Map<string, string>()
    for (const p of products) {
      map.set(p.id, p.name)
    }
    return map
  }, [products])

  // --- Build autocomplete options ---
  const autocompleteOptions: AutocompleteOption[] = useMemo(
    () => products.map((p) => ({ id: p.id, label: p.name })),
    [products],
  )

  // --- Enrich line items with product names ---
  const rows: LineItemRow[] = useMemo(() => {
    if (!receipt) return []
    return receipt.line_items.map((li) => ({
      ...li,
      product_name: li.product_id ? productMap.get(li.product_id) ?? '' : '',
    }))
  }, [receipt, productMap])

  // --- Status counts ---
  const matchedCount = useMemo(
    () => rows.filter((r) => r.matched !== 'unmatched').length,
    [rows],
  )
  const needsReviewCount = useMemo(
    () => rows.filter((r) => r.matched === 'unmatched').length,
    [rows],
  )

  // --- Cell update handler ---
  const handleCellUpdate = useCallback(
    (rowIndex: number, columnId: string, value: string) => {
      const row = rows[rowIndex]
      if (!row) return

      if (columnId === 'product_id') {
        // This is a product match via autocomplete
        matchMutation.mutate(
          { lineItemId: row.id, productId: value },
          {
            onSuccess: () => {
              const matchedProduct = products.find((p) => p.id === value)
              if (matchedProduct) {
                setPendingRuleMatches((prev) => {
                  // Avoid duplicates for the same rawName + productId
                  if (prev.some((m) => m.rawName === row.raw_name && m.productId === value)) {
                    return prev
                  }
                  return [...prev, {
                    rawName: row.raw_name,
                    productName: matchedProduct.name,
                    productId: value,
                    selected: true,
                  }]
                })
              }
            },
          },
        )
        return
      }

      // For other editable fields: quantity, unit, total_price
      updateMutation.mutate({ itemId: row.id, field: columnId, value })
    },
    [rows, matchMutation, updateMutation, products],
  )

  // --- Handle autocomplete "create new" ---
  const handleAutocompleteCreate = useCallback(
    (rowIndex: number, _columnId: string, label: string) => {
      // Create product then match
      import('@/api/products').then((mod) => {
        mod.createProduct({ name: label }).then((newProduct) => {
          const row = rows[rowIndex]
          if (!row) return
          matchMutation.mutate(
            { lineItemId: row.id, productId: newProduct.id },
            {
              onSuccess: () => {
                queryClient.invalidateQueries({ queryKey: ['products'] })
              },
            },
          )
        })
      })
    },
    [rows, matchMutation, queryClient],
  )

  // --- Table columns ---
  const columns: ColumnDef<LineItemRow, unknown>[] = useMemo(
    () => [
      {
        id: 'status',
        header: '',
        size: 40,
        cell: ({ row }) => {
          const item = row.original
          if (item.matched !== 'unmatched') {
            return (
              <span className="flex items-center justify-center" title="Matched">
                <svg
                  className="w-4 h-4 text-success-dark"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  strokeWidth={2.5}
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    d="M5 13l4 4L19 7"
                  />
                </svg>
              </span>
            )
          }
          return (
            <span
              className="flex items-center justify-center"
              title="Needs review"
            >
              <svg
                className="w-4 h-4 text-expensive"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth={2.5}
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M12 9v2m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
                />
              </svg>
            </span>
          )
        },
      },
      {
        accessorKey: 'raw_name',
        header: 'Receipt Text',
        size: 200,
      },
      {
        accessorKey: 'product_id',
        header: 'Product',
        size: 220,
        meta: {
          editable: true,
          cellType: 'autocomplete' as const,
          autocompleteOptions,
          onAutocompleteSearch: setProductSearch,
          onAutocompleteCreate: handleAutocompleteCreate,
          getDisplayValue: (value: unknown) => {
            const id = value as string | null
            if (!id) return ''
            return productMap.get(id) ?? ''
          },
        },
      },
      {
        accessorKey: 'quantity',
        header: 'Qty',
        size: 70,
        meta: {
          editable: true,
          cellType: 'number' as const,
        },
      },
      {
        accessorKey: 'unit',
        header: 'Unit',
        size: 80,
        meta: {
          editable: true,
          cellType: 'text' as const,
        },
      },
      {
        accessorKey: 'total_price',
        header: 'Price',
        size: 90,
        meta: {
          editable: true,
          cellType: 'text' as const,
        },
        cell: ({ row }) => {
          const price = row.original.total_price
          const formatted = price != null ? '$' + Number(price).toFixed(2) : '\u2014'
          return <span className="tabular-nums">{formatted}</span>
        },
      },
    ],
    [autocompleteOptions, productMap, handleAutocompleteCreate],
  )

  // --- Row class names for unmatched highlighting ---
  const getRowClassName = useCallback(
    (row: LineItemRow) => (row.matched === 'unmatched' ? 'bg-expensive-subtle/30' : ''),
    [],
  )

  // --- Loading & error states ---
  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-16">
        <p className="text-body text-neutral-400">Loading receipt...</p>
      </div>
    )
  }

  if (isError || !receipt) {
    return (
      <div className="flex items-center justify-center py-16">
        <p className="text-body text-expensive">
          Failed to load receipt. Please try again.
        </p>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {/* Status bar */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Badge variant="success">{matchedCount} matched</Badge>
          {needsReviewCount > 0 && (
            <Badge variant="warning">{needsReviewCount} need review</Badge>
          )}
          <span className="text-caption text-neutral-400">
            {receipt.status === 'reviewed' ? 'Reviewed' : receipt.status}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {onScrollToImage && (
            <Button variant="secondary" size="sm" onClick={onScrollToImage}>
              View Original Receipt
            </Button>
          )}
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setRawJsonOpen(true)}
          >
            View Raw JSON
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={() => {
              if (pendingRuleMatches.length > 0 && receipt.status !== 'reviewed') {
                setBatchRuleModalOpen(true)
              } else {
                confirmMutation.mutate()
              }
            }}
            disabled={confirmMutation.isPending || receipt.status === 'reviewed'}
          >
            {confirmMutation.isPending
              ? 'Confirming...'
              : receipt.status === 'reviewed'
                ? 'Confirmed'
                : 'Confirm All'}
          </Button>
        </div>
      </div>

      {/* Editable line items table */}
      <EditableTable<LineItemRow>
        columns={columns}
        data={rows}
        onCellUpdate={handleCellUpdate}
        getRowClassName={getRowClassName}
        virtualizeRows={rows.length > 50}
      />

      {/* Raw JSON modal */}
      <Modal
        open={rawJsonOpen}
        onClose={() => setRawJsonOpen(false)}
        title="Raw LLM JSON"
      >
        <pre className="text-small font-mono text-neutral-900 bg-neutral-50 rounded-lg p-4 overflow-auto max-h-[60vh] whitespace-pre-wrap break-words">
          {receipt.raw_llm_json
            ? JSON.stringify(JSON.parse(receipt.raw_llm_json), null, 2)
            : 'No raw JSON available'}
        </pre>
      </Modal>

      {/* Batch rule creation modal — shown after Confirm All */}
      <Modal
        open={batchRuleModalOpen}
        onClose={() => {
          setBatchRuleModalOpen(false)
          setPendingRuleMatches([])
          confirmMutation.mutate()
        }}
        title="Create Auto-Match Rules"
        footer={
          <>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setBatchRuleModalOpen(false)
                setPendingRuleMatches([])
                confirmMutation.mutate()
              }}
            >
              Skip
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={() => {
                const selected = pendingRuleMatches.filter((m) => m.selected)
                // Create rules for selected matches then confirm
                Promise.all(
                  selected.map((m) =>
                    import('@/api/matching').then((mod) =>
                      mod.createRule({
                        condition_op: 'exact',
                        condition_val: m.rawName,
                        product_id: m.productId,
                        store_id: receipt.store_id ?? undefined,
                      }),
                    ),
                  ),
                ).finally(() => {
                  setBatchRuleModalOpen(false)
                  setPendingRuleMatches([])
                  confirmMutation.mutate()
                })
              }}
            >
              Create Selected Rules & Confirm
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-900 mb-3">
          You matched {pendingRuleMatches.length} new{' '}
          {pendingRuleMatches.length === 1 ? 'item' : 'items'}. Create
          auto-match rules for future receipts?
        </p>
        <div className="flex flex-col gap-2">
          {pendingRuleMatches.map((match, idx) => (
            <label
              key={`${match.rawName}-${match.productId}`}
              className="flex items-center gap-3 px-3 py-2 rounded-lg bg-neutral-50 cursor-pointer hover:bg-neutral-200/40"
            >
              <input
                type="checkbox"
                checked={match.selected}
                onChange={() =>
                  setPendingRuleMatches((prev) =>
                    prev.map((m, i) =>
                      i === idx ? { ...m, selected: !m.selected } : m,
                    ),
                  )
                }
                className="w-4 h-4 accent-brand"
              />
              <span className="text-caption text-neutral-600">
                &ldquo;{match.rawName}&rdquo;
              </span>
              <span className="text-caption text-neutral-400 mx-1">&rarr;</span>
              <span className="text-caption font-medium text-brand">
                {match.productName}
              </span>
            </label>
          ))}
        </div>
      </Modal>
    </div>
  )
}

ReceiptReview.displayName = 'ReceiptReview'

export { ReceiptReview }
export type { ReceiptReviewProps }
