import { useState, useCallback } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { fetchGroup, updateGroup, deleteGroup } from '@/api/groups'
import { listProducts, updateProduct } from '@/api/products'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { Modal } from '@/components/ui/Modal'
import type { GroupMember, ProductListItem } from '@/types'

function formatPrice(price: string | undefined): string {
  if (!price) return '\u2014'
  const num = parseFloat(price)
  if (isNaN(num)) return '\u2014'
  return `$${num.toFixed(2)}`
}

function MemberRow({ member, isBestDeal, onRemove, removing }: {
  member: GroupMember
  isBestDeal: boolean
  onRemove: () => void
  removing: boolean
}) {
  return (
    <tr className={`border-b border-neutral-200 last:border-0 ${isBestDeal ? 'bg-success-subtle/30' : ''}`}>
      <td className="py-2.5 text-body-medium text-neutral-900">
        <Link to={`/products/${member.product_id}`} className="text-brand hover:underline">
          {member.product_name}
        </Link>
      </td>
      <td className="py-2.5 text-caption text-neutral-600">{member.brand ?? '\u2014'}</td>
      <td className="py-2.5 text-caption text-neutral-600">{member.store_name ?? '\u2014'}</td>
      <td className="py-2.5 text-caption text-neutral-600">
        {member.pack_quantity ? `${member.pack_quantity} ${member.pack_unit ?? ''}`.trim() : '\u2014'}
      </td>
      <td className="py-2.5 text-right text-caption font-medium text-neutral-900">
        {formatPrice(member.latest_price)}
      </td>
      <td className={`py-2.5 text-right font-medium ${isBestDeal ? 'text-success-dark' : 'text-neutral-600'}`}>
        {formatPrice(member.price_per_unit)}
        {isBestDeal && <Badge variant="success" className="ml-2">Best</Badge>}
      </td>
      <td className="py-2.5 text-right text-caption text-neutral-400">
        {member.last_purchased ?? '\u2014'}
      </td>
      <td className="py-2.5 text-right">
        <Button size="sm" variant="subtle" onClick={onRemove} disabled={removing}>
          Remove
        </Button>
      </td>
    </tr>
  )
}

function AddProductModal({ open, onClose, groupId, existingMemberIds }: {
  open: boolean
  onClose: () => void
  groupId: string
  existingMemberIds: Set<string>
}) {
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')

  const { data: products } = useQuery({
    queryKey: ['products', search],
    queryFn: () => listProducts(search ? { search } : undefined),
    enabled: open,
  })

  const addMutation = useMutation({
    mutationFn: (productId: string) =>
      updateProduct(productId, { product_group_id: groupId }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-group', groupId] })
      queryClient.invalidateQueries({ queryKey: ['products'] })
      queryClient.invalidateQueries({ queryKey: ['product-groups'] })
    },
  })

  const available = (products ?? []).filter((p: ProductListItem) => !existingMemberIds.has(p.id))

  return (
    <Modal open={open} onClose={onClose} title="Add Product to Group">
      <div className="space-y-4">
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search products..."
          className="w-full px-3 py-2 text-body border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
          autoFocus
        />
        <div className="max-h-64 overflow-y-auto space-y-1">
          {available.length === 0 ? (
            <p className="text-caption text-neutral-400 py-4 text-center">
              {search ? 'No matching products found.' : 'No products available.'}
            </p>
          ) : (
            available.slice(0, 20).map((p: ProductListItem) => (
              <button
                key={p.id}
                type="button"
                className="w-full text-left px-3 py-2 rounded-xl hover:bg-neutral-50 transition-colors flex items-center justify-between cursor-pointer"
                onClick={() => {
                  addMutation.mutate(p.id)
                }}
                disabled={addMutation.isPending}
              >
                <div>
                  <span className="text-body text-neutral-900">{p.name}</span>
                  {p.brand && <span className="ml-2 text-caption text-neutral-400">{p.brand}</span>}
                </div>
                {addMutation.isPending ? (
                  <span className="text-caption text-neutral-400">Adding...</span>
                ) : (
                  <span className="text-caption text-brand">+ Add</span>
                )}
              </button>
            ))
          )}
        </div>
      </div>
    </Modal>
  )
}

function ProductGroupPage() {
  const { id } = useParams<{ id: string }>()
  const groupId = id ?? ''
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const [editingName, setEditingName] = useState(false)
  const [nameValue, setNameValue] = useState('')
  const [editingUnit, setEditingUnit] = useState(false)
  const [unitValue, setUnitValue] = useState('')
  const [showAddProduct, setShowAddProduct] = useState(false)
  const [deleteConfirm, setDeleteConfirm] = useState(false)

  const { data: group, isLoading, error } = useQuery({
    queryKey: ['product-group', groupId],
    queryFn: () => fetchGroup(groupId),
    enabled: !!groupId,
  })

  const updateMutation = useMutation({
    mutationFn: (data: { name?: string; comparison_unit?: string }) =>
      updateGroup(groupId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-group', groupId] })
      queryClient.invalidateQueries({ queryKey: ['product-groups'] })
      setEditingName(false)
      setEditingUnit(false)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteGroup(groupId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-groups'] })
      navigate('/products')
    },
  })

  const removeMemberMutation = useMutation({
    mutationFn: (productId: string) =>
      updateProduct(productId, { product_group_id: null }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-group', groupId] })
      queryClient.invalidateQueries({ queryKey: ['products'] })
      queryClient.invalidateQueries({ queryKey: ['product-groups'] })
    },
  })

  const handleStartEditName = useCallback(() => {
    if (group) {
      setNameValue(group.name)
      setEditingName(true)
    }
  }, [group])

  const handleSaveName = useCallback(() => {
    if (nameValue.trim()) {
      updateMutation.mutate({ name: nameValue.trim() })
    }
  }, [nameValue, updateMutation])

  const handleStartEditUnit = useCallback(() => {
    if (group) {
      setUnitValue(group.comparison_unit ?? '')
      setEditingUnit(true)
    }
  }, [group])

  const handleSaveUnit = useCallback(() => {
    updateMutation.mutate({ comparison_unit: unitValue.trim() || undefined })
  }, [unitValue, updateMutation])

  if (isLoading) {
    return (
      <div className="py-8">
        <p className="text-body text-neutral-400">Loading group...</p>
      </div>
    )
  }

  if (error || !group) {
    return (
      <div className="py-8">
        <p className="text-body text-expensive">Failed to load product group.</p>
        <Link to="/products" className="text-body text-brand hover:underline mt-2 inline-block">
          Back to Products
        </Link>
      </div>
    )
  }

  const members = group.members ?? []
  const existingMemberIds = new Set(members.map((m) => m.product_id))

  // Find best deal (lowest price_per_unit)
  let bestDealId: string | null = null
  let lowestPPU = Infinity
  for (const m of members) {
    if (m.price_per_unit) {
      const ppu = parseFloat(m.price_per_unit)
      if (!isNaN(ppu) && ppu < lowestPPU) {
        lowestPPU = ppu
        bestDealId = m.product_id
      }
    }
  }

  return (
    <div className="py-8 max-w-4xl">
      {/* Breadcrumb */}
      <div className="mb-4">
        <Link to="/products" className="text-caption text-brand hover:underline">
          Products
        </Link>
        <span className="text-caption text-neutral-400 mx-2">/</span>
        <span className="text-caption text-neutral-600">{group.name}</span>
      </div>

      {/* Header */}
      <div className="flex items-start justify-between mb-6">
        <div>
          {editingName ? (
            <div className="flex items-center gap-2">
              <input
                type="text"
                value={nameValue}
                onChange={(e) => setNameValue(e.target.value)}
                className="font-display text-subhead font-bold text-neutral-900 tracking-tight px-2 py-1 border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand"
                onBlur={handleSaveName}
                onKeyDown={(e) => { if (e.key === 'Enter') handleSaveName(); if (e.key === 'Escape') setEditingName(false) }}
                autoFocus
              />
            </div>
          ) : (
            <h1
              className="font-display text-subhead font-bold text-neutral-900 tracking-tight cursor-pointer hover:text-brand transition-colors"
              onClick={handleStartEditName}
              title="Click to edit name"
            >
              {group.name}
            </h1>
          )}
          <div className="flex items-center gap-3 mt-2">
            <Badge variant="neutral">{group.member_count} member{group.member_count !== 1 ? 's' : ''}</Badge>
            {editingUnit ? (
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  value={unitValue}
                  onChange={(e) => setUnitValue(e.target.value)}
                  placeholder="e.g., oz, lb, ct"
                  className="w-32 px-2 py-1 text-caption border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand"
                  onBlur={handleSaveUnit}
                  onKeyDown={(e) => { if (e.key === 'Enter') handleSaveUnit(); if (e.key === 'Escape') setEditingUnit(false) }}
                  autoFocus
                />
              </div>
            ) : (
              <span
                className="text-caption text-neutral-400 cursor-pointer hover:text-brand transition-colors"
                onClick={handleStartEditUnit}
                title="Click to edit comparison unit"
              >
                Comparison unit: {group.comparison_unit ?? 'not set'}
              </span>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => setShowAddProduct(true)}>
            Add Product
          </Button>
          <Button size="sm" variant="subtle" className="text-expensive" onClick={() => setDeleteConfirm(true)}>
            Delete Group
          </Button>
        </div>
      </div>

      {/* Mixed unit warning */}
      {group.units_mixed && (
        <div className="mb-4 px-4 py-3 bg-amber-50 border border-amber-200 rounded-xl">
          <p className="text-body text-amber-800">
            <strong>Mixed units detected.</strong> Members of this group have different pack units, so price-per-unit comparisons may not be accurate. Set a comparison unit and ensure all members have matching pack units for reliable comparisons.
          </p>
        </div>
      )}

      {/* Member comparison table */}
      {members.length === 0 ? (
        <div className="bg-white rounded-2xl shadow-subtle p-8 text-center">
          <p className="text-body text-neutral-400 mb-4">No products in this group yet.</p>
          <Button onClick={() => setShowAddProduct(true)}>Add First Product</Button>
        </div>
      ) : (
        <div className="bg-white rounded-2xl shadow-subtle p-5">
          <h2 className="font-display text-feature font-semibold text-neutral-900 mb-3">Product Comparison</h2>
          <div className="overflow-x-auto">
            <table className="w-full text-left">
              <thead>
                <tr className="border-b border-neutral-200">
                  <th className="pb-2 text-small font-medium text-neutral-400">Product</th>
                  <th className="pb-2 text-small font-medium text-neutral-400">Brand</th>
                  <th className="pb-2 text-small font-medium text-neutral-400">Store</th>
                  <th className="pb-2 text-small font-medium text-neutral-400">Pack Size</th>
                  <th className="pb-2 text-small font-medium text-neutral-400 text-right">Price</th>
                  <th className="pb-2 text-small font-medium text-neutral-400 text-right">
                    Per {group.comparison_unit ?? 'Unit'}
                  </th>
                  <th className="pb-2 text-small font-medium text-neutral-400 text-right">Last Purchased</th>
                  <th className="pb-2 text-small font-medium text-neutral-400 text-right"></th>
                </tr>
              </thead>
              <tbody>
                {members.map((member) => (
                  <MemberRow
                    key={member.product_id}
                    member={member}
                    isBestDeal={member.product_id === bestDealId}
                    onRemove={() => removeMemberMutation.mutate(member.product_id)}
                    removing={removeMemberMutation.isPending}
                  />
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Add Product Modal */}
      <AddProductModal
        open={showAddProduct}
        onClose={() => setShowAddProduct(false)}
        groupId={groupId}
        existingMemberIds={existingMemberIds}
      />

      {/* Delete Confirmation */}
      <Modal
        open={deleteConfirm}
        onClose={() => setDeleteConfirm(false)}
        title="Delete Product Group"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setDeleteConfirm(false)}>
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
        }
      >
        <p className="text-body text-neutral-600">
          Delete group &quot;{group.name}&quot;? Products will be ungrouped but not deleted.
        </p>
      </Modal>
    </div>
  )
}

export default ProductGroupPage
