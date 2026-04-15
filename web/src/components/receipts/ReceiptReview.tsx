import { useState, useMemo, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { type ColumnDef } from '@tanstack/react-table'
import { EditableTable, type AutocompleteOption } from '@/components/ui/EditableTable'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
// CreateRuleModal replaced by inline batch rule modal
import { getReceipt, updateLineItem, acceptSuggestions, confirmReceipt, type ReceiptDetail } from '@/api/receipts'
import { listProducts } from '@/api/products'
import { matchLineItem } from '@/api/matching'
import type { LineItem, Product } from '@/types'

interface ReceiptReviewProps {
  receiptId: string
}

/** Row data for the editable table — extends LineItem with resolved product name */
interface LineItemRow extends LineItem {
  product_name: string
}

function ReceiptReview({ receiptId }: ReceiptReviewProps) {
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
    mutationFn: () => confirmReceipt(receiptId),
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

  // --- Build product lookup map (search results + API-provided names) ---
  const productMap = useMemo(() => {
    const map = new Map<string, string>()
    // Include product names from the receipt's own line items (from API JOIN)
    if (receipt) {
      for (const li of receipt.line_items) {
        if (li.product_id && li.product_name) {
          map.set(li.product_id, li.product_name)
        }
      }
    }
    // Search results override (fresher data)
    for (const p of products) {
      map.set(p.id, p.name)
    }
    return map
  }, [products, receipt])

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
      product_name: li.product_name ?? (li.product_id ? productMap.get(li.product_id) ?? '' : ''),
    }))
  }, [receipt, productMap])

  // --- Status counts ---
  const matchedCount = useMemo(
    () => rows.filter((r) => r.matched !== 'unmatched').length,
    [rows],
  )
  const suggestedRows = useMemo(
    () => rows.filter((r) => r.matched === 'unmatched' && r.suggestion_type != null),
    [rows],
  )
  const suggestedCount = suggestedRows.length
  const unmatchedCount = useMemo(
    () => rows.filter((r) => r.matched === 'unmatched' && r.suggestion_type == null).length,
    [rows],
  )

  // --- Suggestion lookup map for inline display ---
  const suggestionMap = useMemo(() => {
    const map = new Map<string, { name: string; type: string }>()
    if (!receipt) return map
    for (const li of receipt.line_items) {
      if (li.matched === 'unmatched' && li.suggestion_type) {
        const name = li.suggestion_type === 'existing_match'
          ? li.suggested_product_name
          : li.suggested_name
        if (name) map.set(li.id, { name, type: li.suggestion_type })
      }
    }
    return map
  }, [receipt])

  // --- Combined confirm loading state ---
  const [confirmLoading, setConfirmLoading] = useState(false)

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
          if (item.suggestion_type) {
            const isExisting = item.suggestion_type === 'existing_match'
            return (
              <span
                className="flex items-center justify-center"
                title={isExisting ? 'Suggested match' : 'Suggested new product'}
              >
                <svg
                  className={`w-4 h-4 ${isExisting ? 'text-amber-500' : 'text-blue-500'}`}
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  strokeWidth={2.5}
                >
                  <path
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3m3.343-5.657l-.707-.707m2.828 9.9a5 5 0 117.072 0l-.548.547A3.374 3.374 0 0014 18.469V19a2 2 0 11-4 0v-.531c0-.895-.356-1.754-.988-2.386l-.548-.547z"
                  />
                </svg>
              </span>
            )
          }
          return (
            <span
              className="flex items-center justify-center"
              title="Unmatched"
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
          getSuggestedValue: (rowIndex: number) => {
            const row = rows[rowIndex]
            if (!row) return null
            return suggestionMap.get(row.id) ?? null
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
        size: 120,
        meta: {
          editable: true,
          cellType: 'text' as const,
        },
        cell: ({ row }) => {
          const item = row.original
          const price = item.total_price
          const formatted = price != null ? '$' + Number(price).toFixed(2) : '\u2014'

          if (item.regular_price && item.discount_amount) {
            return (
              <div className="text-right">
                <span className="tabular-nums">{formatted}</span>
                <span className="block text-xs text-neutral-400">
                  <span className="line-through">
                    ${Number(item.regular_price).toFixed(2)}
                  </span>
                  <span className="text-green-600 ml-1">
                    -${Number(item.discount_amount).toFixed(2)}
                  </span>
                </span>
              </div>
            )
          }

          return <span className="tabular-nums">{formatted}</span>
        },
      },
    ],
    [autocompleteOptions, productMap, handleAutocompleteCreate, suggestionMap, rows],
  )

  // --- Row class names for unmatched highlighting ---
  const getRowClassName = useCallback(
    (row: LineItemRow) => {
      if (row.matched !== 'unmatched') return ''
      if (row.suggestion_type) return 'bg-amber-50/50'
      return 'bg-expensive-subtle/30'
    },
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
          {suggestedCount > 0 && (
            <Badge variant="warning">{suggestedCount} suggested</Badge>
          )}
          {unmatchedCount > 0 && (
            <Badge variant="error">{unmatchedCount} unmatched</Badge>
          )}
          <span className="text-caption text-neutral-400">
            {receipt.status === 'reviewed' ? 'Reviewed' : receipt.status}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setRawJsonOpen(true)}
          >
            <svg className="w-4 h-4 inline-block mr-1 -mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M10 20l4-16m4 4l4 4-4 4M6 16l-4-4 4-4" />
            </svg>
            Raw JSON
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={async () => {
              setConfirmLoading(true)
              try {
                // Step 1: Accept any pending suggestions
                if (suggestedRows.length > 0) {
                  await acceptSuggestions(receiptId, { line_item_ids: suggestedRows.map(r => r.id) })
                }
                // Step 2: Check for pending rule matches (preserve batch rule modal)
                if (pendingRuleMatches.length > 0 && receipt.status !== 'reviewed') {
                  setBatchRuleModalOpen(true)
                  return
                }
                // Step 3: Confirm receipt
                await confirmReceipt(receiptId)
                queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
                queryClient.invalidateQueries({ queryKey: ['products'] })
              } catch (err) {
                console.error('Confirm failed:', err)
                alert('Failed to confirm receipt. Please try again.')
              } finally {
                setConfirmLoading(false)
              }
            }}
            disabled={confirmLoading || confirmMutation.isPending || receipt.status === 'reviewed'}
          >
            {confirmLoading || confirmMutation.isPending
              ? 'Confirming...'
              : receipt.status === 'reviewed'
                ? 'Confirmed'
                : 'Confirm Receipt'}
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
