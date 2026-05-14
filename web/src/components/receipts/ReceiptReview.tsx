import { useState, useMemo, useCallback, useEffect, lazy, Suspense, type FormEvent } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { type ColumnDef } from '@tanstack/react-table'
import { Camera, Check, ChevronDown, Circle, CircleAlert, ExternalLink, FileCode2, Loader2, Search, Wrench } from 'lucide-react'
import { EditableTable, type AutocompleteOption } from '@/components/ui/EditableTable'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import {
  ManualLineItemGrid,
  createManualLineItemRows,
  manualLineItemRowsAreComplete,
  toManualLineItemInputs,
  type ManualLineItemGridRow,
} from '@/components/receipts/ManualLineItemGrid'
// CreateRuleModal replaced by inline batch rule modal
import { getReceipt, updateLineItem, createLineItem, createLineItems, repairReceiptPreview, applyRepairPreview, acceptSuggestions, confirmReceipt, type CreateLineItemRequest, type ManualLineItemInput, type ReceiptDetail, type RepairPreviewResponse } from '@/api/receipts'
import { listProducts, updateProduct } from '@/api/products'
import { matchLineItem } from '@/api/matching'
import type { AcceptSuggestionsResponse, LineItem, Product } from '@/types'

const BarcodeScannerModal = lazy(() =>
  import('@/components/receipts/BarcodeScannerModal').then((module) => ({
    default: module.BarcodeScannerModal,
  })),
)

interface ReceiptReviewProps {
  receiptId: string
}

type IdentifierWarning = NonNullable<AcceptSuggestionsResponse['warnings']>[number]

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

type UnitCategory = 'weight' | 'volume' | 'count' | 'unknown' | 'blank'

type PackageContentsStatus =
  | { kind: 'label'; label: string; source: 'receipt' | 'product' }
  | { kind: 'set' }
  | { kind: 'ambiguous' }
  | { kind: 'none' }

const unitAliases: Record<string, string> = {
  ounces: 'oz',
  ounce: 'oz',
  oz: 'oz',
  pounds: 'lb',
  pound: 'lb',
  lbs: 'lb',
  lb: 'lb',
  grams: 'g',
  gram: 'g',
  g: 'g',
  kilograms: 'kg',
  kilogram: 'kg',
  kgs: 'kg',
  kg: 'kg',
  'fluid ounces': 'fl_oz',
  'fluid ounce': 'fl_oz',
  'fl oz': 'fl_oz',
  fl_oz: 'fl_oz',
  floz: 'fl_oz',
  gallons: 'gal',
  gallon: 'gal',
  gal: 'gal',
  quarts: 'qt',
  quart: 'qt',
  qt: 'qt',
  pints: 'pt',
  pint: 'pt',
  pt: 'pt',
  cups: 'cup',
  cup: 'cup',
  tablespoons: 'tbsp',
  tablespoon: 'tbsp',
  tbsp: 'tbsp',
  tbs: 'tbsp',
  teaspoons: 'tsp',
  teaspoon: 'tsp',
  tsp: 'tsp',
  milliliters: 'ml',
  milliliter: 'ml',
  ml: 'ml',
  liters: 'l',
  liter: 'l',
  litres: 'l',
  litre: 'l',
  l: 'l',
  each: 'each',
  ea: 'each',
  ct: 'each',
  count: 'each',
  pc: 'each',
  pcs: 'each',
  piece: 'each',
  pieces: 'each',
}

function normalizeUnit(unit: string | null | undefined): string {
  const normalized = unit?.trim().toLowerCase().replace(/\s+/g, ' ') ?? ''
  return unitAliases[normalized] ?? normalized
}

function classifyUnit(unit: string | null | undefined): UnitCategory {
  const normalized = normalizeUnit(unit)
  if (!normalized) return 'blank'
  if (['oz', 'lb', 'g', 'kg'].includes(normalized)) return 'weight'
  if (['fl_oz', 'gal', 'qt', 'pt', 'cup', 'tbsp', 'tsp', 'ml', 'l'].includes(normalized)) return 'volume'
  if (normalized === 'each') return 'count'
  return 'unknown'
}

function isExplicitCountUnit(unit: string | null | undefined): boolean {
  const normalized = unit?.trim().toLowerCase().replace(/\s+/g, ' ') ?? ''
  return ['ct', 'count', 'pc', 'pcs', 'piece', 'pieces'].includes(normalized)
}

function formatProductPackQuantity(quantity: number): string {
  return Number.isInteger(quantity) ? quantity.toFixed(0) : String(quantity)
}

function productPackLabel(item: LineItem): string | null {
  if (item.product_pack_quantity == null || !item.product_pack_unit) return null
  return `${formatProductPackQuantity(item.product_pack_quantity)} ${item.product_pack_unit}`
}

function hasProductPackageContents(item: LineItem): boolean {
  return item.product_pack_quantity != null && Boolean(item.product_pack_unit?.trim())
}

function materiallyDifferentReceiptDescription(item: LineItem): string | null {
  const description = item.receipt_description?.trim()
  if (!description) return null
  const raw = item.raw_name.trim()
  const normalizedDescription = description.toLowerCase().replace(/\s+/g, ' ')
  const normalizedRaw = raw.toLowerCase().replace(/\s+/g, ' ')
  if (normalizedDescription === normalizedRaw || normalizedRaw.includes(normalizedDescription)) {
    return null
  }
  return description
}

function purchasedLabel(item: Pick<LineItem, 'quantity' | 'unit'>): string {
  return `${item.quantity}${item.unit ? ` ${item.unit}` : ''}`.trim()
}

function suggestedProductName(item: LineItem): string | null {
  if (item.matched !== 'unmatched' || !item.suggestion_type) return null
  if (item.suggestion_type === 'new_product') return item.suggested_name
  return item.suggested_product_name
}

function suggestionTone(item: LineItem): { label: string; className: string } | null {
  if (item.suggestion_type === 'new_product') {
    return {
      label: 'Suggested new',
      className: 'border-blue-200 bg-blue-50 text-blue-700',
    }
  }
  if (item.suggestion_type === 'cross_store_match') {
    return {
      label: 'Cross-store suggestion',
      className: 'border-purple-200 bg-purple-50 text-purple-700',
    }
  }
  if (item.suggestion_type === 'existing_match') {
    return {
      label: 'Suggested match',
      className: 'border-amber-200 bg-amber-50 text-amber-700',
    }
  }
  return null
}

function matchMethodLabel(matched: LineItem['matched']): string | null {
  if (matched === 'code') return 'code match'
  if (matched === 'identifier') return 'ID match'
  return null
}

function enrichmentBadge(item: LineItem): { label: string; variant: 'neutral' | 'success' | 'warning' | 'error' } | null {
  const status = item.enrichment_job_status
  if (!status || status === 'cancelled' || status === 'partial') return null
  if (status === 'queued' || status === 'running') {
    return { label: 'Looking up...', variant: 'neutral' }
  }
  if (status === 'failed') {
    return { label: 'Lookup failed', variant: 'error' }
  }
  if (status === 'succeeded') {
    return (item.enrichment_suggestion_count ?? 0) > 0
      ? { label: 'Package data found', variant: 'success' }
      : { label: 'No package data', variant: 'neutral' }
  }
  return null
}

function barcodeEligible(item: LineItem): boolean {
  if (item.review_status === 'accepted') return false
  if (item.product_id) return false
  return item.matched === 'unmatched' && (item.suggestion_type == null || item.suggestion_type === 'new_product')
}

function packageContentsStatus(item: LineItem): PackageContentsStatus {
  if (item.pack_quantity_override && item.pack_unit_override) {
    return { kind: 'label', label: `${item.pack_quantity_override} ${item.pack_unit_override}`, source: 'receipt' }
  }

  const packLabel = productPackLabel(item)
  const lineCategory = classifyUnit(item.unit)
  const packCategory = classifyUnit(item.product_pack_unit)

  if (packLabel) {
    if ((lineCategory === 'weight' || lineCategory === 'volume') && packCategory !== 'unknown' && packCategory !== lineCategory) {
      return { kind: 'ambiguous' }
    }
    if (isExplicitCountUnit(item.unit) && packCategory !== 'count' && packCategory !== 'unknown') {
      return { kind: 'ambiguous' }
    }
  }

  if (packLabel) {
    return { kind: 'label', label: packLabel, source: 'product' }
  }
  if (lineCategory === 'weight' || lineCategory === 'volume') {
    return { kind: 'none' }
  }
  if (item.product_id) {
    return { kind: 'set' }
  }
  return { kind: 'none' }
}

interface EmptyReceiptManualEntryProps {
  initialRowCount: number
  isSaving: boolean
  isError: boolean
  onSubmit: (items: ManualLineItemInput[]) => void
}

function EmptyReceiptManualEntry({
  initialRowCount,
  isSaving,
  isError,
  onSubmit,
}: EmptyReceiptManualEntryProps) {
  const [rows, setRows] = useState<ManualLineItemGridRow[]>(() =>
    createManualLineItemRows(initialRowCount),
  )
  const isValid = manualLineItemRowsAreComplete(rows)

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!isValid || isSaving) return
    onSubmit(toManualLineItemInputs(rows))
  }

  return (
    <form onSubmit={handleSubmit} className="rounded-lg border border-neutral-200 bg-white p-4">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="font-display text-feature font-semibold text-neutral-900">
          Items
        </h2>
        <span className="text-caption text-neutral-400">
          {rows.length} {rows.length === 1 ? 'item' : 'items'}
        </span>
      </div>
      <ManualLineItemGrid
        rows={rows}
        onRowsChange={setRows}
        disabled={isSaving}
      />
      {isError && (
        <p className="mt-3 text-small text-expensive" role="alert">
          Failed to save items.
        </p>
      )}
      <div className="mt-4 flex justify-end">
        <Button
          type="submit"
          size="sm"
          disabled={!isValid || isSaving}
        >
          {isSaving ? 'Saving...' : 'Save Items'}
        </Button>
      </div>
    </form>
  )
}

function ReceiptEvidenceLine({ item }: { item: LineItemRow }) {
  const cleanedDescription = materiallyDifferentReceiptDescription(item)

  return (
    <div className="mt-1 flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1 text-small text-neutral-500">
      <span className="block min-w-0 max-w-full truncate font-mono text-neutral-500">
        {item.raw_name}
      </span>
      {item.store_item_code && (
        <span className="rounded-md bg-brand-subtle px-1.5 py-0.5 font-mono font-medium text-brand">
          SKU/PLU {item.store_item_code}
        </span>
      )}
      {item.upc && (
        <span className="rounded-md bg-neutral-50 px-1.5 py-0.5 font-mono font-medium text-neutral-600">
          UPC {item.upc}
        </span>
      )}
      {cleanedDescription && (
        <>
          <span className="text-neutral-300" aria-hidden="true">·</span>
          <span className="min-w-0 truncate text-neutral-400">
            {cleanedDescription}
          </span>
        </>
      )}
    </div>
  )
}

function ProductMatchDisplay({ item }: { item: LineItemRow }) {
  const methodLabel = matchMethodLabel(item.matched)
  const suggestedName = suggestedProductName(item)
  const suggestion = suggestionTone(item)
  const lookupBadge = enrichmentBadge(item)

  return (
    <div className="min-w-0 py-1">
      {item.product_id ? (
        <div className="flex min-w-0 items-center gap-1.5">
          <span className="block min-w-0 truncate text-body-medium font-semibold text-neutral-900">
            {item.product_name || 'Matched product'}
          </span>
          <ChevronDown className="h-3.5 w-3.5 flex-shrink-0 text-neutral-400" aria-hidden="true" />
          <Link
            to={`/products/${item.product_id}`}
            className="inline-flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-md text-neutral-400 transition-colors hover:bg-neutral-50 hover:text-brand"
            title="Open product"
            aria-label={`Open ${item.product_name || 'product'} page`}
            onClick={(event) => event.stopPropagation()}
          >
            <ExternalLink className="h-3.5 w-3.5" aria-hidden="true" />
          </Link>
          {methodLabel && <Badge variant="neutral">{methodLabel}</Badge>}
          {lookupBadge && <Badge variant={lookupBadge.variant}>{lookupBadge.label}</Badge>}
        </div>
      ) : suggestedName && suggestion ? (
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
          <span className={['inline-flex items-center rounded-md border px-1.5 py-0.5 text-small font-semibold', suggestion.className].join(' ')}>
            {suggestion.label}
          </span>
          <span className="block min-w-0 truncate text-body-medium font-semibold text-neutral-900">
            {suggestedName}
          </span>
          <ChevronDown className="h-3.5 w-3.5 flex-shrink-0 text-neutral-400" aria-hidden="true" />
          {lookupBadge && <Badge variant={lookupBadge.variant}>{lookupBadge.label}</Badge>}
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="inline-flex items-center gap-1 rounded-md border border-amber-200 bg-amber-50 px-2 py-1 text-caption font-semibold text-amber-700">
            <Search className="h-3.5 w-3.5" aria-hidden="true" />
            Match product
          </span>
          {lookupBadge && <Badge variant={lookupBadge.variant}>{lookupBadge.label}</Badge>}
        </div>
      )}
      <ReceiptEvidenceLine item={item} />
    </div>
  )
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
  const [addRowScannerOpen, setAddRowScannerOpen] = useState(false)

  // --- Contextual repair note modal ---
  const [repairOpen, setRepairOpen] = useState(false)
  const [repairNote, setRepairNote] = useState('')
  const [repairPreview, setRepairPreview] = useState<RepairPreviewResponse | null>(null)
  const [sizeEditItem, setSizeEditItem] = useState<LineItemRow | null>(null)
  const [sizeQuantity, setSizeQuantity] = useState('')
  const [sizeUnit, setSizeUnit] = useState('')
  const [savePackageDefault, setSavePackageDefault] = useState(false)
  const [identifierWarnings, setIdentifierWarnings] = useState<IdentifierWarning[]>([])
  const [barcodeItem, setBarcodeItem] = useState<LineItemRow | null>(null)

  useEffect(() => {
    setIdentifierWarnings([])
  }, [receiptId])

  const recordIdentifierWarnings = useCallback((response: AcceptSuggestionsResponse) => {
    const warnings = response.warnings ?? []
    if (warnings.length === 0) return
    setIdentifierWarnings((current) => [...warnings, ...current].slice(0, 6))
  }, [])

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

  const closePackageContentsEditor = useCallback(() => {
    setSizeEditItem(null)
    setSizeQuantity('')
    setSizeUnit('')
    setSavePackageDefault(false)
  }, [])

  const sizeMutation = useMutation({
    mutationFn: async () => {
      if (!sizeEditItem) throw new Error('missing line item')
      const trimmedQuantity = sizeQuantity.trim()
      const trimmedUnit = sizeUnit.trim()
      const lineItem = await updateLineItem(receiptId, sizeEditItem.id, {
        pack_quantity_override: trimmedQuantity,
        pack_unit_override: trimmedUnit,
        pack_override_source: 'user',
      })
      if (savePackageDefault && sizeEditItem.product_id) {
        await updateProduct(sizeEditItem.product_id, {
          pack_quantity: Number(trimmedQuantity),
          pack_unit: trimmedUnit,
        })
      }
      return lineItem
    },
    onSuccess: () => {
      closePackageContentsEditor()
      queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
      queryClient.invalidateQueries({ queryKey: ['receipts', 'compare'] })
      queryClient.invalidateQueries({ queryKey: ['products'] })
      queryClient.invalidateQueries({ queryKey: ['product-detail'] })
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

  const createLineItemsMutation = useMutation({
    mutationFn: (items: ManualLineItemInput[]) => createLineItems(receiptId, items),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
      queryClient.invalidateQueries({ queryKey: ['receipts'] })
      queryClient.invalidateQueries({ queryKey: ['products'] })
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

  const barcodeEligibleRows = useMemo(
    () => rows.filter(barcodeEligible),
    [rows],
  )

  const handleBarcodeApplied = useCallback(() => {
    const currentItemId = barcodeItem?.id
    const nextItem = barcodeEligibleRows.find((row) => row.id !== currentItemId) ?? null
    setBarcodeItem(nextItem)
    queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
    queryClient.invalidateQueries({ queryKey: ['products'] })
  }, [barcodeEligibleRows, barcodeItem?.id, queryClient, receiptId])

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
  const reviewedCount = useMemo(
    () => rows.filter((r) => r.review_status === 'accepted').length,
    [rows],
  )
  const reviewRemainingCount = Math.max(rows.length - reviewedCount, 0)
  const canConfirmReceipt =
    receipt?.status !== 'reviewed' &&
    rows.length > 0 &&
    reviewRemainingCount === 0
  const showEmptyManualEntry =
    receipt != null &&
    rows.length === 0 &&
    receipt.status !== 'pending' &&
    receipt.status !== 'processing' &&
    receipt.status !== 'reviewed'
  const emptyManualEntryRowCount = Math.max(
    1,
    Math.min(receipt?.items_sold_count ?? 5, 100),
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
  const [reviewingItemId, setReviewingItemId] = useState<string | null>(null)

  const handleToggleItemReview = useCallback(
    async (item: LineItemRow) => {
      if (!receipt) return
      setReviewingItemId(item.id)
      try {
        if (item.review_status === 'accepted') {
          await updateLineItem(receiptId, item.id, { review_status: 'pending' })
          return
        }

        if (!item.product_id) {
          if (!item.suggestion_type) return
          const response = await acceptSuggestions(receiptId, { line_item_ids: [item.id] })
          recordIdentifierWarnings(response)
        }

        await updateLineItem(receiptId, item.id, { review_status: 'accepted' })
        queryClient.invalidateQueries({ queryKey: ['products'] })
      } catch (err) {
        console.error('Failed to update line item review status:', err)
        alert('Failed to update line item review status. Please try again.')
      } finally {
        setReviewingItemId(null)
        queryClient.invalidateQueries({ queryKey: ['receipt', receiptId] })
        queryClient.invalidateQueries({ queryKey: ['receipts'] })
      }
    },
    [queryClient, receipt, receiptId, recordIdentifierWarnings],
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
        upc: newRow.upc?.trim() || undefined,
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
          const accepted = item.review_status === 'accepted'
          const canAccept = !!item.product_id || item.suggestion_type != null
          const busy = reviewingItemId === item.id
          const disabled = busy || (!accepted && !canAccept) || receipt?.status === 'reviewed'
          const tone = accepted
            ? 'text-success-dark bg-success-subtle hover:bg-success-subtle'
            : item.suggestion_type === 'new_product'
              ? 'text-blue-600 hover:bg-blue-50'
              : item.suggestion_type === 'cross_store_match'
                ? 'text-purple-600 hover:bg-purple-50'
                : item.suggestion_type === 'existing_match'
                  ? 'text-amber-600 hover:bg-amber-50'
                  : canAccept
                    ? 'text-neutral-600 hover:bg-neutral-100'
                    : 'text-expensive'

          return (
            <button
              type="button"
              className={[
                'flex h-7 w-7 items-center justify-center rounded-md transition-colors',
                disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer',
                tone,
              ].join(' ')}
              title={
                accepted
                  ? receipt?.status === 'reviewed'
                    ? 'Reviewed'
                    : 'Mark item for review'
                  : canAccept
                    ? 'Mark item reviewed'
                    : 'Match product before reviewing'
              }
              aria-label={
                accepted
                  ? receipt?.status === 'reviewed'
                    ? `Reviewed ${item.raw_name}`
                    : `Mark ${item.raw_name} for review`
                  : canAccept
                    ? `Mark ${item.raw_name} reviewed`
                    : `Match ${item.raw_name} before reviewing`
              }
              disabled={disabled}
              onClick={(event) => {
                event.stopPropagation()
                void handleToggleItemReview(item)
              }}
            >
              {busy ? (
                <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
              ) : accepted ? (
                <Check className="h-4 w-4" aria-hidden="true" />
              ) : canAccept ? (
                <Circle className="h-4 w-4" aria-hidden="true" />
              ) : (
                <CircleAlert className="h-4 w-4" aria-hidden="true" />
              )}
            </button>
          )
        },
      },
      {
        id: 'barcode',
        header: '',
        size: 40,
        cell: ({ row }) => {
          const item = row.original
          if (!barcodeEligible(item)) {
            return <span className="block h-7 w-7" />
          }
          return (
            <button
              type="button"
              className="flex h-7 w-7 items-center justify-center rounded-md text-neutral-500 transition-colors hover:bg-brand-subtle hover:text-brand"
              title="Scan barcode"
              aria-label={`Scan barcode for ${item.raw_name}`}
              onClick={(event) => {
                event.stopPropagation()
                setBarcodeItem(item)
              }}
            >
              <Camera className="h-4 w-4" aria-hidden="true" />
            </button>
          )
        },
      },
      {
        accessorKey: 'product_id',
        header: 'Product',
        size: 420,
        meta: {
          editable: true,
          cellType: 'autocomplete' as const,
          autocompleteOptions,
          onAutocompleteSearch: setProductSearch,
          onAutocompleteCreate: handleAutocompleteCreate,
          displayRenderer: ({ row }) => <ProductMatchDisplay item={row.original} />,
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
        header: 'Purchased Qty',
        size: 95,
        meta: {
          editable: true,
          cellType: 'number' as const,
        },
      },
      {
        accessorKey: 'unit',
        header: 'Purchased Unit',
        size: 110,
        meta: {
          editable: true,
          cellType: 'text' as const,
        },
      },
      {
        id: 'package_contents',
        header: 'Package Contents',
        size: 145,
        cell: ({ row }) => {
          const item = row.original
          const status = packageContentsStatus(item)
          if (status.kind === 'label') {
            return (
              <div className="min-w-0">
                <span className="block text-caption text-neutral-600">{status.label}</span>
                <span className="block text-small text-neutral-400">
                  {status.source === 'receipt' ? 'receipt override' : 'product default'}
                </span>
              </div>
            )
          }
          if (status.kind === 'none') {
            return <span className="text-caption text-neutral-300">—</span>
          }
          return (
            <button
              type="button"
              className="inline-flex"
              onClick={(event) => {
                event.stopPropagation()
                setSizeEditItem(item)
                setSizeQuantity(item.pack_quantity_override ?? (item.product_pack_quantity != null ? formatProductPackQuantity(item.product_pack_quantity) : ''))
                setSizeUnit(item.pack_unit_override ?? item.product_pack_unit ?? '')
                setSavePackageDefault(Boolean(item.product_id) && !hasProductPackageContents(item))
              }}
            >
              <Badge variant={status.kind === 'ambiguous' ? 'warning' : 'neutral'}>
                {status.kind === 'ambiguous' ? 'Check contents' : 'Set contents'}
              </Badge>
            </button>
          )
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
    [
      autocompleteOptions,
      productMap,
      handleAutocompleteCreate,
      suggestionMap,
      rows,
      handleToggleItemReview,
      reviewingItemId,
      receipt?.status,
    ],
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
    <div className="flex flex-col gap-4 pb-72">
      {/* Status bar */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-3">
          {showEmptyManualEntry ? (
            <Badge variant="warning">No line items</Badge>
          ) : (
            <>
              <Badge variant={reviewRemainingCount === 0 ? 'success' : 'warning'}>
                {reviewedCount}/{rows.length} reviewed
              </Badge>
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
            </>
          )}
          <span className="text-caption text-neutral-400">
            {receipt.status === 'reviewed' ? 'Reviewed' : receipt.status}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {barcodeEligibleRows.length > 0 && (
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={() => setBarcodeItem(barcodeEligibleRows[0] ?? null)}
            >
              <Camera className="mr-1 h-4 w-4" aria-hidden="true" />
              Scan next new item
            </Button>
          )}
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setRawJsonOpen(true)}
          >
            <FileCode2 className="mr-1 h-4 w-4" aria-hidden="true" />
            Raw JSON
          </Button>
          {showEmptyManualEntry ? (
            <Button
              type="button"
              variant="outlined"
              size="sm"
              onClick={() => {
                setRepairPreview(null)
                setRepairOpen(true)
              }}
            >
              <Wrench className="mr-1 h-4 w-4" aria-hidden="true" />
              Repair Scan
            </Button>
          ) : (
            <Button
              variant="primary"
              size="sm"
              onClick={async () => {
                if (!canConfirmReceipt) return
                setConfirmLoading(true)
                try {
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
              disabled={
                confirmLoading ||
                confirmMutation.isPending ||
                receipt.status === 'reviewed' ||
                !canConfirmReceipt
              }
              title={
                reviewRemainingCount > 0
                  ? `${reviewRemainingCount} line ${reviewRemainingCount === 1 ? 'item needs' : 'items need'} review`
                  : undefined
              }
            >
              {confirmLoading || confirmMutation.isPending
                ? 'Confirming...'
                : receipt.status === 'reviewed'
                  ? 'Confirmed'
                  : 'Confirm Receipt'}
            </Button>
          )}
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
            {!showEmptyManualEntry && (
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
            )}
          </div>
        </div>
      )}

      {identifierWarnings.length > 0 && (
        <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3">
          <div className="flex flex-col gap-1">
            {identifierWarnings.map((warning, index) => (
              <p key={`${warning.code}-${warning.line_item_id ?? 'receipt'}-${index}`} className="text-body text-amber-900">
                {warning.message}
              </p>
            ))}
          </div>
        </div>
      )}

      {showEmptyManualEntry && (
        <div className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3">
          <p className="text-body-medium text-amber-900">
            No readable line items were saved from this scan.
          </p>
        </div>
      )}

      {showEmptyManualEntry ? (
        <EmptyReceiptManualEntry
          key={`${receiptId}-${emptyManualEntryRowCount}`}
          initialRowCount={emptyManualEntryRowCount}
          isSaving={createLineItemsMutation.isPending}
          isError={createLineItemsMutation.isError}
          onSubmit={(items) => createLineItemsMutation.mutate(items)}
        />
      ) : (
        <EditableTable<LineItemRow>
          columns={columns}
          data={rows}
          onCellUpdate={handleCellUpdate}
          getRowClassName={getRowClassName}
          virtualizeRows={false}
          enableSorting={false}
        />
      )}

      {barcodeItem && (
        <Suspense fallback={null}>
          <BarcodeScannerModal
            open={barcodeItem !== null}
            mode="apply-to-line"
            receiptId={receiptId}
            lineItem={barcodeItem}
            onClose={() => setBarcodeItem(null)}
            onApplied={handleBarcodeApplied}
          />
        </Suspense>
      )}

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
                newRow.total_price.trim() === '' ||
                ((newRow.pack_quantity_override?.trim() ?? '') === '' ? (newRow.pack_unit_override?.trim() ?? '') !== '' : (newRow.pack_unit_override?.trim() ?? '') === '')
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
          <label className="col-span-2 flex flex-col gap-1 text-caption font-medium text-neutral-900">
            UPC
            <div className="flex gap-2">
              <input
                value={newRow.upc ?? ''}
                onChange={(e) => updateNewRow('upc', e.target.value)}
                inputMode="numeric"
                className="min-w-0 flex-1 rounded-lg border border-neutral-200 px-3 py-2 font-mono text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
              />
              <button
                type="button"
                className="inline-flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg border border-neutral-200 text-neutral-500 transition-colors hover:bg-brand-subtle hover:text-brand"
                onClick={() => setAddRowScannerOpen(true)}
                aria-label="Scan UPC"
                title="Scan UPC"
              >
                <Camera className="h-4 w-4" aria-hidden="true" />
              </button>
            </div>
          </label>
          <label className="flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Purchased Qty
            <input
              value={newRow.quantity ?? ''}
              onChange={(e) => updateNewRow('quantity', e.target.value)}
              className="rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </label>
          <label className="flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Purchased Unit
            <input
              value={newRow.unit ?? ''}
              onChange={(e) => updateNewRow('unit', e.target.value)}
              className="rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </label>
          <label className="flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Contents Qty
            <input
              value={newRow.pack_quantity_override ?? ''}
              onChange={(e) => updateNewRow('pack_quantity_override', e.target.value)}
              className="rounded-lg border border-neutral-200 px-3 py-2 text-body font-normal focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
            />
          </label>
          <label className="flex flex-col gap-1 text-caption font-medium text-neutral-900">
            Contents Unit
            <input
              value={newRow.pack_unit_override ?? ''}
              onChange={(e) => updateNewRow('pack_unit_override', e.target.value)}
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

      {addRowScannerOpen && (
        <Suspense fallback={null}>
          <BarcodeScannerModal
            open={addRowScannerOpen}
            mode="fill"
            title="Scan UPC"
            initialValue={newRow.upc ?? ''}
            onClose={() => setAddRowScannerOpen(false)}
            onFill={(value) => {
              updateNewRow('upc', value)
              setAddRowScannerOpen(false)
            }}
          />
        </Suspense>
      )}

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
                    <th className="px-2 py-1 text-left text-caption font-semibold text-neutral-600">Purchased</th>
                    <th className="px-2 py-1 text-left text-caption font-semibold text-neutral-600">Price</th>
                  </tr>
                </thead>
                <tbody>
                  {repairPreview.items.map((item, index) => (
                    <tr key={`${item.line_number}-${item.raw_name}-${index}`} className="border-t border-neutral-200">
                      <td className="px-2 py-1 text-caption text-neutral-600">{item.line_number}</td>
                      <td className="px-2 py-1 text-caption text-neutral-900 truncate">{item.raw_name}</td>
                      <td className="px-2 py-1 text-caption text-neutral-600">
                        {[item.quantity, item.unit].filter(Boolean).join(' ')}
                      </td>
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

      <Modal
        open={sizeEditItem !== null}
        onClose={closePackageContentsEditor}
        title="Package Contents"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={closePackageContentsEditor}>
              Cancel
            </Button>
            <Button
              size="sm"
              onClick={() => sizeMutation.mutate()}
              disabled={!sizeQuantity.trim() || !sizeUnit.trim() || sizeMutation.isPending}
            >
              {sizeMutation.isPending ? 'Saving...' : 'Save contents'}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <div>
            <p className="text-body-medium text-neutral-900">{sizeEditItem?.raw_name}</p>
            {sizeEditItem?.product_name && (
              <p className="text-caption text-neutral-400">{sizeEditItem.product_name}</p>
            )}
          </div>
          {sizeEditItem && (
            <div className="rounded-xl bg-neutral-50 px-3 py-2">
              <span className="block text-small font-medium text-neutral-400">Purchased on receipt</span>
              <span className="mt-0.5 block text-caption font-semibold text-neutral-900">
                {purchasedLabel(sizeEditItem)}
              </span>
            </div>
          )}
          <div className="grid grid-cols-[minmax(0,7rem)_minmax(0,1fr)] gap-2">
            <label className="block">
              <span className="mb-1 block text-small font-medium text-neutral-400">Contents Qty</span>
              <input
                type="number"
                min="0"
                step="any"
                value={sizeQuantity}
                onChange={(e) => setSizeQuantity(e.target.value)}
                className="w-full px-3 py-2 text-caption border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
                placeholder="32"
              />
            </label>
            <label className="block">
              <span className="mb-1 block text-small font-medium text-neutral-400">Contents Unit</span>
              <input
                type="text"
                value={sizeUnit}
                onChange={(e) => setSizeUnit(e.target.value)}
                className="w-full px-3 py-2 text-caption border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
                placeholder="oz"
              />
            </label>
          </div>
          <label className="flex items-start gap-2 rounded-xl border border-neutral-200 px-3 py-2 text-caption text-neutral-600">
            <input
              type="checkbox"
              className="mt-1 h-4 w-4 rounded border-neutral-300 text-brand focus:ring-brand"
              checked={savePackageDefault}
              disabled={!sizeEditItem?.product_id}
              onChange={(e) => setSavePackageDefault(e.target.checked)}
            />
            <span>
              <span className="block font-medium text-neutral-900">Use for future purchases of this product</span>
              <span className="block text-small text-neutral-400">
                {sizeEditItem && hasProductPackageContents(sizeEditItem)
                  ? 'Updates the existing product default'
                  : 'Product has no default contents yet'}
              </span>
            </span>
          </label>
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
                    const response = await acceptSuggestions(receiptId, { line_item_ids: suggestedRows.map(r => r.id) })
                    recordIdentifierWarnings(response)
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
