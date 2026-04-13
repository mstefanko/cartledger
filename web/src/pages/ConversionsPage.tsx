import { useState, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listConversions, createConversion, deleteConversion } from '@/api/conversions'
import { listProducts } from '@/api/products'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Modal } from '@/components/ui/Modal'
import type { UnitConversion, Product } from '@/types'

function ConversionsPage() {
  const queryClient = useQueryClient()
  const [showAdd, setShowAdd] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<UnitConversion | null>(null)

  // Form state
  const [fromUnit, setFromUnit] = useState('')
  const [toUnit, setToUnit] = useState('')
  const [factor, setFactor] = useState('')
  const [productId, setProductId] = useState('')

  const conversionsQuery = useQuery({
    queryKey: ['conversions'],
    queryFn: listConversions,
  })

  const productsQuery = useQuery({
    queryKey: ['products'],
    queryFn: () => listProducts(),
  })

  const createMutation = useMutation({
    mutationFn: createConversion,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['conversions'] })
      resetForm()
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteConversion,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['conversions'] })
      setDeleteTarget(null)
    },
  })

  function resetForm() {
    setFromUnit('')
    setToUnit('')
    setFactor('')
    setProductId('')
    setShowAdd(false)
  }

  function handleCreate() {
    if (!fromUnit.trim() || !toUnit.trim() || !factor.trim()) return
    createMutation.mutate({
      from_unit: fromUnit.trim(),
      to_unit: toUnit.trim(),
      factor: factor.trim(),
      product_id: productId || undefined,
    })
  }

  const conversions = conversionsQuery.data ?? []
  const products = productsQuery.data ?? []

  const productNameById = (id: string | null): string | null => {
    if (!id) return null
    return products.find((p: Product) => p.id === id)?.name ?? null
  }

  const { generic, productSpecific } = useMemo(() => {
    const gen: UnitConversion[] = []
    const spec: UnitConversion[] = []
    for (const c of conversions) {
      if (c.product_id) {
        spec.push(c)
      } else {
        gen.push(c)
      }
    }
    return { generic: gen, productSpecific: spec }
  }, [conversions])

  return (
    <div className="py-8 max-w-4xl">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight">
            Unit Conversions
          </h1>
          <p className="text-body text-neutral-500 mt-1">
            Manage conversion factors for normalizing prices across different units.
          </p>
        </div>
        <Button size="sm" onClick={() => setShowAdd(!showAdd)}>
          {showAdd ? 'Cancel' : '+ Add Conversion'}
        </Button>
      </div>

      {/* Add Conversion Form */}
      {showAdd && (
        <div className="bg-white rounded-2xl shadow-subtle p-5 mb-6">
          <h2 className="font-display text-feature font-semibold text-neutral-900 mb-4">
            New Conversion
          </h2>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <Input
              label="From Unit"
              placeholder="e.g., cup"
              value={fromUnit}
              onChange={(e) => setFromUnit(e.target.value)}
            />
            <Input
              label="To Unit"
              placeholder="e.g., oz"
              value={toUnit}
              onChange={(e) => setToUnit(e.target.value)}
            />
            <Input
              label="Factor"
              placeholder="e.g., 8.0"
              type="number"
              step="any"
              value={factor}
              onChange={(e) => setFactor(e.target.value)}
              helperText="1 from_unit = factor * to_unit"
            />
            <div className="flex flex-col gap-1.5">
              <label className="text-caption font-medium text-neutral-900">
                Product (optional)
              </label>
              <select
                value={productId}
                onChange={(e) => setProductId(e.target.value)}
                className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 text-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand transition-colors"
              >
                <option value="">Generic (all products)</option>
                {products.map((p: Product) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <div className="flex justify-end mt-4">
            <Button
              size="sm"
              onClick={handleCreate}
              disabled={!fromUnit.trim() || !toUnit.trim() || !factor.trim() || createMutation.isPending}
            >
              {createMutation.isPending ? 'Creating...' : 'Create Conversion'}
            </Button>
          </div>
          {createMutation.isError && (
            <p className="text-small text-expensive mt-2">
              Failed to create conversion. Please check your inputs and try again.
            </p>
          )}
        </div>
      )}

      {conversionsQuery.isLoading && (
        <p className="text-body text-neutral-400">Loading conversions...</p>
      )}

      {conversionsQuery.isError && (
        <p className="text-body text-expensive">Failed to load conversions.</p>
      )}

      {!conversionsQuery.isLoading && conversions.length === 0 && (
        <div className="bg-white rounded-2xl shadow-subtle p-6">
          <p className="text-body text-neutral-400">
            No custom conversions yet. The system uses built-in conversions for standard weight and volume units.
            Add custom conversions for product-specific densities (e.g., 1 cup flour = 4.25 oz).
          </p>
        </div>
      )}

      {/* Generic Conversions */}
      {generic.length > 0 && (
        <ConversionTable
          title="Generic Conversions"
          subtitle="Applied to all products"
          conversions={generic}
          productNameById={productNameById}
          onDelete={setDeleteTarget}
        />
      )}

      {/* Product-Specific Conversions */}
      {productSpecific.length > 0 && (
        <ConversionTable
          title="Product-Specific Conversions"
          subtitle="Applied only to the assigned product"
          conversions={productSpecific}
          productNameById={productNameById}
          onDelete={setDeleteTarget}
          className="mt-5"
        />
      )}

      {/* Delete Confirmation */}
      <Modal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        title="Delete Conversion"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setDeleteTarget(null)}>
              Cancel
            </Button>
            <Button
              size="sm"
              className="bg-expensive text-white hover:opacity-90"
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
            Delete the conversion{' '}
            <span className="font-medium">
              1 {deleteTarget.from_unit} = {deleteTarget.factor} {deleteTarget.to_unit}
            </span>
            {deleteTarget.product_id && productNameById(deleteTarget.product_id) && (
              <> for <span className="font-medium">{productNameById(deleteTarget.product_id)}</span></>
            )}
            ? This cannot be undone.
          </p>
        )}
      </Modal>
    </div>
  )
}

// --- Conversion Table ---

function ConversionTable({
  title,
  subtitle,
  conversions,
  productNameById,
  onDelete,
  className = '',
}: {
  title: string
  subtitle: string
  conversions: UnitConversion[]
  productNameById: (id: string | null) => string | null
  onDelete: (c: UnitConversion) => void
  className?: string
}) {
  return (
    <div className={`bg-white rounded-2xl shadow-subtle p-5 ${className}`}>
      <h2 className="font-display text-feature font-semibold text-neutral-900">{title}</h2>
      <p className="text-small text-neutral-400 mb-3">{subtitle}</p>
      <div className="overflow-x-auto">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-neutral-200">
              <th className="pb-2 text-small font-medium text-neutral-400">From</th>
              <th className="pb-2 text-small font-medium text-neutral-400">To</th>
              <th className="pb-2 text-small font-medium text-neutral-400 text-right">Factor</th>
              {conversions.some((c) => c.product_id) && (
                <th className="pb-2 text-small font-medium text-neutral-400">Product</th>
              )}
              <th className="pb-2 text-small font-medium text-neutral-400 text-right"></th>
            </tr>
          </thead>
          <tbody>
            {conversions.map((c) => (
              <tr key={c.id} className="border-b border-neutral-200 last:border-0">
                <td className="py-2.5 text-body text-neutral-900">{c.from_unit}</td>
                <td className="py-2.5 text-body text-neutral-900">{c.to_unit}</td>
                <td className="py-2.5 text-right text-body font-medium text-neutral-900">
                  {c.factor}
                </td>
                {conversions.some((conv) => conv.product_id) && (
                  <td className="py-2.5 text-caption text-neutral-600">
                    {productNameById(c.product_id) ?? '\u2014'}
                  </td>
                )}
                <td className="py-2.5 text-right">
                  <button
                    type="button"
                    className="p-1.5 text-neutral-300 hover:text-expensive rounded-lg hover:bg-neutral-50 transition-colors cursor-pointer"
                    onClick={() => onDelete(c)}
                    aria-label="Delete conversion"
                  >
                    <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                    </svg>
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

export default ConversionsPage
