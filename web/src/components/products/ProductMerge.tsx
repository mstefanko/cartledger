import { useState, useEffect, useCallback, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { Modal } from '@/components/ui/Modal'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { listProducts, mergeProducts, getProductDetail } from '@/api/products'
import type { Product } from '@/types'

interface ProductMergeProps {
  open: boolean
  onClose: () => void
  keepProduct: Product
}

function ProductMerge({ open, onClose, keepProduct }: ProductMergeProps) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [selectedProduct, setSelectedProduct] = useState<Product | null>(null)
  const [showDropdown, setShowDropdown] = useState(false)
  const dropdownRef = useRef<HTMLDivElement>(null)
  const searchTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Debounce search
  useEffect(() => {
    if (searchTimeoutRef.current) clearTimeout(searchTimeoutRef.current)
    searchTimeoutRef.current = setTimeout(() => {
      setDebouncedSearch(search)
    }, 300)
    return () => {
      if (searchTimeoutRef.current) clearTimeout(searchTimeoutRef.current)
    }
  }, [search])

  const { data: products = [] } = useQuery({
    queryKey: ['products', debouncedSearch],
    queryFn: () => listProducts(debouncedSearch ? { search: debouncedSearch } : undefined),
    enabled: open && debouncedSearch.length > 0,
  })

  // Get detail of the product to merge (for preview counts)
  const { data: mergeDetail } = useQuery({
    queryKey: ['product-detail', selectedProduct?.id],
    queryFn: () => getProductDetail(selectedProduct!.id),
    enabled: !!selectedProduct,
  })

  const { data: keepDetail } = useQuery({
    queryKey: ['product-detail', keepProduct.id],
    queryFn: () => getProductDetail(keepProduct.id),
    enabled: open,
  })

  const mergeMutation = useMutation({
    mutationFn: () => mergeProducts(keepProduct.id, selectedProduct!.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['products'] })
      queryClient.invalidateQueries({ queryKey: ['product-detail', keepProduct.id] })
      onClose()
      navigate(`/products/${keepProduct.id}`)
    },
  })

  // Close dropdown on outside click
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setShowDropdown(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  // Reset on open
  useEffect(() => {
    if (open) {
      setSearch('')
      setSelectedProduct(null)
      setShowDropdown(false)
    }
  }, [open])

  const handleSelectProduct = useCallback((product: Product) => {
    setSelectedProduct(product)
    setSearch(product.name)
    setShowDropdown(false)
  }, [])

  const filteredProducts = products
    .filter((p) => p.id !== keepProduct.id)
    .slice(0, 10)

  const aliasCount = mergeDetail?.aliases?.length ?? 0
  const transactionCount = mergeDetail?.price_history?.length ?? 0

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Merge Products"
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={() => mergeMutation.mutate()}
            disabled={!selectedProduct || mergeMutation.isPending}
            className="bg-expensive text-white hover:opacity-90"
          >
            {mergeMutation.isPending ? 'Merging...' : 'Confirm Merge'}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <p className="text-caption text-neutral-500">
          Merge another product into <strong>{keepProduct.name}</strong>. The merged product will be
          deleted and all its aliases, transactions, and list items will be transferred.
        </p>

        {/* Search */}
        <div ref={dropdownRef} className="relative">
          <label className="text-caption font-medium text-neutral-900 block mb-1.5">
            Product to merge
          </label>
          <input
            type="text"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value)
              setShowDropdown(true)
              if (!e.target.value) setSelectedProduct(null)
            }}
            onFocus={() => {
              if (search) setShowDropdown(true)
            }}
            placeholder="Search for a product..."
            className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 text-body font-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
          />
          {showDropdown && filteredProducts.length > 0 && (
            <div className="absolute z-20 top-full left-0 right-0 mt-1 bg-white border border-neutral-200 rounded-xl shadow-lg max-h-48 overflow-y-auto">
              {filteredProducts.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  className="w-full text-left px-3 py-2 text-body hover:bg-brand-subtle transition-colors cursor-pointer"
                  onClick={() => handleSelectProduct(p)}
                >
                  <span className="text-neutral-900">{p.name}</span>
                  {p.category && (
                    <span className="ml-2 text-small text-neutral-400">{p.category}</span>
                  )}
                </button>
              ))}
            </div>
          )}
        </div>

        {/* Preview */}
        {selectedProduct && (
          <div className="border border-neutral-200 rounded-xl p-4 space-y-3">
            <h3 className="text-caption font-semibold text-neutral-900">Merge Preview</h3>

            <div className="flex items-start gap-3">
              <div className="flex-1 p-3 bg-success-subtle/30 rounded-xl">
                <div className="flex items-center gap-2 mb-1">
                  <Badge variant="success">Keep</Badge>
                </div>
                <p className="text-body-medium font-medium text-neutral-900">{keepProduct.name}</p>
                {keepProduct.category && (
                  <p className="text-small text-neutral-400">{keepProduct.category}</p>
                )}
                {keepDetail && (
                  <p className="text-small text-neutral-400 mt-1">
                    {keepDetail.aliases.length} aliases, {keepDetail.price_history.length} transactions
                  </p>
                )}
              </div>

              <div className="flex items-center pt-6">
                <svg className="w-5 h-5 text-neutral-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13 7l5 5m0 0l-5 5m5-5H6" />
                </svg>
              </div>

              <div className="flex-1 p-3 bg-expensive-subtle/30 rounded-xl">
                <div className="flex items-center gap-2 mb-1">
                  <Badge variant="error">Merge (will be deleted)</Badge>
                </div>
                <p className="text-body-medium font-medium text-neutral-900">{selectedProduct.name}</p>
                {selectedProduct.category && (
                  <p className="text-small text-neutral-400">{selectedProduct.category}</p>
                )}
              </div>
            </div>

            {(aliasCount > 0 || transactionCount > 0) && (
              <div className="bg-neutral-50 rounded-xl p-3">
                <p className="text-caption font-medium text-neutral-700 mb-1">Will be transferred:</p>
                <ul className="text-small text-neutral-500 space-y-0.5">
                  {aliasCount > 0 && (
                    <li>{aliasCount} alias{aliasCount !== 1 ? 'es' : ''}</li>
                  )}
                  {transactionCount > 0 && (
                    <li>{transactionCount} transaction{transactionCount !== 1 ? 's' : ''}</li>
                  )}
                </ul>
              </div>
            )}

            {mergeMutation.isError && (
              <p className="text-small text-expensive">
                Failed to merge products. Please try again.
              </p>
            )}
          </div>
        )}
      </div>
    </Modal>
  )
}

export { ProductMerge }
