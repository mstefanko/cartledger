import { useState, useEffect, useCallback, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Modal } from '@/components/ui/Modal'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { listProducts } from '@/api/products'
import { listStores } from '@/api/stores'
import type { MatchingRule, CreateRuleRequest, UpdateRuleRequest, Product } from '@/types'

const CONDITION_OPS = [
  { value: 'exact', label: 'Exact match' },
  { value: 'contains', label: 'Contains' },
  { value: 'starts_with', label: 'Starts with' },
  { value: 'matches', label: 'Regex matches' },
] as const

interface RuleFormModalProps {
  open: boolean
  onClose: () => void
  onSubmit: (data: CreateRuleRequest | UpdateRuleRequest) => void
  isSubmitting: boolean
  rule?: MatchingRule | null
}

function RuleFormModal({ open, onClose, onSubmit, isSubmitting, rule }: RuleFormModalProps) {
  const [conditionOp, setConditionOp] = useState('contains')
  const [conditionVal, setConditionVal] = useState('')
  const [storeId, setStoreId] = useState('')
  const [productId, setProductId] = useState('')
  const [priority, setPriority] = useState(0)
  const [productSearch, setProductSearch] = useState('')
  const [showProductDropdown, setShowProductDropdown] = useState(false)
  const [category, setCategory] = useState('')
  const dropdownRef = useRef<HTMLDivElement>(null)
  const searchTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [debouncedProductSearch, setDebouncedProductSearch] = useState('')

  const { data: stores = [] } = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  const { data: products = [] } = useQuery({
    queryKey: ['products', debouncedProductSearch],
    queryFn: () => listProducts(debouncedProductSearch ? { search: debouncedProductSearch } : undefined),
    enabled: open,
  })

  // Debounce product search
  useEffect(() => {
    if (searchTimeoutRef.current) clearTimeout(searchTimeoutRef.current)
    searchTimeoutRef.current = setTimeout(() => {
      setDebouncedProductSearch(productSearch)
    }, 300)
    return () => {
      if (searchTimeoutRef.current) clearTimeout(searchTimeoutRef.current)
    }
  }, [productSearch])

  // Pre-fill when editing
  useEffect(() => {
    if (rule) {
      setConditionOp(rule.condition_op)
      setConditionVal(rule.condition_val)
      setStoreId(rule.store_id ?? '')
      setProductId(rule.product_id)
      setPriority(rule.priority)
      setCategory(rule.category ?? '')
      // Find product name for display
      const product = products.find((p) => p.id === rule.product_id)
      setProductSearch(product?.name ?? '')
    } else {
      setConditionOp('contains')
      setConditionVal('')
      setStoreId('')
      setProductId('')
      setPriority(0)
      setCategory('')
      setProductSearch('')
    }
  }, [rule, open]) // eslint-disable-line react-hooks/exhaustive-deps

  // Close dropdown on outside click
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setShowProductDropdown(false)
      }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  const handleSelectProduct = useCallback((product: Product) => {
    setProductId(product.id)
    setProductSearch(product.name)
    setShowProductDropdown(false)
  }, [])

  const handleSubmit = useCallback(() => {
    if (!conditionVal.trim() || !productId) return

    const data: CreateRuleRequest = {
      condition_op: conditionOp,
      condition_val: conditionVal.trim(),
      product_id: productId,
      priority,
      store_id: storeId || undefined,
      category: category.trim() || undefined,
    }
    onSubmit(data)
  }, [conditionOp, conditionVal, productId, priority, storeId, category, onSubmit])

  const filteredProducts = products.slice(0, 10)

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={rule ? 'Edit Rule' : 'Create Matching Rule'}
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={handleSubmit}
            disabled={!conditionVal.trim() || !productId || isSubmitting}
          >
            {isSubmitting ? 'Saving...' : rule ? 'Update Rule' : 'Create Rule'}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {/* Condition Operation */}
        <div className="flex flex-col gap-1.5">
          <label className="text-caption font-medium text-neutral-900">Condition</label>
          <select
            value={conditionOp}
            onChange={(e) => setConditionOp(e.target.value)}
            className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 text-body font-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
          >
            {CONDITION_OPS.map((op) => (
              <option key={op.value} value={op.value}>
                {op.label}
              </option>
            ))}
          </select>
        </div>

        {/* Condition Value */}
        <Input
          label="Condition Value"
          value={conditionVal}
          onChange={(e) => setConditionVal(e.target.value)}
          placeholder="e.g., BNLS CHKN BRST"
        />

        {/* Store (optional) */}
        <div className="flex flex-col gap-1.5">
          <label className="text-caption font-medium text-neutral-900">Store (optional)</label>
          <select
            value={storeId}
            onChange={(e) => setStoreId(e.target.value)}
            className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 text-body font-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
          >
            <option value="">Any store</option>
            {stores.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </div>

        {/* Product (autocomplete search) */}
        <div className="flex flex-col gap-1.5" ref={dropdownRef}>
          <label className="text-caption font-medium text-neutral-900">Maps to Product</label>
          <div className="relative">
            <input
              type="text"
              value={productSearch}
              onChange={(e) => {
                setProductSearch(e.target.value)
                setShowProductDropdown(true)
                if (!e.target.value) setProductId('')
              }}
              onFocus={() => setShowProductDropdown(true)}
              placeholder="Search for a product..."
              className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 text-body font-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
            />
            {showProductDropdown && filteredProducts.length > 0 && (
              <div className="absolute z-20 top-full left-0 right-0 mt-1 bg-white border border-neutral-200 rounded-xl shadow-lg max-h-48 overflow-y-auto">
                {filteredProducts.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    className={[
                      'w-full text-left px-3 py-2 text-body hover:bg-brand-subtle transition-colors cursor-pointer',
                      p.id === productId ? 'bg-brand-subtle font-medium' : '',
                    ].join(' ')}
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
          {productId && (
            <p className="text-small text-success-dark">
              Selected: {productSearch}
            </p>
          )}
        </div>

        {/* Priority */}
        <Input
          label="Priority"
          type="number"
          value={String(priority)}
          onChange={(e) => setPriority(parseInt(e.target.value, 10) || 0)}
          helperText="Lower numbers = higher priority. Rules are evaluated in priority order."
        />

        {/* Category (optional) */}
        <Input
          label="Category (optional)"
          value={category}
          onChange={(e) => setCategory(e.target.value)}
          placeholder="e.g., Meat, Dairy"
        />
      </div>
    </Modal>
  )
}

export { RuleFormModal }
