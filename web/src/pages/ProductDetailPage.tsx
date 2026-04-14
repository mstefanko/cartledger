import { useState, useRef, useCallback, useMemo } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  getProductDetail,
  uploadProductImage,
  deleteProductImage,
  createProductAlias,
  deleteProductAlias,
} from '@/api/products'
import { listStores } from '@/api/stores'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { Modal } from '@/components/ui/Modal'
import { ProductMerge } from '@/components/products/ProductMerge'
import type { ProductDetail, ProductImage, ProductAlias, Store, PriceHistoryEntry } from '@/types'

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

// --- Sub-components ---

function PriceTrendSection({ detail }: { detail: ProductDetail }) {
  const { pct, direction } = pctChange(detail.price_history)
  // Sparkline placeholder — real chart in Phase 5
  const recentHistory = detail.price_history.slice(0, 12).reverse()
  const bars = recentHistory.map((e) => parseFloat(e.unit_price))

  const max = Math.max(...bars, 1)
  const min = Math.min(...bars, 0)
  const range = max - min || 1

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-5">
      <div className="flex items-center justify-between mb-3">
        <h2 className="font-display text-feature font-semibold text-neutral-900">Price Trend</h2>
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
      {detail.stats.count > 0 && (
        <div className="flex gap-4 mt-3 text-small text-neutral-500">
          <span>Avg: {formatPrice(detail.stats.avg)}</span>
          <span>Min: {formatPrice(detail.stats.min)}</span>
          <span>Max: {formatPrice(detail.stats.max)}</span>
          <span>{detail.stats.count} purchases</span>
        </div>
      )}
      {detail.stats.total_saved && parseFloat(detail.stats.total_saved) > 0 && (
        <div className="mt-2 text-sm text-green-600">
          Total saved: ${Number(detail.stats.total_saved).toFixed(2)}
        </div>
      )}
    </div>
  )
}

function PhotosSection({ detail, productId }: { detail: ProductDetail; productId: string }) {
  const queryClient = useQueryClient()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState(false)
  const [lightboxImage, setLightboxImage] = useState<ProductImage | null>(null)
  const [deleteConfirm, setDeleteConfirm] = useState<ProductImage | null>(null)

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
      <div className="bg-white rounded-2xl shadow-subtle p-5">
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-display text-feature font-semibold text-neutral-900">Photos</h2>
          <Button
            size="sm"
            variant="subtle"
            onClick={() => fileInputRef.current?.click()}
            disabled={uploading}
          >
            {uploading ? 'Uploading...' : '+ Add Photo'}
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            accept="image/jpeg,image/png"
            className="hidden"
            onChange={handleFileSelect}
          />
        </div>
        {detail.images.length === 0 ? (
          <p className="text-caption text-neutral-400">No photos yet. Add one to help identify this product.</p>
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
                    src={`${window.location.origin}/${img.image_path}`}
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
      </div>

      {/* Lightbox Modal */}
      <Modal open={!!lightboxImage} onClose={() => setLightboxImage(null)}>
        {lightboxImage && (
          <img
            src={`${window.location.origin}/${lightboxImage.image_path}`}
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

function AliasChip({ alias, storeName, onDelete }: { alias: ProductAlias; storeName: string | null; onDelete: () => void }) {
  return (
    <span className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-neutral-50 rounded-xl text-caption text-neutral-600">
      &quot;{alias.alias}&quot;
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
      <div className="bg-white rounded-2xl shadow-subtle p-5">
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-display text-feature font-semibold text-neutral-900">Aliases</h2>
          <Button size="sm" variant="subtle" onClick={() => setShowAdd(!showAdd)}>
            {showAdd ? 'Cancel' : '+ Add Alias'}
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
      </div>

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

function formatNormalizedPrice(rawPrice: string | null, rawUnit: string, normalizedPrice: string | null | undefined, normalizedUnit: string | null | undefined): string {
  const raw = formatPrice(rawPrice, rawUnit)
  if (!normalizedPrice || !normalizedUnit) return raw
  // Don't show normalized if it's the same unit
  if (rawUnit === normalizedUnit) return raw
  const normNum = parseFloat(normalizedPrice)
  if (isNaN(normNum)) return raw
  return `${raw} ($${normNum.toFixed(2)}/${normalizedUnit})`
}

function PriceComparisonSection({ detail }: { detail: ProductDetail }) {
  if (detail.store_prices.length === 0) {
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
    <div className="bg-white rounded-2xl shadow-subtle p-5">
      <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">Price Comparison</h2>
      <div className="overflow-x-auto">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-neutral-200">
              <th className="pb-2 text-small font-medium text-neutral-400">Store</th>
              <th className="pb-2 text-small font-medium text-neutral-400 text-right">Unit Price</th>
              <th className="pb-2 text-small font-medium text-neutral-400 text-right">Last Purchased</th>
              <th className="pb-2 text-small font-medium text-neutral-400 text-right"></th>
            </tr>
          </thead>
          <tbody>
            {detail.store_prices.map((sp) => {
              const norm = storeNormalized.get(sp.store_id)
              return (
                <tr
                  key={sp.store_id}
                  className={`border-b border-neutral-200 last:border-0 ${
                    sp.is_cheapest ? 'bg-success-subtle/30' : ''
                  }`}
                >
                  <td className="py-2.5 text-body-medium text-neutral-900">{sp.store_name}</td>
                  <td className={`py-2.5 text-right font-medium ${sp.is_cheapest ? 'text-success-dark' : 'text-neutral-600'}`}>
                    {formatNormalizedPrice(sp.latest_price, unit, norm?.normalized_price, norm?.normalized_unit)}
                  </td>
                  <td className="py-2.5 text-right text-caption text-neutral-400">
                    {sp.latest_date}
                  </td>
                  <td className="py-2.5 text-right">
                    {sp.is_cheapest && <Badge variant="success">Best</Badge>}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function TransactionsSection({ detail }: { detail: ProductDetail }) {
  const unit = detail.product.default_unit ?? 'ea'

  if (detail.price_history.length === 0) {
    return null
  }

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-5">
      <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">All Transactions</h2>
      <div className="overflow-x-auto">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-neutral-200">
              <th className="pb-2 text-small font-medium text-neutral-400">Date</th>
              <th className="pb-2 text-small font-medium text-neutral-400">Store</th>
              <th className="pb-2 text-small font-medium text-neutral-400 text-right">Qty</th>
              <th className="pb-2 text-small font-medium text-neutral-400 text-right">Unit Price</th>
              <th className="pb-2 text-small font-medium text-neutral-400 text-right">Total</th>
            </tr>
          </thead>
          <tbody>
            {detail.price_history.map((entry, i) => (
              <tr key={i} className="border-b border-neutral-200 last:border-0">
                <td className="py-2.5 text-caption text-neutral-600">{entry.date}</td>
                <td className="py-2.5 text-caption text-neutral-900">{entry.store_name}</td>
                <td className="py-2.5 text-right text-caption text-neutral-600">
                  {parseFloat(entry.quantity)} {entry.unit || unit}
                </td>
                <td className="py-2.5 text-right text-caption font-medium text-neutral-900">
                  {formatPrice(entry.unit_price, entry.unit || unit)}
                  {entry.is_sale && (
                    <span className="ml-1 text-xs text-green-600 font-medium">Sale</span>
                  )}
                </td>
                <td className="py-2.5 text-right text-caption text-neutral-600">
                  {formatPrice(entry.total_price)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function MealieLinksSection({ detail }: { detail: ProductDetail }) {
  if (detail.links.length === 0) {
    return null
  }

  const sourceLabel = (source: string): string => {
    switch (source) {
      case 'mealie_food':
        return 'food'
      case 'mealie_recipe':
        return 'recipe'
      default:
        return source
    }
  }

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-5">
      <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">Linked in Mealie</h2>
      <div className="space-y-2">
        {detail.links.map((link) => (
          <div key={link.id} className="flex items-center gap-2">
            <svg className="w-4 h-4 text-brand flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
            </svg>
            <a
              href={link.url}
              target="_blank"
              rel="noopener noreferrer"
              className="text-body text-brand hover:underline"
            >
              {link.label ?? link.url}
            </a>
            <Badge variant="neutral">{sourceLabel(link.source)}</Badge>
          </div>
        ))}
      </div>
    </div>
  )
}

// --- Main Page ---

function ProductDetailPage() {
  const { id } = useParams<{ id: string }>()
  const productId = id ?? ''
  const [mergeOpen, setMergeOpen] = useState(false)

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

  return (
    <div className="py-8 max-w-4xl">
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
        <div className="flex items-start justify-between">
          <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight">
            {product.name}
          </h1>
          <Button size="sm" variant="outlined" onClick={() => setMergeOpen(true)}>
            Merge with Another Product
          </Button>
        </div>
        <div className="flex items-center gap-3 mt-2">
          {product.category && <Badge variant="neutral">{product.category}</Badge>}
          {product.default_unit && (
            <span className="text-caption text-neutral-400">
              Default unit: {product.default_unit}
            </span>
          )}
        </div>
      </div>

      {/* Content sections */}
      <div className="space-y-5">
        <PriceTrendSection detail={detail} />
        <PhotosSection detail={detail} productId={productId} />
        <AliasesSection detail={detail} productId={productId} stores={stores} />
        <PriceComparisonSection detail={detail} />
        <TransactionsSection detail={detail} />
        <MealieLinksSection detail={detail} />
      </div>

      {/* Merge Modal */}
      <ProductMerge
        open={mergeOpen}
        onClose={() => setMergeOpen(false)}
        keepProduct={product}
      />
    </div>
  )
}

export default ProductDetailPage
