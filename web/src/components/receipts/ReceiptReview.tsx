import { useState, useMemo, useCallback, useEffect, type FormEvent } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { type ColumnDef } from '@tanstack/react-table'
import { EditableTable, type AutocompleteOption } from '@/components/ui/EditableTable'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
// CreateRuleModal replaced by inline batch rule modal
import { getReceipt, updateLineItem, createLineItem, repairReceiptPreview, applyRepairPreview, acceptSuggestions, confirmReceipt, type CreateLineItemRequest, type ReceiptDetail, type RepairPreviewResponse } from '@/api/receipts'
import { listProducts } from '@/api/products'
import { matchLineItem } from '@/api/matching'
import type { LineItem, Product } from '@/types'

interface ReceiptReviewProps {
  receiptId: string
}

const SCAN_PROGRESS_STAGES = [
  { label: 'Reading receipt image...', duration: 12_000 },
  { label: 'Extracting line items and prices...', duration: 30_000 },
  { label: 'Identifying store and date...', duration: 15_000 },
  { label: 'Matching products...', duration: 20_000 },
  { label: 'Almost done...', duration: 120_000 },
]

function ScanProgress() {
  const [stageIndex, setStageIndex] = useState(0)
  const [barWidth, setBarWidth] = useState(0)

  useEffect(() => {
    let elapsed = 0
    let currentStage = 0
    const interval = window.setInterval(() => {
      elapsed += 500

      let cumulativeDuration = 0
      for (let i = 0; i < SCAN_PROGRESS_STAGES.length; i++) {
        cumulativeDuration += SCAN_PROGRESS_STAGES[i]!.duration
        if (elapsed < cumulativeDuration) {
          currentStage = i
          break
        }
        if (i === SCAN_PROGRESS_STAGES.length - 1) currentStage = i
      }
      setStageIndex(currentStage)

      const totalDuration = SCAN_PROGRESS_STAGES.reduce((sum, stage) => sum + stage.duration, 0)
      const linear = Math.min(elapsed / totalDuration, 0.95)
      const eased = 1 - Math.pow(1 - linear, 2)
      setBarWidth(Math.round(eased * 100))
    }, 500)
    return () => window.clearInterval(interval)
  }, [])

  return (
    <div
      role="status"
      className="flex min-h-[320px] flex-col items-center justify-center rounded-lg border border-neutral-200 bg-neutral-50 px-6 py-12 text-center"
    >
      <div className="h-12 w-12 animate-spin rounded-full border-4 border-neutral-200 border-t-brand" />
      <p className="mt-6 font-display text-feature font-semibold text-neutral-900">
        Scanning receipt
      </p>
      <div className="mt-6 w-full max-w-sm">
        <div className="h-2 overflow-hidden rounded-full bg-neutral-200">
          <div
            className="h-full rounded-full bg-brand transition-all duration-500 ease-out"
            style={{ width: `${barWidth}%` }}
          />
        </div>
        <p className="mt-3 text-small text-neutral-400 animate-pulse">
          {SCAN_PROGRESS_STAGES[stageIndex]?.label ?? 'Processing...'}
        </p>
      </div>
    </div>
  )
}

/** Row data for the editable table — extends LineItem with resolved product name */
interface LineItemRow extends LineItem {
  product_name: string
}

const emptyNewRow: CreateLineItemRequest = {
  raw_name: '',
  quantity: '1',
  unit: 'each',
  total_price: '',
  count_contribution: '1',
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

  // --- Manual add row modal ---
  const [addRowOpen, setAddRowOpen] = useState(false)
  const [newRow, setNewRow] = useState<CreateLineItemRequest>(emptyNewRow)

  // --- Contextual repair note modal ---
  const [repairOpen, setRepairOpen] = useState(false)
  const [repairNote, setRepairNote] = useState('')
  const [repairPreview, setRepairPreview] = useState<RepairPreviewResponse | null>(null)

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

  const createLineItemMutation = useMutation({
    mutationFn: (data: CreateLineItemRequest) => createLineItem(receiptId, data),
    onSuccess: () => {
      setNewRow(emptyNewRow)
      setAddRowOpen(false)
      queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
    },
  })

  const repairPreviewMutation = useMutation({
    mutationFn: (note: string) => repairReceiptPreview(receiptId, note),
    onSuccess: (preview) => {
      setRepairPreview(preview)
    },
  })

  const applyRepairMutation = useMutation({
    mutationFn: (preview: RepairPreviewResponse) => applyRepairPreview(receiptId, preview),
    onSuccess: () => {
      setRepairOpen(false)
      setRepairNote('')
      setRepairPreview(null)
      queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
      queryClient.invalidateQueries({ queryKey: ['receipts'] })
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

  // --- Cross-store confirmation modal ---
  const [crossStoreConfirmOpen, setCrossStoreConfirmOpen] = useState(false)

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
  const suggestedMatchCount = useMemo(
    () => rows.filter((r) => r.matched === 'unmatched' && r.suggestion_type === 'existing_match').length,
    [rows],
  )
  const suggestedNewCount = useMemo(
    () => rows.filter((r) => r.matched === 'unmatched' && r.suggestion_type === 'new_product').length,
    [rows],
  )
  const crossStoreMatchCount = useMemo(
    () => rows.filter((r) => r.matched === 'unmatched' && r.suggestion_type === 'cross_store_match').length,
    [rows],
  )
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
        const name = (li.suggestion_type === 'existing_match' || li.suggestion_type === 'cross_store_match')
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

  const updateNewRow = useCallback(
    (field: keyof CreateLineItemRequest, value: string) => {
      setNewRow((prev) => ({
        ...prev,
        [field]: field === 'line_number' ? (value ? Number(value) : undefined) : value,
      }))
    },
    [],
  )

  const handleAddRowSubmit = useCallback(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      createLineItemMutation.mutate({
        ...newRow,
        raw_name: newRow.raw_name.trim(),
        total_price: newRow.total_price.trim(),
        quantity: newRow.quantity?.trim() || undefined,
        unit: newRow.unit?.trim() || undefined,
        unit_price: newRow.unit_price?.trim() || undefined,
        count_contribution: newRow.count_contribution?.trim() || undefined,
      })
    },
    [createLineItemMutation, newRow],
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
          if (item.suggestion_type === 'existing_match') {
            return (
              <span className="flex items-center justify-center" title="Suggested match to existing product">
                <svg className="w-4 h-4 text-amber-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3m3.343-5.657l-.707-.707m2.828 9.9a5 5 0 117.072 0l-.548.547A3.374 3.374 0 0014 18.469V19a2 2 0 11-4 0v-.531c0-.895-.356-1.754-.988-2.386l-.548-.547z" />
                </svg>
              </span>
            )
          }
          if (item.suggestion_type === 'new_product') {
            return (
              <span className="flex items-center justify-center" title="Will create new product">
                <svg className="w-4 h-4 text-blue-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v6m3-3H9m12 0a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
              </span>
            )
          }
          if (item.suggestion_type === 'cross_store_match') {
            return (
              <span className="flex items-center justify-center" title="Similar product found at another store">
                <svg className="w-4 h-4 text-purple-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
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
        cell: ({ row }) => {
          const item = row.original
          return (
            <div>
              <span>{item.raw_name}</span>
              {item.product_id && item.product_name && (
                <Link
                  to={`/products/${item.product_id}`}
                  className="block text-xs text-brand hover:underline mt-0.5"
                  onClick={(e) => e.stopPropagation()}
                >
                  {item.product_name}
                </Link>
              )}
            </div>
          )
        },
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

  if ((receipt.status === 'pending' || receipt.status === 'processing') && receipt.line_items.length === 0) {
    return <ScanProgress />
  }

  return (
    <div className="flex flex-col gap-4">
      {/* Status bar */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Badge variant="success">{matchedCount} matched</Badge>
          {suggestedMatchCount > 0 && (
            <Badge variant="warning">{suggestedMatchCount} suggested</Badge>
          )}
          {suggestedNewCount > 0 && (
            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-blue-100 text-blue-700">
              {suggestedNewCount} new
            </span>
          )}
          {crossStoreMatchCount > 0 && (
            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium bg-purple-100 text-purple-700">
              {crossStoreMatchCount} cross-store
            </span>
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
              // If cross-store matches exist, show confirmation dialog first
              if (crossStoreMatchCount > 0 && !crossStoreConfirmOpen) {
                setCrossStoreConfirmOpen(true)
                return
              }
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

      {receipt.warnings && receipt.warnings.length > 0 && (
        <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div className="flex flex-col gap-1">
              {receipt.warnings.map((warning) => (
                <p key={warning.code} className="text-body text-amber-900">
                  {warning.message}
                </p>
              ))}
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => setAddRowOpen(true)}
              >
                Add Row
              </Button>
              <Button
                type="button"
                variant="outlined"
                size="sm"
                onClick={() => {
                  setRepairPreview(null)
                  setRepairOpen(true)
                }}
              >
                Repair Scan
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Editable line items table */}
      <EditableTable<LineItemRow>
        columns={columns}
        data={rows}
        onCellUpdate={handleCellUpdate}
        getRowClassName={getRowClassName}
        virtualizeRows={rows.length > 50}
        enableSorting={false}
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

      <Modal
        open={addRowOpen}
        onClose={() => {
          setAddRowOpen(false)
          setNewRow(emptyNewRow)
        }}
        title="Add Line Item"
        footer={
          <>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={() => {
                setAddRowOpen(false)
                setNewRow(emptyNewRow)
              }}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              form="add-line-item-form"
              size="sm"
              disabled={
                createLineItemMutation.isPending ||
                newRow.raw_name.trim() === '' ||
                newRow.total_price.trim() === ''
              }
            >
              {createLineItemMutation.isPending ? 'Adding...' : 'Add Row'}
            </Button>
          </>
        }
      >
        <form id="add-line-item-form" onSubmit={handleAddRowSubmit} className="grid grid-cols-2 gap-3">
          <label className="col-span-2 flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Receipt Text
            <input
              value={newRow.raw_name}
              onChange={(e) => updateNewRow('raw_name', e.target.value)}
              className="rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
              autoFocus
            />
          </label>
          <label className="flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Qty
            <input
              value={newRow.quantity ?? ''}
              onChange={(e) => updateNewRow('quantity', e.target.value)}
              className="rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </label>
          <label className="flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Unit
            <input
              value={newRow.unit ?? ''}
              onChange={(e) => updateNewRow('unit', e.target.value)}
              className="rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </label>
          <label className="flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Price
            <input
              value={newRow.total_price}
              onChange={(e) => updateNewRow('total_price', e.target.value)}
              className="rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </label>
          <label className="flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Count
            <input
              value={newRow.count_contribution ?? ''}
              onChange={(e) => updateNewRow('count_contribution', e.target.value)}
              className="rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </label>
          <label className="col-span-2 flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Line
            <input
              type="number"
              value={newRow.line_number ?? ''}
              onChange={(e) => updateNewRow('line_number', e.target.value)}
              className="rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </label>
          {createLineItemMutation.isError && (
            <p className="col-span-2 text-small text-expensive">
              Failed to add row.
            </p>
          )}
        </form>
      </Modal>

      <Modal
        open={repairOpen}
        onClose={() => setRepairOpen(false)}
        title="Repair Scan"
        footer={
          <>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={() => {
                setRepairOpen(false)
                setRepairPreview(null)
              }}
            >
              Cancel
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={
                repairPreviewMutation.isPending ||
                applyRepairMutation.isPending ||
                repairNote.trim() === ''
              }
              onClick={() => {
                if (repairPreview) {
                  applyRepairMutation.mutate(repairPreview)
                  return
                }
                repairPreviewMutation.mutate(repairNote.trim())
              }}
            >
              {applyRepairMutation.isPending
                ? 'Applying...'
                : repairPreview
                  ? 'Apply Repair'
                  : repairPreviewMutation.isPending
                    ? 'Repairing...'
                    : 'Preview Repair'}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <label className="flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Repair Note
            <textarea
              value={repairNote}
              onChange={(e) => {
                setRepairNote(e.target.value)
                setRepairPreview(null)
              }}
              rows={4}
              className="resize-none rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </label>
          {repairPreviewMutation.isError && (
            <p className="text-small text-expensive">
              Repair preview failed.
            </p>
          )}
          {applyRepairMutation.isError && (
            <p className="text-small text-expensive">
              Failed to apply repair.
            </p>
          )}
          {repairPreview && (
            <div className="max-h-72 overflow-auto rounded-lg border border-neutral-200">
              <table className="w-full table-fixed border-collapse">
                <thead className="bg-neutral-50">
                  <tr>
                    <th className="px-2 py-1 text-left text-caption font-semibold text-neutral-600">Line</th>
                    <th className="px-2 py-1 text-left text-caption font-semibold text-neutral-600">Item</th>
                    <th className="px-2 py-1 text-left text-caption font-semibold text-neutral-600">Qty</th>
                    <th className="px-2 py-1 text-left text-caption font-semibold text-neutral-600">Price</th>
                  </tr>
                </thead>
                <tbody>
                  {repairPreview.items.map((item, index) => (
                    <tr key={`${item.line_number}-${item.raw_name}-${index}`} className="border-t border-neutral-200">
                      <td className="px-2 py-1 text-caption text-neutral-600">{item.line_number}</td>
                      <td className="px-2 py-1 text-caption text-neutral-900 truncate">{item.raw_name}</td>
                      <td className="px-2 py-1 text-caption text-neutral-600">{item.quantity}</td>
                      <td className="px-2 py-1 text-caption tabular-nums text-neutral-900">
                        ${Number(item.total_price).toFixed(2)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </Modal>

      {/* Cross-store match confirmation modal */}
      <Modal
        open={crossStoreConfirmOpen}
        onClose={() => setCrossStoreConfirmOpen(false)}
        title="Cross-Store Matches"
        footer={
          <>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setCrossStoreConfirmOpen(false)}
            >
              Cancel
            </Button>
            <Button
              variant="primary"
              size="sm"
              onClick={async () => {
                setCrossStoreConfirmOpen(false)
                setConfirmLoading(true)
                try {
                  if (suggestedRows.length > 0) {
                    await acceptSuggestions(receiptId, { line_item_ids: suggestedRows.map(r => r.id) })
                  }
                  if (pendingRuleMatches.length > 0 && receipt.status !== 'reviewed') {
                    setBatchRuleModalOpen(true)
                    return
                  }
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
            >
              Confirm All
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-600">
          {crossStoreMatchCount} {crossStoreMatchCount === 1 ? 'item was' : 'items were'} matched to products from other stores — confirm these too?
        </p>
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
