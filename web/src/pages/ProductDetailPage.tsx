import { useState, useRef, useCallback, useMemo, useEffect, lazy, Suspense, type ReactNode } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  Check,
  Camera,
  ExternalLink,
  Flame,
  Link2,
  Package as PackageIcon,
  Plus,
  RefreshCw,
  Search,
  ShieldAlert,
  Trash2,
  Wheat,
  X,
  Zap,
} from 'lucide-react'
import {
  getProductDetail,
  getProductUsage,
  deleteProduct,
  updateProduct,
  addProductLink,
  deleteProductLink,
  createProductEnrichmentJob,
  listProductEnrichmentJobs,
  acceptProductEnrichmentSuggestion,
  rejectProductEnrichmentSuggestion,
  bulkAcceptProductEnrichmentSuggestions,
  bulkRejectProductEnrichmentSuggestions,
  previewProductPriceRecompute,
  recomputeProductPrices,
  uploadProductImage,
  deleteProductImage,
  createProductAlias,
  deleteProductAlias,
  type ProductUsage,
} from '@/api/products'
import { getProductEnrichmentSettings } from '@/api/product-enrichment'
import { fetchGroups, fetchGroupSuggestions, createGroup } from '@/api/groups'
import { listStores } from '@/api/stores'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { Modal } from '@/components/ui/Modal'
import { ProductMerge } from '@/components/products/ProductMerge'
import type {
  ProductDetail,
  ProductImage,
  ProductAlias,
  Store,
  PriceHistoryEntry,
  ProductGroup,
  GroupSuggestion,
  ProductEnrichmentSuggestion,
  ProductNutrition,
  ProductEnrichmentJob,
  ProductExternalMetadata,
  ProductMetadataNutrients,
} from '@/types'

const BarcodeScannerModal = lazy(() =>
  import('@/components/receipts/BarcodeScannerModal').then((module) => ({
    default: module.BarcodeScannerModal,
  })),
)

// --- Helper ---

function formatPrice(price: string | null | undefined, unit?: string): string {
  if (!price) return '\u2014'
  const num = parseFloat(price)
  if (isNaN(num)) return '\u2014'
  const formatted = `$${num.toFixed(2)}`
  return unit ? `${formatted}/${unit}` : formatted
}

function pctChange(history: ProductDetail['price_history']): { pct: number; direction: 'up' | 'down' | 'flat' } {
  if (history.length < 2) return { pct: 0, direction: 'flat' }
  const latest = parseFloat(history[0]?.unit_price ?? '0')
  const oldest = parseFloat(history[history.length - 1]?.unit_price ?? '0')
  if (oldest === 0) return { pct: 0, direction: 'flat' }
  const pct = ((latest - oldest) / oldest) * 100
  return { pct: Math.abs(Math.round(pct)), direction: pct > 0.5 ? 'up' : pct < -0.5 ? 'down' : 'flat' }
}

function canonicalUnitPreview(unit: string): string {
  const normalized = unit.trim().toLowerCase().replace(/\s+/g, ' ')
  const aliases: Record<string, string> = {
    pounds: 'lb',
    pound: 'lb',
    lbs: 'lb',
    ounces: 'oz',
    ounce: 'oz',
    gallons: 'gal',
    gallon: 'gal',
    'fluid ounces': 'fl_oz',
    'fluid ounce': 'fl_oz',
    'fl oz': 'fl_oz',
    floz: 'fl_oz',
    each: 'each',
    ea: 'each',
    ct: 'each',
    count: 'each',
  }
  return aliases[normalized] ?? normalized
}

function isActiveEnrichmentJob(job: ProductEnrichmentJob): boolean {
  return job.status === 'queued' || job.status === 'running'
}

function enrichmentJobStatusLabel(job: ProductEnrichmentJob): string {
  switch (job.status) {
    case 'queued':
      return 'Queued'
    case 'running':
      return 'Looking up'
    case 'succeeded':
      return 'Lookup complete'
    case 'partial':
      return 'Lookup partially complete'
    case 'failed':
      return 'Lookup failed'
    case 'cancelled':
      return 'Lookup cancelled'
    default:
      return job.status
  }
}

function enrichmentProviderLabel(source: string): string {
  switch (source) {
    case 'openfoodfacts':
      return 'Open Food Facts'
    case 'usda_fdc':
      return 'USDA FoodData Central'
    case 'url':
      return 'URL'
    default:
      return source
  }
}

// --- Sub-components ---

function ProductCard({ children, className = '' }: { children: ReactNode; className?: string }) {
  return (
    <section className={['rounded-2xl border border-neutral-200 bg-white shadow-subtle', className].filter(Boolean).join(' ')}>
      {children}
    </section>
  )
}

function ProductCardTitle({ title, meta }: { title: string; meta?: string }) {
  return (
    <div className="flex flex-wrap items-baseline gap-2">
      <h2 className="font-display text-feature font-semibold text-neutral-900">{title}</h2>
      {meta && (
        <span className="text-small font-semibold uppercase text-neutral-400">
          {meta}
        </span>
      )}
    </div>
  )
}

function fieldSuggestions(detail: ProductDetail, fields: string[]): ProductEnrichmentSuggestion[] {
  const fieldSet = new Set(fields)
  return (detail.enrichment_suggestions ?? []).filter(
    (suggestion) => suggestion.status === 'pending' && fieldSet.has(suggestion.field),
  )
}

function InlineSuggestionList({
  productId,
  suggestions,
  showField = false,
  className = '',
}: {
  productId: string
  suggestions: ProductEnrichmentSuggestion[]
  showField?: boolean
  className?: string
}) {
  if (suggestions.length === 0) return null

  return (
    <div className={['space-y-1.5', className].filter(Boolean).join(' ')}>
      {suggestions.map((suggestion) => (
        <InlineSuggestion key={suggestion.id} productId={productId} suggestion={suggestion} showField={showField} />
      ))}
    </div>
  )
}

function InlineSuggestion({
  productId,
  suggestion,
  showField,
}: {
  productId: string
  suggestion: ProductEnrichmentSuggestion
  showField: boolean
}) {
  const queryClient = useQueryClient()
  const acceptMutation = useMutation({
    mutationFn: () => acceptProductEnrichmentSuggestion(productId, suggestion.id, { fields: [suggestion.field] }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      queryClient.invalidateQueries({ queryKey: ['products'] })
    },
  })
  const rejectMutation = useMutation({
    mutationFn: () => rejectProductEnrichmentSuggestion(productId, suggestion.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
    },
  })
  const mutationPending = acceptMutation.isPending || rejectMutation.isPending

  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-neutral-500">
      <span className="inline-flex items-center gap-1 rounded-lg bg-brand-subtle px-2 py-0.5 text-small font-semibold text-brand">
        <Zap className="h-3 w-3" aria-hidden="true" />
        {sourceLabel(suggestion.source)}
      </span>
      <span>suggests</span>
      {showField && <span className="font-semibold text-neutral-900">{fieldLabel(suggestion.field)}</span>}
      <span className="font-semibold text-neutral-900">{suggestion.value}</span>
      {suggestion.evidence && (
        <span className="text-neutral-400">{suggestion.evidence}</span>
      )}
      <span className="text-neutral-300" aria-hidden="true">·</span>
      <button
        type="button"
        className="inline-flex items-center gap-1 font-semibold text-brand transition-colors hover:text-brand-deep disabled:opacity-50"
        onClick={() => acceptMutation.mutate()}
        disabled={mutationPending}
      >
        <Check className="h-3.5 w-3.5" aria-hidden="true" />
        Accept
      </button>
      <span className="text-neutral-300" aria-hidden="true">·</span>
      <button
        type="button"
        className="inline-flex items-center gap-1 font-medium text-neutral-500 transition-colors hover:text-neutral-900 disabled:opacity-50"
        onClick={() => rejectMutation.mutate()}
        disabled={mutationPending}
      >
        <X className="h-3.5 w-3.5" aria-hidden="true" />
        Dismiss
      </button>
    </div>
  )
}

function ProductInfoRow({ label, hint, children }: { label: string; hint: string; children: ReactNode }) {
  return (
    <div className="grid gap-3 px-5 py-4 sm:grid-cols-[190px_minmax(0,1fr)] sm:gap-6">
      <div>
        <h3 className="text-caption font-semibold text-neutral-900">{label}</h3>
        <p className="mt-0.5 text-small text-neutral-400">{hint}</p>
      </div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function ProductInfoSection({ detail, productId }: { detail: ProductDetail; productId: string }) {
  const queryClient = useQueryClient()
  const [brand, setBrand] = useState(detail.product.brand ?? '')
  const [upc, setUpc] = useState(detail.product.upc ?? '')
  const [packQuantity, setPackQuantity] = useState(detail.product.pack_quantity?.toString() ?? '')
  const [packUnit, setPackUnit] = useState(detail.product.pack_unit ?? '')
  const [confirmMode, setConfirmMode] = useState<'save' | 'recompute' | null>(null)
  const [affectedCount, setAffectedCount] = useState<number | null>(null)
  const [showPriceBasis, setShowPriceBasis] = useState(false)
  const [scannerOpen, setScannerOpen] = useState(false)

  const updateMutation = useMutation({
    mutationFn: (data: { brand?: string; upc?: string | null; pack_quantity?: number; pack_unit?: string }) =>
      updateProduct(productId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
    },
  })

  const handleSaveBrand = useCallback(() => {
    updateMutation.mutate({ brand: brand || undefined })
  }, [brand, updateMutation])

  const handleSaveUPC = useCallback(() => {
    updateMutation.mutate({ upc: upc.trim() || null })
  }, [upc, updateMutation])

  const jobsQuery = useQuery({
    queryKey: ['product-enrichment-jobs', productId],
    queryFn: () => listProductEnrichmentJobs(productId),
    enabled: !!productId,
    refetchInterval: (query) => {
      const jobs = query.state.data?.jobs ?? []
      return jobs.some(isActiveEnrichmentJob) ? 2000 : false
    },
  })
  const jobs = jobsQuery.data?.jobs ?? []
  const activeLookupJob = jobs.find(isActiveEnrichmentJob)
  const latestLookupJob = jobs[0]

  const { data: enrichmentSettings, isLoading: enrichmentSettingsLoading } = useQuery({
    queryKey: ['product-enrichment-settings'],
    queryFn: getProductEnrichmentSettings,
  })

  useEffect(() => {
    if (latestLookupJob && !isActiveEnrichmentJob(latestLookupJob)) {
      void queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
    }
  }, [latestLookupJob?.id, latestLookupJob?.status, productId, queryClient])

  const lookupMutation = useMutation({
    mutationFn: (sources?: string[]) =>
      createProductEnrichmentJob(productId, {
        trigger: 'manual_lookup',
        upc: upc.trim(),
        sources,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-enrichment-jobs', productId] })
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
    },
  })

  const previewMutation = useMutation({
    mutationFn: () => previewProductPriceRecompute(productId),
    onSuccess: (data) => {
      setAffectedCount(data.affected_count)
    },
  })

  const recomputeMutation = useMutation({
    mutationFn: () => recomputeProductPrices(productId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      setConfirmMode(null)
      setAffectedCount(null)
    },
  })

  const packPayload = useCallback(() => {
    const qty = packQuantity ? parseFloat(packQuantity) : undefined
    return {
      pack_quantity: qty,
      pack_unit: packUnit || undefined,
    }
  }, [packQuantity, packUnit])

  const openSaveConfirm = useCallback(() => {
    setConfirmMode('save')
    setAffectedCount(null)
    previewMutation.mutate()
  }, [previewMutation])

  const openRecomputeConfirm = useCallback(() => {
    setConfirmMode('recompute')
    setAffectedCount(null)
    previewMutation.mutate()
  }, [previewMutation])

  const handleSaveOnly = useCallback(() => {
    updateMutation.mutate(packPayload(), {
      onSuccess: () => {
        setConfirmMode(null)
        setAffectedCount(null)
      },
    })
  }, [packPayload, updateMutation])

  const handleSaveAndRecompute = useCallback(() => {
    updateMutation.mutate(packPayload(), {
      onSuccess: () => {
        recomputeMutation.mutate()
      },
    })
  }, [packPayload, updateMutation, recomputeMutation])

  const handleRecomputeOnly = useCallback(() => {
    recomputeMutation.mutate()
  }, [recomputeMutation])

  // Compute price per unit from latest price
  const latestEntry = detail.price_history[0]
  const latestPrice = latestEntry ? parseFloat(latestEntry.unit_price ?? '0') : null
  const packQty = detail.product.pack_quantity
  const pricePerUnit = (latestPrice && packQty && packQty > 0)
    ? latestPrice / packQty
    : null
  const latestNormalized = latestEntry?.normalized_price && latestEntry.normalized_unit ? latestEntry : undefined
  const productContents = packQty && detail.product.pack_unit
    ? `${packQty} ${detail.product.pack_unit}`
    : null
  const latestPurchased = latestEntry
    ? `${latestEntry.quantity} ${latestEntry.unit || detail.product.default_unit || 'each'}`
    : null
  const comparedPrice = latestNormalized?.normalized_price && latestNormalized.normalized_unit
    ? `$${parseFloat(latestNormalized.normalized_price).toFixed(2)} / ${latestNormalized.normalized_unit}`
    : pricePerUnit != null
      ? `$${pricePerUnit.toFixed(2)} / ${detail.product.pack_unit ?? 'unit'}`
      : null
  const packChanged =
    packQuantity !== (detail.product.pack_quantity?.toString() ?? '') ||
    packUnit !== (detail.product.pack_unit ?? '')
  const canonicalUnit = packUnit.trim() ? canonicalUnitPreview(packUnit) : ''
  const savingPack = updateMutation.isPending || recomputeMutation.isPending
  const providerAvailability = enrichmentSettings?.provider_availability ?? {}
  const manualLookupEnabled = Boolean(enrichmentSettings?.global_enabled && enrichmentSettings?.manual_lookup_enabled)
  const openFoodFactsEnabled = manualLookupEnabled && Boolean(providerAvailability.openfoodfacts?.enabled)
  const usdaEnabled = manualLookupEnabled && Boolean(providerAvailability.usda_fdc?.enabled)
  const lookupDisabled = enrichmentSettingsLoading || upc.trim().length === 0 || lookupMutation.isPending || !!activeLookupJob || !manualLookupEnabled
  let lookupHelper: string | null = null
  if (!enrichmentSettingsLoading) {
    if (upc.trim().length === 0) {
      lookupHelper = 'Add a UPC to search barcode providers.'
    } else if (!manualLookupEnabled) {
      lookupHelper = 'Manual lookup is disabled in Settings.'
    } else if (providerAvailability.usda_fdc?.reason && !usdaEnabled) {
      lookupHelper = `USDA unavailable: ${providerAvailability.usda_fdc.reason}.`
    }
  }
  const latestLookupSources = latestLookupJob?.requested_sources?.length
    ? latestLookupJob.requested_sources.map(enrichmentProviderLabel).join(', ')
    : null
  const brandSuggestions = fieldSuggestions(detail, ['brand'])
  const upcSuggestions = fieldSuggestions(detail, ['upc'])
  const packageSuggestions = fieldSuggestions(detail, ['pack_quantity', 'pack_unit'])

  return (
    <>
      <ProductCard className="overflow-hidden">
        <div className="border-b border-neutral-200 px-5 py-4">
          <ProductCardTitle title="Product Info" meta="Editable Fields" />
        </div>
        <div className="divide-y divide-neutral-200">
          <ProductInfoRow label="Brand" hint="Saves automatically">
            <div className="max-w-sm">
              <input
                type="text"
                value={brand}
                onChange={(e) => setBrand(e.target.value)}
                placeholder="e.g., Kirkland, Great Value"
                className="w-full rounded-xl border border-neutral-200 px-3 py-2 text-caption focus:border-transparent focus:outline-none focus:ring-2 focus:ring-brand"
                onBlur={handleSaveBrand}
                onKeyDown={(e) => { if (e.key === 'Enter') handleSaveBrand() }}
              />
            </div>
            <InlineSuggestionList productId={productId} suggestions={brandSuggestions} className="mt-2" />
          </ProductInfoRow>

          <ProductInfoRow label="UPC" hint="Lookup required before save">
            <div className="flex max-w-lg gap-2">
              <input
                type="text"
                value={upc}
                onChange={(e) => setUpc(e.target.value)}
                placeholder="Barcode"
                inputMode="numeric"
                className="min-w-0 flex-1 rounded-xl border border-neutral-200 px-3 py-2 text-caption focus:border-transparent focus:outline-none focus:ring-2 focus:ring-brand"
                onBlur={handleSaveUPC}
                onKeyDown={(e) => { if (e.key === 'Enter') handleSaveUPC() }}
              />
              <button
                type="button"
                className="inline-flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-xl border border-neutral-200 text-neutral-500 transition-colors hover:bg-brand-subtle hover:text-brand"
                onClick={() => setScannerOpen(true)}
                title="Scan UPC"
                aria-label="Scan UPC"
              >
                <Camera className="h-4 w-4" aria-hidden="true" />
              </button>
            </div>
            <div className="mt-2 flex flex-wrap gap-2">
              <Button
                size="sm"
                variant="subtle"
                className="gap-1.5"
                onClick={() => lookupMutation.mutate(['openfoodfacts'])}
                disabled={lookupDisabled || !openFoodFactsEnabled}
                title={!openFoodFactsEnabled ? 'Open Food Facts lookup is disabled in Settings.' : undefined}
              >
                <Search className="h-3.5 w-3.5" aria-hidden="true" />
                Search Open Food Facts
              </Button>
              <Button
                size="sm"
                variant="subtle"
                className="gap-1.5"
                onClick={() => lookupMutation.mutate(['usda_fdc'])}
                disabled={lookupDisabled || !usdaEnabled}
                title={providerAvailability.usda_fdc?.reason || undefined}
              >
                <Search className="h-3.5 w-3.5" aria-hidden="true" />
                Search USDA
              </Button>
              <Button
                size="sm"
                variant="outlined"
                className="gap-1.5"
                onClick={() => lookupMutation.mutate(['openfoodfacts', 'usda_fdc'])}
                disabled={lookupDisabled || (!openFoodFactsEnabled && !usdaEnabled)}
              >
                <Search className="h-3.5 w-3.5" aria-hidden="true" />
                Lookup all
              </Button>
            </div>
            {lookupHelper && (
              <p className="mt-1 text-small text-neutral-400">{lookupHelper}</p>
            )}
            {latestLookupJob && (
              <div className="mt-2 text-small">
                <span className={latestLookupJob.status === 'failed' ? 'text-expensive' : 'text-neutral-400'}>
                  {enrichmentJobStatusLabel(latestLookupJob)}
                </span>
                {latestLookupSources && (
                  <span className="ml-1 text-neutral-400">({latestLookupSources})</span>
                )}
                {latestLookupJob.last_error && (
                  <span className="ml-1 text-expensive">{latestLookupJob.last_error}</span>
                )}
              </div>
            )}
            <InlineSuggestionList productId={productId} suggestions={upcSuggestions} className="mt-2" />
          </ProductInfoRow>

          <ProductInfoRow label="Package contents" hint="What one purchased package contains">
            <div className="flex max-w-2xl flex-col gap-2 md:flex-row md:items-center">
              <input
                type="number"
                value={packQuantity}
                onChange={(e) => setPackQuantity(e.target.value)}
                placeholder="e.g., 12"
                min="0"
                step="any"
                className="w-full rounded-xl border border-neutral-200 px-3 py-2 text-caption focus:border-transparent focus:outline-none focus:ring-2 focus:ring-brand md:w-32"
                onKeyDown={(e) => { if (e.key === 'Enter' && packChanged) openSaveConfirm() }}
              />
              <input
                type="text"
                value={packUnit}
                onChange={(e) => setPackUnit(e.target.value)}
                placeholder="unit (e.g., oz, ct)"
                className="min-w-0 flex-1 rounded-xl border border-neutral-200 px-3 py-2 text-caption focus:border-transparent focus:outline-none focus:ring-2 focus:ring-brand"
                onKeyDown={(e) => { if (e.key === 'Enter' && packChanged) openSaveConfirm() }}
              />
              <Button size="sm" onClick={openSaveConfirm} disabled={!packChanged || savingPack}>
                {updateMutation.isPending ? 'Saving...' : 'Save contents'}
              </Button>
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-small text-neutral-400">
              {canonicalUnit && <span>Canonical: {canonicalUnit}</span>}
            </div>
            <InlineSuggestionList productId={productId} suggestions={packageSuggestions} className="mt-2" showField />
          </ProductInfoRow>

          <ProductInfoRow label="Price basis" hint="How CartLedger compares this product">
            <div className="space-y-3">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                {comparedPrice ? (
                  <p className="font-mono text-feature font-semibold text-success-dark">
                    {comparedPrice}
                  </p>
                ) : (
                  <p className="text-caption text-neutral-400">
                    Add package contents to compare prices
                  </p>
                )}
                <Button
                  size="sm"
                  variant="subtle"
                  onClick={() => setShowPriceBasis((open) => !open)}
                >
                  {showPriceBasis ? 'Hide basis' : 'Why this price?'}
                </Button>
                <Button size="sm" variant="subtle" onClick={openRecomputeConfirm} disabled={savingPack}>
                  {recomputeMutation.isPending ? 'Recomputing...' : 'Recompute price history'}
                </Button>
              </div>
              {showPriceBasis && (
                <div className="grid gap-2 rounded-xl border border-neutral-200 bg-neutral-50 p-3 text-caption sm:grid-cols-2">
                  <div>
                    <span className="block text-small font-medium text-neutral-400">Latest receipt price</span>
                    <span className="mt-0.5 block font-semibold text-neutral-900">
                      {latestEntry ? `$${Number(latestEntry.total_price).toFixed(2)}` : '\u2014'}
                    </span>
                  </div>
                  <div>
                    <span className="block text-small font-medium text-neutral-400">Purchased as</span>
                    <span className="mt-0.5 block font-semibold text-neutral-900">
                      {latestPurchased ?? '\u2014'}
                    </span>
                  </div>
                  <div>
                    <span className="block text-small font-medium text-neutral-400">Product default contents</span>
                    <span className="mt-0.5 block font-semibold text-neutral-900">
                      {productContents ?? 'Not set'}
                    </span>
                  </div>
                  <div>
                    <span className="block text-small font-medium text-neutral-400">Compared price</span>
                    <span className="mt-0.5 block font-semibold text-neutral-900">
                      {comparedPrice ?? '\u2014'}
                    </span>
                  </div>
                </div>
              )}
            </div>
          </ProductInfoRow>
        </div>
      </ProductCard>

      {scannerOpen && (
        <Suspense fallback={null}>
          <BarcodeScannerModal
            open={scannerOpen}
            mode="fill"
            title="Scan UPC"
            initialValue={upc}
            onClose={() => setScannerOpen(false)}
            onFill={(value) => {
              setUpc(value)
            }}
          />
        </Suspense>
      )}

      <Modal
        open={confirmMode !== null}
        onClose={() => {
          if (!savingPack) {
            setConfirmMode(null)
            setAffectedCount(null)
          }
        }}
        title={confirmMode === 'save' ? 'Save Package Contents' : 'Recompute Price History'}
        footer={(
          <>
            <Button variant="secondary" size="sm" onClick={() => setConfirmMode(null)} disabled={savingPack}>
              Cancel
            </Button>
            {confirmMode === 'save' && (
              <Button variant="secondary" size="sm" onClick={handleSaveOnly} disabled={savingPack}>
                Save only
              </Button>
            )}
            <Button
              size="sm"
              onClick={confirmMode === 'save' ? handleSaveAndRecompute : handleRecomputeOnly}
              disabled={savingPack || previewMutation.isPending}
            >
              {confirmMode === 'save' ? 'Save and recompute' : 'Recompute'}
            </Button>
          </>
        )}
      >
        <p className="text-body text-neutral-600">
          {previewMutation.isPending || affectedCount === null
            ? 'Checking linked purchase history...'
            : confirmMode === 'save'
              ? `Save these package contents and recompute ${affectedCount} linked historical purchases?`
              : `Recompute ${affectedCount} linked historical purchases from their receipt lines?`}
        </p>
      </Modal>
    </>
  )
}

function PriceTrendSection({ detail }: { detail: ProductDetail }) {
  const { pct, direction } = pctChange(detail.price_history)
  // Sparkline placeholder — real chart in Phase 5
  const recentHistory = detail.price_history.slice(0, 12).reverse()
  const bars = recentHistory.map((e) => parseFloat(e.unit_price))

  const max = Math.max(...bars, 1)
  const min = Math.min(...bars, 0)
  const range = max - min || 1

  return (
    <ProductCard className="p-5">
      <div className="flex items-center justify-between mb-3">
        <ProductCardTitle title="Price Trend" />
        {direction !== 'flat' && (
          <Badge variant={direction === 'up' ? 'error' : 'success'}>
            {direction === 'up' ? '+' : '-'}{pct}% {direction === 'up' ? '\u2191' : '\u2193'}
          </Badge>
        )}
      </div>
      {bars.length > 1 ? (
        <div className="flex items-end gap-1 h-16">
          {bars.map((val, i) => {
            const height = ((val - min) / range) * 100
            const isSale = recentHistory[i]?.is_sale ?? false
            return (
              <div
                key={i}
                className={`flex-1 rounded-sm min-h-[4px] ${isSale ? 'bg-green-500' : 'bg-brand'}`}
                style={{ height: `${Math.max(height, 6)}%` }}
                title={isSale ? `Sale price: $${isNaN(val) ? '0.00' : val.toFixed(2)}` : `$${isNaN(val) ? '0.00' : val.toFixed(2)}`}
              />
            )
          })}
        </div>
      ) : (
        <p className="text-caption text-neutral-400">Not enough data for trend</p>
      )}
      {detail.stats.total_purchases > 0 && (
        <div className="flex gap-4 mt-3 text-small text-neutral-500">
          <span>Avg: {formatPrice(detail.stats.avg_price)}</span>
          <span>Min: {formatPrice(detail.stats.min_price)}</span>
          <span>Max: {formatPrice(detail.stats.max_price)}</span>
          <span>{detail.stats.total_purchases} purchases</span>
        </div>
      )}
      {detail.stats.total_saved && parseFloat(detail.stats.total_saved) > 0 && (
        <div className="mt-2 text-sm text-green-600">
          Total saved: ${Number(detail.stats.total_saved).toFixed(2)}
        </div>
      )}
    </ProductCard>
  )
}

function isSourceImageSuggestion(suggestion: ProductEnrichmentSuggestion): boolean {
  return ['image_front_url', 'image_nutrition_url', 'image_ingredients_url', 'image_packaging_url'].includes(suggestion.field)
}

function sourceImageSuggestionLabel(field: string): string {
  const labels: Record<string, string> = {
    image_front_url: 'Front photo',
    image_nutrition_url: 'Nutrition photo',
    image_ingredients_url: 'Ingredients photo',
    image_packaging_url: 'Package photo',
  }
  return labels[field] ?? 'Source photo'
}

function productImageURL(productId: string, image: ProductImage): string {
  return `${window.location.origin}/api/v1/products/${encodeURIComponent(productId)}/images/${encodeURIComponent(image.id)}/file`
}

function SourcePhotoSuggestion({ productId, suggestion }: { productId: string; suggestion: ProductEnrichmentSuggestion }) {
  const queryClient = useQueryClient()
  const acceptMutation = useMutation({
    mutationFn: () => acceptProductEnrichmentSuggestion(productId, suggestion.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      queryClient.invalidateQueries({ queryKey: ['products'] })
    },
  })
  const rejectMutation = useMutation({
    mutationFn: () => rejectProductEnrichmentSuggestion(productId, suggestion.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
    },
  })
  const mutationPending = acceptMutation.isPending || rejectMutation.isPending

  return (
    <div className="flex flex-col gap-3 rounded-xl border border-brand/20 bg-brand-subtle/50 p-3 sm:flex-row sm:items-center">
      <a
        href={suggestion.value}
        target="_blank"
        rel="noopener noreferrer"
        className="block h-20 w-20 shrink-0 overflow-hidden rounded-lg border border-neutral-200 bg-white"
        aria-label={`Open ${sourceImageSuggestionLabel(suggestion.field).toLowerCase()}`}
      >
        <img
          src={suggestion.value}
          alt={sourceImageSuggestionLabel(suggestion.field)}
          className="h-full w-full object-cover"
          referrerPolicy="no-referrer"
          loading="lazy"
        />
      </a>
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2 text-caption">
          <span className="inline-flex items-center gap-1 rounded-lg bg-white px-2 py-0.5 text-small font-semibold text-brand">
            <Zap className="h-3 w-3" aria-hidden="true" />
            {sourceLabel(suggestion.source)}
          </span>
          <span className="font-semibold text-neutral-900">suggests</span>
          <span className="font-semibold text-neutral-900">{sourceImageSuggestionLabel(suggestion.field)}</span>
          {suggestion.evidence && <span className="text-neutral-400">{suggestion.evidence}</span>}
        </div>
      </div>
      <div className="flex shrink-0 flex-wrap gap-2">
        <Button
          size="sm"
          className="gap-1.5"
          onClick={() => acceptMutation.mutate()}
          disabled={mutationPending}
        >
          <Check className="h-3.5 w-3.5" aria-hidden="true" />
          {acceptMutation.isPending ? 'Saving...' : 'Save photo'}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          className="gap-1.5"
          onClick={() => rejectMutation.mutate()}
          disabled={mutationPending}
        >
          <X className="h-3.5 w-3.5" aria-hidden="true" />
          {rejectMutation.isPending ? 'Dismissing...' : 'Dismiss'}
        </Button>
      </div>
    </div>
  )
}

function PhotosSection({ detail, productId }: { detail: ProductDetail; productId: string }) {
  const queryClient = useQueryClient()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState(false)
  const [lightboxImage, setLightboxImage] = useState<ProductImage | null>(null)
  const [deleteConfirm, setDeleteConfirm] = useState<ProductImage | null>(null)
  const sourcePhotoSuggestions = detail.enrichment_suggestions.filter(isSourceImageSuggestion)

  const uploadMutation = useMutation({
    mutationFn: (file: File) => uploadProductImage(productId, file),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      setUploading(false)
    },
    onError: () => {
      setUploading(false)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (imageId: string) => deleteProductImage(productId, imageId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      setDeleteConfirm(null)
    },
  })

  const handleFileSelect = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const file = e.target.files?.[0]
      if (!file) return

      // Validate type and size
      if (!['image/jpeg', 'image/png'].includes(file.type)) {
        alert('Only JPEG and PNG images are supported.')
        return
      }
      if (file.size > 10 * 1024 * 1024) {
        alert('Image must be under 10MB.')
        return
      }

      setUploading(true)
      uploadMutation.mutate(file)
      // Reset input so same file can be re-selected
      e.target.value = ''
    },
    [uploadMutation],
  )

  return (
    <>
      <ProductCard className="p-5">
        <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <ProductCardTitle title="Photos" meta={detail.images.length === 0 ? 'None Yet' : `${detail.images.length} saved`} />
          <Button
            size="sm"
            variant="subtle"
            className="gap-1.5"
            onClick={() => fileInputRef.current?.click()}
            disabled={uploading}
          >
            <Plus className="h-3.5 w-3.5" aria-hidden="true" />
            {uploading ? 'Uploading...' : 'Add Photo'}
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            accept="image/jpeg,image/png"
            className="hidden"
            onChange={handleFileSelect}
          />
        </div>
        {sourcePhotoSuggestions.length > 0 && (
          <div className="mb-4 space-y-2">
            {sourcePhotoSuggestions.map((suggestion) => (
              <SourcePhotoSuggestion key={suggestion.id} productId={productId} suggestion={suggestion} />
            ))}
          </div>
        )}
        {detail.images.length === 0 ? (
          <div className="grid max-w-2xl grid-cols-2 gap-3 sm:grid-cols-4">
            {['primary', 'add', 'add', 'add'].map((label, index) => (
              <button
                key={`${label}-${index}`}
                type="button"
                className="aspect-square rounded-xl border border-dashed border-neutral-200 bg-neutral-50 text-small font-semibold text-neutral-400 transition-colors hover:border-brand hover:bg-brand-subtle hover:text-brand"
                onClick={() => fileInputRef.current?.click()}
              >
                {label}
              </button>
            ))}
          </div>
        ) : (
          <div className="grid grid-cols-3 sm:grid-cols-4 md:grid-cols-5 gap-3">
            {detail.images.map((img) => (
              <div key={img.id} className="relative group">
                <button
                  type="button"
                  className="w-full aspect-square rounded-xl overflow-hidden bg-neutral-50 border border-neutral-200 hover:border-brand transition-colors cursor-pointer"
                  onClick={() => setLightboxImage(img)}
                >
                  <img
                    src={productImageURL(productId, img)}
                    alt={img.caption ?? 'Product photo'}
                    className="w-full h-full object-cover"
                  />
                </button>
                <button
                  type="button"
                  className="absolute top-1 right-1 w-6 h-6 bg-white/80 rounded-full flex items-center justify-center text-neutral-500 hover:text-expensive opacity-0 group-hover:opacity-100 transition-opacity cursor-pointer"
                  onClick={() => setDeleteConfirm(img)}
                  aria-label="Delete image"
                >
                  <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>
            ))}
          </div>
        )}
      </ProductCard>

      {/* Lightbox Modal */}
      <Modal open={!!lightboxImage} onClose={() => setLightboxImage(null)}>
        {lightboxImage && (
          <img
            src={productImageURL(productId, lightboxImage)}
            alt={lightboxImage.caption ?? 'Product photo'}
            className="w-full rounded-xl"
          />
        )}
      </Modal>

      {/* Delete Confirmation Modal */}
      <Modal
        open={!!deleteConfirm}
        onClose={() => setDeleteConfirm(null)}
        title="Delete Photo"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setDeleteConfirm(null)}>
              Cancel
            </Button>
            <Button
              size="sm"
              className="bg-expensive text-white hover:opacity-90"
              onClick={() => deleteConfirm && deleteMutation.mutate(deleteConfirm.id)}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-600">Are you sure you want to delete this photo? This cannot be undone.</p>
      </Modal>
    </>
  )
}

function aliasSourceLabel(source?: string | null): string {
  switch (source) {
    case 'receipt_match':
      return 'Receipt'
    case 'manual_match':
      return 'Accepted'
    case 'user_alias':
      return 'Manual'
    case 'import':
      return 'Import'
    case 'enrichment':
      return 'Enriched'
    case 'legacy':
      return 'Legacy'
    default:
      return 'Alias'
  }
}

function AliasChip({ alias, storeName, onDelete }: { alias: ProductAlias; storeName: string | null; onDelete: () => void }) {
  return (
    <span className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-neutral-50 rounded-xl text-caption text-neutral-600">
      &quot;{alias.alias}&quot;
      <Badge variant={alias.source === 'user_alias' || alias.source === 'manual_match' ? 'success' : 'neutral'}>
        {aliasSourceLabel(alias.source)}
      </Badge>
      {storeName && (
        <Badge variant="neutral">{storeName}</Badge>
      )}
      <button
        type="button"
        className="ml-1 text-neutral-400 hover:text-expensive transition-colors cursor-pointer"
        onClick={onDelete}
        aria-label={`Delete alias ${alias.alias}`}
      >
        <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </span>
  )
}

function AliasesSection({ detail, productId, stores }: { detail: ProductDetail; productId: string; stores: Store[] }) {
  const queryClient = useQueryClient()
  const [showAdd, setShowAdd] = useState(false)
  const [newAlias, setNewAlias] = useState('')
  const [newAliasStoreId, setNewAliasStoreId] = useState('')
  const [deleteConfirm, setDeleteConfirm] = useState<ProductAlias | null>(null)

  const createMutation = useMutation({
    mutationFn: (data: { alias: string; store_id?: string }) =>
      createProductAlias(productId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      queryClient.invalidateQueries({ queryKey: ['product-aliases', productId] })
      setNewAlias('')
      setNewAliasStoreId('')
      setShowAdd(false)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (aliasId: string) => deleteProductAlias(productId, aliasId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      queryClient.invalidateQueries({ queryKey: ['product-aliases', productId] })
      setDeleteConfirm(null)
    },
  })

  const handleAddAlias = useCallback(() => {
    if (!newAlias.trim()) return
    createMutation.mutate({
      alias: newAlias.trim(),
      store_id: newAliasStoreId || undefined,
    })
  }, [newAlias, newAliasStoreId, createMutation])

  const storeNameById = (storeId: string | null): string | null => {
    if (!storeId) return null
    return stores.find((s) => s.id === storeId)?.name ?? null
  }

  // Group aliases: global vs store-specific
  const { globalAliases, storeAliases } = useMemo(() => {
    const global: ProductAlias[] = []
    const store: ProductAlias[] = []
    for (const a of detail.aliases) {
      if (a.store_id) {
        store.push(a)
      } else {
        global.push(a)
      }
    }
    return { globalAliases: global, storeAliases: store }
  }, [detail.aliases])

  return (
    <>
      <ProductCard className="p-5">
        <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <ProductCardTitle title="Aliases" meta="Store-Specific Names" />
          <Button size="sm" variant="subtle" className="gap-1.5" onClick={() => setShowAdd(!showAdd)}>
            {showAdd ? (
              <>
                <X className="h-3.5 w-3.5" aria-hidden="true" />
                Cancel
              </>
            ) : (
              <>
                <Plus className="h-3.5 w-3.5" aria-hidden="true" />
                Add Alias
              </>
            )}
          </Button>
        </div>

        {showAdd && (
          <div className="flex flex-col sm:flex-row gap-2 mb-4 p-3 bg-neutral-50 rounded-xl">
            <input
              type="text"
              value={newAlias}
              onChange={(e) => setNewAlias(e.target.value)}
              placeholder="Alias name (e.g., BNLS CHKN BRST)"
              className="flex-1 px-3 py-2 text-caption border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleAddAlias()
              }}
            />
            <select
              value={newAliasStoreId}
              onChange={(e) => setNewAliasStoreId(e.target.value)}
              className="px-3 py-2 text-caption border border-neutral-200 rounded-xl bg-white focus:outline-none focus:ring-2 focus:ring-brand"
            >
              <option value="">Any store (global)</option>
              {stores.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
            <Button size="sm" onClick={handleAddAlias} disabled={!newAlias.trim() || createMutation.isPending}>
              {createMutation.isPending ? 'Adding...' : 'Add'}
            </Button>
          </div>
        )}

        {detail.aliases.length === 0 ? (
          <p className="text-caption text-neutral-400">No aliases yet.</p>
        ) : (
          <div className="space-y-3">
            {/* Global aliases */}
            {globalAliases.length > 0 && (
              <div>
                <p className="text-small font-medium text-neutral-400 mb-1.5">Global aliases</p>
                <div className="flex flex-wrap gap-2">
                  {globalAliases.map((a) => (
                    <AliasChip
                      key={a.id}
                      alias={a}
                      storeName={null}
                      onDelete={() => setDeleteConfirm(a)}
                    />
                  ))}
                </div>
              </div>
            )}

            {/* Store-specific aliases */}
            {storeAliases.length > 0 && (
              <div>
                <p className="text-small font-medium text-neutral-400 mb-1.5">Store-specific aliases</p>
                <div className="flex flex-wrap gap-2">
                  {storeAliases.map((a) => (
                    <AliasChip
                      key={a.id}
                      alias={a}
                      storeName={storeNameById(a.store_id)}
                      onDelete={() => setDeleteConfirm(a)}
                    />
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </ProductCard>

      {/* Delete Alias Confirmation */}
      <Modal
        open={!!deleteConfirm}
        onClose={() => setDeleteConfirm(null)}
        title="Delete Alias"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setDeleteConfirm(null)}>
              Cancel
            </Button>
            <Button
              size="sm"
              className="bg-expensive text-white hover:opacity-90"
              onClick={() => deleteConfirm && deleteMutation.mutate(deleteConfirm.id)}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-600">
          Delete alias &quot;{deleteConfirm?.alias}&quot;? This cannot be undone.
        </p>
      </Modal>
    </>
  )
}

function StoreCodesSection({ detail }: { detail: ProductDetail }) {
  const codes = detail.store_codes ?? []
  return (
    <ProductCard className="p-5">
      <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <ProductCardTitle title="Store SKUs / PLUs" meta={`${codes.length} mapped`} />
      </div>
      {codes.length === 0 ? (
        <p className="text-caption text-neutral-400">No store SKUs or PLUs mapped yet.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-neutral-200">
                <th className="py-2 text-small font-medium text-neutral-400">Store</th>
                <th className="py-2 text-small font-medium text-neutral-400">SKU / PLU</th>
                <th className="py-2 text-small font-medium text-neutral-400">Source</th>
                <th className="py-2 text-small font-medium text-neutral-400 text-right">Last Seen</th>
              </tr>
            </thead>
            <tbody>
              {codes.map((code) => (
                <tr key={code.id} className="border-b border-neutral-100 last:border-0">
                  <td className="py-2 text-caption text-neutral-900">{code.store_name}</td>
                  <td className="py-2">
                    <span className="rounded-md bg-neutral-50 px-2 py-1 font-mono text-caption text-neutral-700">
                      {code.store_item_code}
                    </span>
                  </td>
                  <td className="py-2">
                    <Badge variant="neutral">{code.source}</Badge>
                  </td>
                  <td className="py-2 text-right text-caption text-neutral-500">
                    {new Date(code.last_seen_at).toLocaleDateString()}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </ProductCard>
  )
}

function PriceComparisonSection({ detail }: { detail: ProductDetail }) {
  if (detail.store_comparison.length === 0) {
    return null
  }

  const unit = detail.product.default_unit ?? 'ea'

  // Find normalized price info from price_history for each store
  const storeNormalized = new Map<string, { normalized_price: string | null; normalized_unit: string | null }>()
  for (const entry of detail.price_history) {
    if (!storeNormalized.has(entry.store_id)) {
      const priceEntry = detail.price_history.find(
        (p) => p.store_id === entry.store_id
      )
      if (priceEntry) {
        storeNormalized.set(entry.store_id, {
          normalized_price: (priceEntry as PriceHistoryEntry & { normalized_price?: string | null }).normalized_price ?? null,
          normalized_unit: (priceEntry as PriceHistoryEntry & { normalized_unit?: string | null }).normalized_unit ?? null,
        })
      }
    }
  }

  return (
    <ProductCard className="overflow-hidden">
      <div className="border-b border-neutral-200 px-5 py-4">
        <ProductCardTitle
          title="Price Comparison"
          meta={`${detail.store_comparison.length} store${detail.store_comparison.length === 1 ? '' : 's'}`}
        />
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-neutral-200">
              <th className="px-5 py-3 text-small font-semibold uppercase text-neutral-400">Store</th>
              <th className="px-5 py-3 text-right text-small font-semibold uppercase text-neutral-400">Receipt Unit Price</th>
              <th className="px-5 py-3 text-right text-small font-semibold uppercase text-neutral-400">Compared Price</th>
              <th className="px-5 py-3 text-right text-small font-semibold uppercase text-neutral-400">Last Purchased</th>
              <th className="px-5 py-3 text-right text-small font-semibold uppercase text-neutral-400"></th>
            </tr>
          </thead>
          <tbody>
            {detail.store_comparison.map((sp) => {
              const norm = storeNormalized.get(sp.store_id)
              const normalizedPrice = norm?.normalized_price ? parseFloat(norm.normalized_price) : null
              const normalizedUnit = norm?.normalized_unit ?? null
              const normalizedPriceText =
                normalizedPrice != null && Number.isFinite(normalizedPrice) && normalizedUnit
                  ? `$${normalizedPrice.toFixed(2)}/${normalizedUnit}`
                  : '\u2014'
              return (
                <tr
                  key={sp.store_id}
                  className={`border-b border-neutral-200 last:border-0 ${
                    sp.is_cheapest ? 'bg-success-subtle/30' : ''
                  }`}
                >
                  <td className="px-5 py-3 text-body-medium text-neutral-900">{sp.store_name}</td>
                  <td className="px-5 py-3 text-right font-medium text-neutral-600">
                    {formatPrice(sp.latest_price, unit)}
                  </td>
                  <td className={`px-5 py-3 text-right font-mono font-semibold ${sp.is_cheapest ? 'text-success-dark' : 'text-neutral-600'}`}>
                    {normalizedPriceText}
                  </td>
                  <td className="px-5 py-3 text-right text-caption text-neutral-400">
                    {sp.latest_date}
                  </td>
                  <td className="px-5 py-3 text-right">
                    {sp.is_cheapest && <Badge variant="success">Best</Badge>}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </ProductCard>
  )
}

function TransactionsSection({ detail }: { detail: ProductDetail }) {
  const unit = detail.product.default_unit ?? 'ea'

  if (detail.price_history.length === 0) {
    return null
  }

  return (
    <ProductCard className="overflow-hidden">
      <div className="border-b border-neutral-200 px-5 py-4">
        <ProductCardTitle
          title="All Transactions"
          meta={`${detail.price_history.length} record${detail.price_history.length === 1 ? '' : 's'}`}
        />
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-neutral-200">
              <th className="px-5 py-3 text-small font-semibold uppercase text-neutral-400">Date</th>
              <th className="px-5 py-3 text-small font-semibold uppercase text-neutral-400">Store</th>
              <th className="px-5 py-3 text-right text-small font-semibold uppercase text-neutral-400">Purchased</th>
              <th className="px-5 py-3 text-right text-small font-semibold uppercase text-neutral-400">Receipt Unit Price</th>
              <th className="px-5 py-3 text-right text-small font-semibold uppercase text-neutral-400">Total</th>
            </tr>
          </thead>
          <tbody>
            {detail.price_history.map((entry, i) => (
              <tr key={i} className="border-b border-neutral-200 last:border-0">
                <td className="px-5 py-3 text-caption text-neutral-600">{entry.date}</td>
                <td className="px-5 py-3 text-caption text-neutral-900">{entry.store_name}</td>
                <td className="px-5 py-3 text-right text-caption text-neutral-600">
                  {parseFloat(entry.quantity)} {entry.unit || unit}
                </td>
                <td className="px-5 py-3 text-right text-caption font-medium text-neutral-900">
                  {formatPrice(entry.unit_price, entry.unit || unit)}
                  {entry.is_sale && (
                    <span className="ml-1 text-xs text-green-600 font-medium">Sale</span>
                  )}
                </td>
                <td className="px-5 py-3 text-right text-caption text-neutral-600">
                  {formatPrice(entry.total_price)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </ProductCard>
  )
}

function sourceLabel(source: string): string {
  switch (source) {
    case 'mealie_food':
      return 'Mealie food'
    case 'mealie_recipe':
      return 'Mealie recipe'
    case 'kroger':
      return 'Kroger'
    case 'openfoodfacts':
      return 'Open Food Facts'
    case 'usda_fdc':
      return 'USDA FoodData Central'
    case 'user_upc':
      return 'UPC'
    case 'receipt_explicit':
      return 'Receipt'
    case 'receipt_llm':
      return 'Receipt'
    case 'receipt':
      return 'Receipt'
    default:
      return source
  }
}

function SourcesSection({ detail, productId }: { detail: ProductDetail; productId: string }) {
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [url, setURL] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [deleteConfirm, setDeleteConfirm] = useState<ProductDetail['links'][number] | null>(null)
  const refreshUPC = detail.product.upc?.trim()
  const refreshURL = detail.links[0]?.url?.trim()
  const canRefreshSources = Boolean(refreshUPC || refreshURL)

  const mutation = useMutation({
    mutationFn: () => addProductLink(productId, { url: url.trim() }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      setURL('')
      setError(null)
      setOpen(false)
    },
    onError: (err: Error) => {
      setError(err.message)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (linkId: string) => deleteProductLink(productId, linkId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      queryClient.invalidateQueries({ queryKey: ['product-enrichment-jobs', productId] })
      setDeleteConfirm(null)
    },
  })

  const refreshMutation = useMutation({
    mutationFn: () =>
      createProductEnrichmentJob(productId, {
        trigger: 'manual_refresh',
        ...(refreshUPC ? { upc: refreshUPC } : { url: refreshURL ?? '' }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-enrichment-jobs', productId] })
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
    },
  })

  const handleSubmit = () => {
    if (!url.trim()) return
    setError(null)
    mutation.mutate()
  }

  return (
    <>
      <ProductCard className="overflow-hidden">
        <div className="flex flex-col gap-3 border-b border-neutral-200 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
          <ProductCardTitle title="Sources" meta={`${detail.links.length} connected`} />
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              size="sm"
              variant="subtle"
              className="gap-1.5"
              onClick={() => refreshMutation.mutate()}
              disabled={!canRefreshSources || refreshMutation.isPending}
            >
              <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
              {refreshMutation.isPending ? 'Queueing...' : 'Refresh sources'}
            </Button>
            <Button size="sm" variant="subtle" className="gap-1.5" onClick={() => setOpen(true)}>
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              Add URL
            </Button>
          </div>
        </div>
        <div className={detail.links.length === 0 ? 'px-5 py-5' : ''}>
          {detail.links.length === 0 ? (
            <p className="text-caption text-neutral-400">No source links yet.</p>
          ) : (
            <div className="divide-y divide-neutral-200">
            {detail.links.map((link) => (
              <div key={link.id} className="px-5 py-3">
                <div className="flex flex-wrap items-center gap-2">
                  <ExternalLink className="h-4 w-4 flex-shrink-0 text-brand" aria-hidden="true" />
                  <a
                    href={link.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="min-w-0 flex-1 truncate text-body text-brand hover:underline"
                  >
                    {link.label ?? link.url}
                  </a>
                  <Badge variant="neutral">{sourceLabel(link.source)}</Badge>
                  {link.http_status && (
                    <span className="text-small text-neutral-400">HTTP {link.http_status}</span>
                  )}
                  <button
                    type="button"
                    className="ml-auto inline-flex h-8 w-8 items-center justify-center rounded-lg text-neutral-400 transition-colors hover:bg-expensive-subtle hover:text-expensive focus:outline-none focus-visible:ring-2 focus-visible:ring-brand focus-visible:ring-offset-2"
                    onClick={() => setDeleteConfirm(link)}
                    aria-label={`Remove ${link.label ?? sourceLabel(link.source)} source`}
                    title="Remove source"
                  >
                    <Trash2 className="h-4 w-4" aria-hidden="true" />
                  </button>
                </div>
                {(link.fetched_at || link.last_error) && (
                  <div className="mt-1 flex flex-wrap gap-3 text-small text-neutral-400">
                    {link.fetched_at && <span>Fetched {new Date(link.fetched_at).toLocaleDateString()}</span>}
                    {link.last_error && <span className="text-expensive">{link.last_error}</span>}
                  </div>
                )}
              </div>
            ))}
            </div>
          )}
        </div>
      </ProductCard>

      <Modal
        open={open}
        onClose={() => {
          if (!mutation.isPending) {
            setOpen(false)
            setError(null)
          }
        }}
        title="Add Product URL"
        footer={(
          <>
            <Button variant="secondary" size="sm" onClick={() => setOpen(false)} disabled={mutation.isPending}>
              Cancel
            </Button>
            <Button size="sm" onClick={handleSubmit} disabled={mutation.isPending || !url.trim()}>
              {mutation.isPending ? 'Fetching...' : 'Add URL'}
            </Button>
          </>
        )}
      >
        <div className="space-y-3">
          <input
            type="url"
            value={url}
            onChange={(e) => setURL(e.target.value)}
            placeholder="https://www.kroger.com/p/..."
            className="w-full rounded-xl border border-neutral-200 px-3 py-2 text-body focus:border-transparent focus:outline-none focus:ring-2 focus:ring-brand"
            autoFocus
          />
          {error && <p className="text-small text-expensive">{error}</p>}
        </div>
      </Modal>

      <Modal
        open={!!deleteConfirm}
        onClose={() => {
          if (!deleteMutation.isPending) {
            setDeleteConfirm(null)
          }
        }}
        title="Remove Source"
        footer={(
          <>
            <Button variant="secondary" size="sm" onClick={() => setDeleteConfirm(null)} disabled={deleteMutation.isPending}>
              Cancel
            </Button>
            <Button
              size="sm"
              className="bg-expensive text-white hover:opacity-90"
              onClick={() => deleteConfirm && deleteMutation.mutate(deleteConfirm.id)}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Removing...' : 'Remove'}
            </Button>
          </>
        )}
      >
        <p className="text-body text-neutral-600">
          Remove {deleteConfirm?.label ?? sourceLabel(deleteConfirm?.source ?? 'source')} and its source suggestions?
        </p>
      </Modal>
    </>
  )
}

function fieldLabel(field: string): string {
  const labels: Record<string, string> = {
    name: 'Name',
    brand: 'Brand',
    category: 'Category',
    default_unit: 'Default unit',
    upc: 'UPC',
    pack_quantity: 'Package contents quantity',
    pack_unit: 'Package contents unit',
    serving_quantity: 'Serving quantity',
    serving_unit: 'Serving unit',
    serving_label: 'Serving size',
    servings_per_container: 'Servings',
    calories: 'Calories',
    total_fat_g: 'Total fat',
    saturated_fat_g: 'Saturated fat',
    trans_fat_g: 'Trans fat',
    cholesterol_mg: 'Cholesterol',
    sodium_mg: 'Sodium',
    total_carbohydrate_g: 'Carbs',
    dietary_fiber_g: 'Fiber',
    total_sugars_g: 'Sugars',
    added_sugars_g: 'Added sugars',
    protein_g: 'Protein',
    ingredients: 'Ingredients',
    allergens: 'Allergens',
    image_front_url: 'Front photo',
    image_nutrition_url: 'Nutrition photo',
    image_ingredients_url: 'Ingredients photo',
    image_packaging_url: 'Package photo',
  }
  return labels[field] ?? field
}

type NutrientField = keyof ProductMetadataNutrients

const NUTRIENT_FIELDS: Array<{ field: NutrientField; label: string; unit: string }> = [
  { field: 'calories', label: 'Calories', unit: '' },
  { field: 'total_fat_g', label: 'Total fat', unit: 'g' },
  { field: 'saturated_fat_g', label: 'Saturated fat', unit: 'g' },
  { field: 'trans_fat_g', label: 'Trans fat', unit: 'g' },
  { field: 'cholesterol_mg', label: 'Cholesterol', unit: 'mg' },
  { field: 'sodium_mg', label: 'Sodium', unit: 'mg' },
  { field: 'total_carbohydrate_g', label: 'Carbs', unit: 'g' },
  { field: 'dietary_fiber_g', label: 'Fiber', unit: 'g' },
  { field: 'total_sugars_g', label: 'Sugars', unit: 'g' },
  { field: 'added_sugars_g', label: 'Added sugars', unit: 'g' },
  { field: 'protein_g', label: 'Protein', unit: 'g' },
]

const nutritionFieldFromRow = (row: ProductNutrition | null, field: NutrientField): number | null | undefined => {
  if (!row) return null
  return row[field]
}

function numberValue(value: number | string | null | undefined): number | null {
  if (value == null) return null
  const parsed = typeof value === 'number' ? value : parseFloat(value)
  return Number.isFinite(parsed) ? parsed : null
}

function compactNumber(value: number): string {
  const maximumFractionDigits = value > 20 ? 0 : value >= 1 ? 1 : 3
  return new Intl.NumberFormat('en-US', { maximumFractionDigits }).format(value)
}

function nutritionAmount(value: number | null | undefined, unit: string): string {
  if (value == null) return '\u2014'
  const formatted = compactNumber(value)
  return unit ? `${formatted}${unit}` : formatted
}

function firstText(...values: Array<string | null | undefined>): string | null {
  for (const value of values) {
    const trimmed = value?.trim()
    if (trimmed) return trimmed
  }
  return null
}

function metadataHasNutrition(metadata: ProductExternalMetadata): boolean {
  const payload = metadata.payload
  const nutrientValues = payload.nutrients ? Object.values(payload.nutrients) : []
  return Boolean(
    nutrientValues.some((value) => value != null) ||
      payload.serving ||
      payload.ingredients ||
      (payload.allergens && payload.allergens.length > 0) ||
      (payload.image_urls && Object.keys(payload.image_urls).length > 0),
  )
}

function chooseNutritionMetadata(detail: ProductDetail, row: ProductNutrition | null): ProductExternalMetadata | null {
  const snapshots = detail.external_metadata ?? []
  if (row?.product_link_id) {
    const linked = snapshots.find((metadata) => metadata.product_link_id === row.product_link_id)
    if (linked) return linked
  }
  return snapshots.find(metadataHasNutrition) ?? snapshots[0] ?? null
}

function sourceSuggestion(detail: ProductDetail, field: string, metadata: ProductExternalMetadata | null): ProductEnrichmentSuggestion | null {
  const suggestions = detail.enrichment_suggestions ?? []
  if (metadata) {
    const linked = suggestions.find((suggestion) =>
      suggestion.field === field &&
      (suggestion.external_metadata_id === metadata.id ||
        suggestion.product_link_id === metadata.product_link_id ||
        suggestion.source === metadata.source),
    )
    if (linked) return linked
  }
  return suggestions.find((suggestion) => suggestion.field === field) ?? null
}

function nutrientValue(detail: ProductDetail, row: ProductNutrition | null, metadata: ProductExternalMetadata | null, field: NutrientField): number | null {
  return (
    numberValue(nutritionFieldFromRow(row, field)) ??
    numberValue(metadata?.payload.nutrients?.[field]) ??
    numberValue(sourceSuggestion(detail, field, metadata)?.value)
  )
}

function suggestionText(detail: ProductDetail, field: string, metadata: ProductExternalMetadata | null): string | null {
  return firstText(sourceSuggestion(detail, field, metadata)?.value)
}

function servingText(detail: ProductDetail, row: ProductNutrition | null, metadata: ProductExternalMetadata | null): string | null {
  const quantity = row?.serving_quantity != null
    ? nutritionAmount(row.serving_quantity, row.serving_unit ? ` ${row.serving_unit}` : '')
    : null
  return firstText(row?.serving_label, quantity, metadata?.payload.serving?.label, suggestionText(detail, 'serving_label', metadata))
}

function servingsPerContainer(detail: ProductDetail, row: ProductNutrition | null, metadata: ProductExternalMetadata | null): number | null {
  return (
    numberValue(row?.servings_per_container) ??
    numberValue(metadata?.payload.serving?.servings_per_container) ??
    numberValue(sourceSuggestion(detail, 'servings_per_container', metadata)?.value)
  )
}

function packageText(metadata: ProductExternalMetadata | null): string | null {
  const pkg = metadata?.payload.package
  if (!pkg) return null
  return firstText(pkg.label, pkg.quantity != null ? `${compactNumber(pkg.quantity)} ${pkg.unit ?? ''}`.trim() : null)
}

function allergenList(detail: ProductDetail, row: ProductNutrition | null, metadata: ProductExternalMetadata | null): string[] {
  if (row?.allergens_json) {
    try {
      const parsed = JSON.parse(row.allergens_json)
      if (Array.isArray(parsed)) {
        return parsed.map(String).map((item) => item.trim()).filter(Boolean)
      }
    } catch {
      // Fall through to comma parsing for older rows.
    }
    const fallback = row.allergens_json.split(/[,;]/).map((item) => item.trim()).filter(Boolean)
    if (fallback.length > 0) return fallback
  }
  if (metadata?.payload.allergens?.length) return metadata.payload.allergens
  const suggested = suggestionText(detail, 'allergens', metadata)
  return suggested ? suggested.split(/[,;]/).map((item) => item.trim()).filter(Boolean) : []
}

function providerLevelBadges(metadata: ProductExternalMetadata | null): string[] {
  const meta = metadata?.payload.provider_meta ?? {}
  return Object.entries(meta)
    .filter(([key, value]) => key.startsWith('nutrient_level_') && value)
    .map(([key, value]) => {
      const nutrient = key.replace('nutrient_level_', '').replace(/[-_]/g, ' ')
      return `${nutrient}: ${value}`
    })
}

type NutritionSuggestionGroup = {
  key: string
  source: string
  sourceURL: string
  suggestions: ProductEnrichmentSuggestion[]
}

function nutritionSuggestionGroups(suggestions: ProductEnrichmentSuggestion[]): NutritionSuggestionGroup[] {
  const groups = new Map<string, NutritionSuggestionGroup>()
  for (const suggestion of suggestions) {
    const key = suggestion.external_metadata_id ?? suggestion.product_link_id ?? `${suggestion.source}:${suggestion.source_url}`
    const existing = groups.get(key)
    if (existing) {
      existing.suggestions.push(suggestion)
      continue
    }
    groups.set(key, {
      key,
      source: suggestion.source,
      sourceURL: suggestion.source_url,
      suggestions: [suggestion],
    })
  }
  return Array.from(groups.values())
}

function NutritionSourceSuggestion({
  productId,
  group,
}: {
  productId: string
  group: NutritionSuggestionGroup
}) {
  const queryClient = useQueryClient()
  const suggestionIds = group.suggestions.map((suggestion) => suggestion.id)
  const fields = group.suggestions.map((suggestion) => fieldLabel(suggestion.field))
  const fieldSummary = fields.length === 1 ? fields[0] : `${fields.length} fields`
  const acceptMutation = useMutation({
    mutationFn: () => bulkAcceptProductEnrichmentSuggestions(productId, { suggestion_ids: suggestionIds }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      queryClient.invalidateQueries({ queryKey: ['products'] })
    },
  })
  const rejectMutation = useMutation({
    mutationFn: () => bulkRejectProductEnrichmentSuggestions(productId, { suggestion_ids: suggestionIds }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
    },
  })
  const mutationPending = acceptMutation.isPending || rejectMutation.isPending

  return (
    <div className="flex flex-col gap-3 rounded-xl border border-brand/20 bg-brand-subtle/50 px-3 py-3 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2 text-caption">
          <span className="inline-flex items-center gap-1 rounded-lg bg-white px-2 py-0.5 text-small font-semibold text-brand">
            <Zap className="h-3 w-3" aria-hidden="true" />
            {sourceLabel(group.source)}
          </span>
          <span className="font-semibold text-neutral-900">suggested nutrition facts</span>
          <span className="text-neutral-400">{fieldSummary}</span>
        </div>
      </div>
      <div className="flex shrink-0 flex-wrap gap-2">
        <Button
          size="sm"
          className="gap-1.5"
          onClick={() => acceptMutation.mutate()}
          disabled={mutationPending}
        >
          <Check className="h-3.5 w-3.5" aria-hidden="true" />
          {acceptMutation.isPending ? 'Accepting...' : 'Accept source'}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          className="gap-1.5"
          onClick={() => rejectMutation.mutate()}
          disabled={mutationPending}
        >
          <X className="h-3.5 w-3.5" aria-hidden="true" />
          {rejectMutation.isPending ? 'Dismissing...' : 'Dismiss'}
        </Button>
      </div>
    </div>
  )
}

function NutritionSection({ detail, productId }: { detail: ProductDetail; productId: string }) {
  const row = detail.nutrition?.[0] ?? null
  const candidateMetadata = chooseNutritionMetadata(detail, row)
  const nutritionSuggestionFields = [
    ...NUTRIENT_FIELDS.map((item) => item.field),
    'serving_quantity',
    'serving_unit',
    'serving_label',
    'servings_per_container',
    'ingredients',
    'allergens',
  ]
  const nutritionSuggestions = fieldSuggestions(detail, nutritionSuggestionFields)
  const sourceSuggestionGroups = nutritionSuggestionGroups(nutritionSuggestions)
  const metadata = row || nutritionSuggestions.length > 0 ? candidateMetadata : null
  const linkedSource = row?.product_link_id ? detail.links.find((link) => link.id === row.product_link_id) : null
  const firstNutritionSuggestion = detail.enrichment_suggestions.find((suggestion) =>
    NUTRIENT_FIELDS.some((field) => field.field === suggestion.field) || ['ingredients', 'allergens', 'serving_label'].includes(suggestion.field),
  )
  const sourceName = metadata?.source ?? linkedSource?.source ?? sourceSuggestion(detail, 'calories', metadata)?.source ?? firstNutritionSuggestion?.source
  const sourceURL = metadata?.source_url ?? metadata?.payload.source_url ?? linkedSource?.url
  const fetchedAt = metadata?.fetched_at ?? linkedSource?.fetched_at
  const nutriScore = metadata?.payload.provider_meta?.nutriscore_grade
  const levels = providerLevelBadges(metadata)
  const serving = servingText(detail, row, metadata)
  const servings = servingsPerContainer(detail, row, metadata)
  const pkg = packageText(metadata)
  const ingredients = firstText(row?.ingredients, metadata?.payload.ingredients, suggestionText(detail, 'ingredients', metadata))
  const allergens = allergenList(detail, row, metadata)
  const nutrients = NUTRIENT_FIELDS.map((item) => ({
    ...item,
    value: nutrientValue(detail, row, metadata, item.field),
  })).filter((item) => item.value != null)
  const calories = nutrients.find((item) => item.field === 'calories')?.value ?? null
  const hasData = Boolean(
    row ||
      (metadata && metadataHasNutrition(metadata)) ||
      nutrients.length > 0 ||
      serving ||
      ingredients ||
      allergens.length > 0 ||
      nutritionSuggestions.length > 0,
  )

  if (!hasData) {
    return (
      <ProductCard className="p-5">
        <ProductCardTitle title="Nutrition" />
        <p className="text-caption text-neutral-400">No nutrition found yet.</p>
      </ProductCard>
    )
  }

  return (
    <ProductCard className="p-5">
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <ProductCardTitle title="Nutrition" />
          <div className="mt-2 flex flex-wrap items-center gap-2">
            {row?.accepted_by_user && <Badge variant="success">Accepted</Badge>}
            {sourceName && <Badge variant="neutral">{sourceLabel(sourceName)}</Badge>}
            {fetchedAt && (
              <span className="text-small text-neutral-400">Fetched {new Date(fetchedAt).toLocaleDateString()}</span>
            )}
          </div>
        </div>
        {sourceURL && (
          <a
            href={sourceURL}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex h-9 w-9 items-center justify-center rounded-lg border border-neutral-200 text-brand transition-colors hover:bg-brand-subtle focus:outline-none focus-visible:ring-2 focus-visible:ring-brand focus-visible:ring-offset-2"
            aria-label="Open nutrition source"
            title="Open source"
          >
            <ExternalLink className="h-4 w-4" aria-hidden="true" />
          </a>
        )}
      </div>

      {sourceSuggestionGroups.length > 0 && (
        <div className="mb-4 space-y-2">
          {sourceSuggestionGroups.map((group) => (
            <NutritionSourceSuggestion key={group.key} productId={productId} group={group} />
          ))}
        </div>
      )}

      <div className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-3">
            {calories != null && (
              <div className="rounded-xl bg-neutral-50 px-3 py-3">
                <span className="flex items-center gap-1.5 text-small text-neutral-400">
                  <Flame className="h-3.5 w-3.5" aria-hidden="true" />
                  Calories
                </span>
                <span className="mt-1 block text-feature font-semibold text-neutral-900">{compactNumber(calories)}</span>
              </div>
            )}
            {serving && (
              <div className="rounded-xl bg-neutral-50 px-3 py-3">
                <span className="flex items-center gap-1.5 text-small text-neutral-400">
                  <PackageIcon className="h-3.5 w-3.5" aria-hidden="true" />
                  Serving
                </span>
                <span className="mt-1 block text-caption font-semibold text-neutral-900">{serving}</span>
                {servings != null && <span className="mt-0.5 block text-small text-neutral-400">{compactNumber(servings)} servings/container</span>}
              </div>
            )}
            {(pkg || nutriScore) && (
              <div className="rounded-xl bg-neutral-50 px-3 py-3">
                <span className="block text-small text-neutral-400">{nutriScore ? 'Nutri-Score' : 'Package'}</span>
                <span className="mt-1 block text-caption font-semibold text-neutral-900">{nutriScore ? nutriScore.toUpperCase() : pkg}</span>
                {pkg && nutriScore && <span className="mt-0.5 block text-small text-neutral-400">{pkg}</span>}
              </div>
            )}
          </div>

          {nutrients.length > 0 && (
            <div>
              <h3 className="mb-2 text-caption font-semibold text-neutral-700">Nutrition facts</h3>
              <div className="grid gap-x-6 gap-y-0 sm:grid-cols-2">
                {nutrients.map((item) => (
                  <div key={item.field} className="flex items-center justify-between border-b border-neutral-200 py-2 text-caption">
                    <span className="text-neutral-500">{item.label}</span>
                    <span className="font-semibold text-neutral-900">{nutritionAmount(item.value, item.unit)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {levels.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {levels.map((level) => (
                <Badge key={level} variant="success">{level}</Badge>
              ))}
            </div>
          )}

          {ingredients && (
            <div className="space-y-1">
              <h3 className="flex items-center gap-1.5 text-caption font-semibold text-neutral-700">
                <Wheat className="h-4 w-4 text-neutral-400" aria-hidden="true" />
                Ingredients
              </h3>
              <p className="text-caption text-neutral-500">{ingredients}</p>
            </div>
          )}

          {allergens.length > 0 && (
            <div className="space-y-2">
              <h3 className="flex items-center gap-1.5 text-caption font-semibold text-neutral-700">
                <ShieldAlert className="h-4 w-4 text-neutral-400" aria-hidden="true" />
                Allergens
              </h3>
              <div className="flex flex-wrap gap-2">
                {allergens.map((allergen) => (
                  <Badge key={allergen} variant="neutral">{allergen}</Badge>
                ))}
              </div>
            </div>
          )}
      </div>
    </ProductCard>
  )
}

// --- Product Group Section ---

function ProductGroupSection({ detail, productId }: { detail: ProductDetail; productId: string }) {
  const queryClient = useQueryClient()
  const [showLinkModal, setShowLinkModal] = useState(false)
  const [newGroupName, setNewGroupName] = useState('')

  const product = detail.product
  const hasGroup = !!product.product_group_id
  const hasBrand = !!product.brand

  const { data: groups } = useQuery({
    queryKey: ['product-groups'],
    queryFn: () => fetchGroups(),
    enabled: !hasGroup,
  })

  const { data: suggestions } = useQuery({
    queryKey: ['group-suggestions', productId],
    queryFn: () => fetchGroupSuggestions(productId),
    enabled: !hasGroup && hasBrand,
  })

  const linkToGroupMutation = useMutation({
    mutationFn: (groupId: string) =>
      updateProduct(productId, { product_group_id: groupId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      queryClient.invalidateQueries({ queryKey: ['product-groups'] })
      setShowLinkModal(false)
    },
  })

  const unlinkMutation = useMutation({
    mutationFn: () =>
      updateProduct(productId, { product_group_id: null }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      queryClient.invalidateQueries({ queryKey: ['product-groups'] })
    },
  })

  const createAndLinkMutation = useMutation({
    mutationFn: async (name: string) => {
      const group = await createGroup({ name })
      await updateProduct(productId, { product_group_id: group.id })
      return group
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-detail', productId] })
      queryClient.invalidateQueries({ queryKey: ['product-groups'] })
      setShowLinkModal(false)
      setNewGroupName('')
    },
  })

  // If product is in a group, show group info
  if (hasGroup) {
    return (
      <ProductCard className="p-5">
        <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <ProductCardTitle title="Product Group" />
          <Button
            size="sm"
            variant="subtle"
            className="text-expensive"
            onClick={() => unlinkMutation.mutate()}
            disabled={unlinkMutation.isPending}
          >
            {unlinkMutation.isPending ? 'Removing...' : 'Remove from Group'}
          </Button>
        </div>
        <Link
          to={`/product-groups/${product.product_group_id}`}
          className="text-body text-brand hover:underline font-medium"
        >
          View group page
        </Link>
      </ProductCard>
    )
  }

  // Product is not in a group
  const suggestionList = suggestions ?? []
  const groupList = groups ?? []

  return (
    <>
      <ProductCard className="p-5">
        <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <ProductCardTitle title="Product Group" />
          <Button size="sm" variant="subtle" className="gap-1.5" onClick={() => setShowLinkModal(true)}>
            <Link2 className="h-3.5 w-3.5" aria-hidden="true" />
            Link to Group
          </Button>
        </div>

        {suggestionList.length > 0 ? (
          <div>
            <p className="text-caption text-neutral-400 mb-2">Suggested groups based on brand:</p>
            <div className="space-y-2">
              {suggestionList.map((s: GroupSuggestion) => (
                <div key={s.group_id} className="flex items-center justify-between p-2 bg-neutral-50 rounded-xl">
                  <div>
                    <span className="text-body text-neutral-900">{s.group_name}</span>
                    <span className="ml-2 text-caption text-neutral-400">
                      {s.member_count} member{s.member_count !== 1 ? 's' : ''} &middot; {s.reason}
                    </span>
                  </div>
                  <Button
                    size="sm"
                    onClick={() => linkToGroupMutation.mutate(s.group_id)}
                    disabled={linkToGroupMutation.isPending}
                  >
                    Join
                  </Button>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <p className="text-caption text-neutral-400">
            Not in a group. Link this product to a group to compare prices across brands and stores.
          </p>
        )}
      </ProductCard>

      {/* Link to Group Modal */}
      <Modal open={showLinkModal} onClose={() => setShowLinkModal(false)} title="Link to Product Group">
        <div className="space-y-4">
          {/* Existing groups */}
          {groupList.length > 0 && (
            <div>
              <p className="text-small font-medium text-neutral-400 mb-2">Existing groups</p>
              <div className="max-h-48 overflow-y-auto space-y-1">
                {groupList.map((g: ProductGroup) => (
                  <button
                    key={g.id}
                    type="button"
                    className="w-full text-left px-3 py-2 rounded-xl hover:bg-neutral-50 transition-colors flex items-center justify-between cursor-pointer"
                    onClick={() => linkToGroupMutation.mutate(g.id)}
                    disabled={linkToGroupMutation.isPending}
                  >
                    <div>
                      <span className="text-body text-neutral-900">{g.name}</span>
                      <span className="ml-2 text-caption text-neutral-400">
                        {g.member_count} member{g.member_count !== 1 ? 's' : ''}
                      </span>
                    </div>
                    <span className="text-caption text-brand">+ Add</span>
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Create new group */}
          <div>
            <p className="text-small font-medium text-neutral-400 mb-2">Or create a new group</p>
            <div className="flex gap-2">
              <input
                type="text"
                value={newGroupName}
                onChange={(e) => setNewGroupName(e.target.value)}
                placeholder="Group name..."
                className="flex-1 px-3 py-2 text-body border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && newGroupName.trim()) {
                    createAndLinkMutation.mutate(newGroupName.trim())
                  }
                }}
              />
              <Button
                size="sm"
                onClick={() => newGroupName.trim() && createAndLinkMutation.mutate(newGroupName.trim())}
                disabled={!newGroupName.trim() || createAndLinkMutation.isPending}
              >
                {createAndLinkMutation.isPending ? 'Creating...' : 'Create & Link'}
              </Button>
            </div>
          </div>
        </div>
      </Modal>
    </>
  )
}

// --- Delete Product Modal ---

interface DeleteProductModalProps {
  open: boolean
  onClose: () => void
  productId: string
  productName: string
  usage: ProductUsage | null
  onMerge: () => void
}

function DeleteProductModal({ open, onClose, productId, productName, usage, onMerge }: DeleteProductModalProps) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [banner, setBanner] = useState<string | null>(null)

  const deleteMutation = useMutation({
    mutationFn: () => deleteProduct(productId),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ['products'] })
      void queryClient.invalidateQueries({ queryKey: ['unmatched-count'] })
      if (result.unmatched_line_items > 0) {
        setBanner(`${result.unmatched_line_items} item${result.unmatched_line_items !== 1 ? 's' : ''} moved to review queue`)
        setTimeout(() => {
          navigate('/products')
        }, 2500)
      } else {
        navigate('/products')
      }
    },
  })

  const isSimple =
    usage !== null &&
    usage.line_items === 0 &&
    usage.shopping_list_items === 0 &&
    usage.matching_rules === 0 &&
    usage.aliases === 0 &&
    usage.images === 0

  if (!open) return null

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Delete Product"
      footer={
        isSimple ? (
          <>
            <Button variant="secondary" size="sm" onClick={onClose}>
              Cancel
            </Button>
            <Button
              size="sm"
              className="bg-expensive text-white hover:opacity-90"
              onClick={() => deleteMutation.mutate()}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </>
        ) : (
          <>
            <Button variant="secondary" size="sm" onClick={onClose}>
              Cancel
            </Button>
            <Button
              size="sm"
              variant="outlined"
              onClick={() => {
                onClose()
                onMerge()
              }}
            >
              Merge into another product
            </Button>
            <Button
              size="sm"
              className="bg-expensive text-white hover:opacity-90"
              onClick={() => deleteMutation.mutate()}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending
                ? 'Deleting...'
                : `Delete + move ${usage?.line_items ?? 0} item${(usage?.line_items ?? 0) !== 1 ? 's' : ''} to review queue`}
            </Button>
          </>
        )
      }
    >
      {banner && (
        <div className="mb-4 px-4 py-3 bg-success-subtle rounded-xl text-body text-success-dark flex items-center justify-between gap-3">
          <span>{banner}</span>
          <Link to="/review" className="text-brand underline text-caption">
            Go to review
          </Link>
        </div>
      )}
      {isSimple ? (
        <p className="text-body text-neutral-600">
          Are you sure you want to delete <strong>{productName}</strong>? This cannot be undone.
        </p>
      ) : (
        <p className="text-body text-neutral-600">
          <strong>{productName}</strong> appears on{' '}
          {usage?.line_items ?? 0} receipt{(usage?.line_items ?? 0) !== 1 ? 's' : ''} and has{' '}
          {usage?.aliases ?? 0} alias{(usage?.aliases ?? 0) !== 1 ? 'es' : ''}.
          Deleting it will move matched line items to the review queue.
        </p>
      )}
    </Modal>
  )
}

// --- Main Page ---

function ProductDetailPage() {
  const { id } = useParams<{ id: string }>()
  const productId = id ?? ''
  const [mergeOpen, setMergeOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [usage, setUsage] = useState<ProductUsage | null>(null)

  const { data: detail, isLoading, error } = useQuery({
    queryKey: ['product-detail', productId],
    queryFn: () => getProductDetail(productId),
    enabled: !!productId,
  })

  const { data: stores = [] } = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  if (isLoading) {
    return (
      <div className="py-8">
        <p className="text-body text-neutral-400">Loading product details...</p>
      </div>
    )
  }

  if (error || !detail) {
    return (
      <div className="py-8">
        <p className="text-body text-expensive">Failed to load product details.</p>
        <Link to="/products" className="text-body text-brand hover:underline mt-2 inline-block">
          Back to Products
        </Link>
      </div>
    )
  }

  const { product } = detail
  const headerSuggestions = fieldSuggestions(detail, ['name', 'category', 'default_unit'])

  return (
    <div className="mx-auto w-full max-w-6xl py-8">
      {/* Breadcrumb */}
      <div className="mb-4">
        <Link to="/products" className="text-caption text-brand hover:underline">
          Products
        </Link>
        <span className="text-caption text-neutral-400 mx-2">/</span>
        <span className="text-caption text-neutral-600">{product.name}</span>
      </div>

      {/* Header */}
      <div className="mb-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <h1 className="font-display text-subhead font-bold text-neutral-900">
            {product.name}
          </h1>
          <div className="flex flex-wrap gap-2 sm:justify-end">
            <Button size="sm" variant="outlined" onClick={() => setMergeOpen(true)}>
              Merge with Another Product
            </Button>
            <Button
              size="sm"
              className="bg-expensive text-white hover:opacity-90"
              onClick={async () => {
                const u = await getProductUsage(productId)
                setUsage(u)
                setDeleteOpen(true)
              }}
            >
              Delete
            </Button>
          </div>
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-3">
          {product.brand && <Badge variant="neutral">{product.brand}</Badge>}
          {product.category && <Badge variant="neutral">{product.category}</Badge>}
          {product.default_unit && (
            <span className="text-caption text-neutral-400">
              Default unit: {product.default_unit}
            </span>
          )}
        </div>
        <InlineSuggestionList productId={productId} suggestions={headerSuggestions} showField className="mt-3" />
      </div>

      {/* Content sections */}
      <div className="space-y-5">
        <ProductInfoSection detail={detail} productId={productId} />
        <ProductGroupSection detail={detail} productId={productId} />
        <SourcesSection detail={detail} productId={productId} />
        <NutritionSection detail={detail} productId={productId} />
        <PriceTrendSection detail={detail} />
        <PhotosSection detail={detail} productId={productId} />
        <StoreCodesSection detail={detail} />
        <AliasesSection detail={detail} productId={productId} stores={stores} />
        <PriceComparisonSection detail={detail} />
        <TransactionsSection detail={detail} />
      </div>

      {/* Merge Modal */}
      <ProductMerge
        open={mergeOpen}
        onClose={() => setMergeOpen(false)}
        keepProduct={product}
      />

      {/* Delete Modal */}
      <DeleteProductModal
        open={deleteOpen}
        onClose={() => setDeleteOpen(false)}
        productId={productId}
        productName={product.name}
        usage={usage}
        onMerge={() => setMergeOpen(true)}
      />
    </div>
  )
}

export default ProductDetailPage
