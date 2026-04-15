import { useState, useMemo, useCallback, useEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { listProducts, createProduct, updateProduct } from '@/api/products'
import { fetchGroups, createGroup } from '@/api/groups'
import type { ProductListItem, ProductGroup } from '@/types'
import { getProductsWithTrends } from '@/api/analytics'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { Sparkline } from '@/components/ui/Sparkline'
import { EditableTable } from '@/components/ui/EditableTable'
import type { ColumnDef, CellContext } from '@tanstack/react-table'
import type { ProductWithTrend } from '@/types'

// --- Groups List View ---

function GroupsView() {
  const queryClient = useQueryClient()

  const { data: groups, isLoading } = useQuery({
    queryKey: ['product-groups'],
    queryFn: fetchGroups,
  })

  const createMutation = useMutation({
    mutationFn: (name: string) => createGroup({ name }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['product-groups'] })
    },
  })

  const handleCreateGroup = useCallback(() => {
    const name = window.prompt('Group name:')
    if (name && name.trim()) {
      createMutation.mutate(name.trim())
    }
  }, [createMutation])

  if (isLoading) {
    return <p className="text-body text-neutral-400">Loading groups...</p>
  }

  const groupList = groups ?? []

  if (groupList.length === 0) {
    return (
      <div className="text-center py-16">
        <p className="text-body text-neutral-400">No product groups yet.</p>
        <p className="text-caption text-neutral-400 mt-1">
          Groups let you compare similar products across stores and brands.
        </p>
        <div className="mt-4">
          <Button onClick={handleCreateGroup}>Create First Group</Button>
        </div>
      </div>
    )
  }

  return (
    <div>
      <div className="flex justify-end mb-4">
        <Button size="sm" onClick={handleCreateGroup}>Create Group</Button>
      </div>
      <div className="space-y-2">
        {groupList.map((g: ProductGroup) => (
          <Link
            key={g.id}
            to={`/product-groups/${g.id}`}
            className="block bg-white rounded-2xl shadow-subtle p-4 hover:shadow-md transition-shadow"
          >
            <div className="flex items-center justify-between">
              <div>
                <h3 className="text-body-medium font-semibold text-neutral-900">{g.name}</h3>
                <div className="flex items-center gap-3 mt-1">
                  <span className="text-caption text-neutral-400">
                    {g.member_count} product{g.member_count !== 1 ? 's' : ''}
                  </span>
                  {g.comparison_unit && (
                    <span className="text-caption text-neutral-400">
                      Compare by: {g.comparison_unit}
                    </span>
                  )}
                </div>
              </div>
              <svg className="w-5 h-5 text-neutral-300" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
              </svg>
            </div>
          </Link>
        ))}
      </div>
    </div>
  )
}

// --- Products List View ---

interface ProductRow extends ProductListItem {
  trend: ProductWithTrend | null
}

function ProductsPage() {
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = searchParams.get('tab') === 'groups' ? 'groups' : 'products'
  const setActiveTab = (tab: 'products' | 'groups') => {
    setSearchParams(tab === 'groups' ? { tab: 'groups' } : {})
  }
  const [searchTerm, setSearchTerm] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [brandFilter, setBrandFilter] = useState('')
  const [missingFilter, setMissingFilter] = useState<'' | 'missing_brand' | 'missing_pack'>('')
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current)
    }
    debounceRef.current = setTimeout(() => {
      setDebouncedSearch(searchTerm)
    }, 300)
    return () => {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current)
      }
    }
  }, [searchTerm])

  const { data: products, isLoading } = useQuery({
    queryKey: ['products', debouncedSearch],
    queryFn: () => listProducts(debouncedSearch ? { search: debouncedSearch } : undefined),
  })

  const { data: productsWithTrends } = useQuery({
    queryKey: ['analytics', 'products-trends'],
    queryFn: () => getProductsWithTrends(),
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: { name?: string; category?: string; default_unit?: string; brand?: string } }) =>
      updateProduct(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['products'] })
    },
  })

  const createMutation = useMutation({
    mutationFn: (data: { name: string }) => createProduct(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['products'] })
    },
  })

  const trendMap = useMemo(() => {
    const map = new Map<string, ProductWithTrend>()
    if (productsWithTrends) {
      for (const pwt of productsWithTrends) {
        map.set(pwt.id, pwt)
      }
    }
    return map
  }, [productsWithTrends])

  // Distinct brands for filter dropdown
  const distinctBrands = useMemo(() => {
    if (!products || !Array.isArray(products)) return []
    const brands = new Set<string>()
    for (const p of products) {
      if (p.brand) brands.add(p.brand)
    }
    return Array.from(brands).sort()
  }, [products])

  const rows: ProductRow[] = useMemo(() => {
    if (!products || !Array.isArray(products)) return []
    return products
      .filter((p) => {
        if (brandFilter && p.brand !== brandFilter) return false
        if (missingFilter === 'missing_brand' && p.brand) return false
        if (missingFilter === 'missing_pack' && p.pack_quantity != null) return false
        return true
      })
      .map((p) => ({
        ...p,
        trend: trendMap.get(p.id) ?? null,
      }))
  }, [products, trendMap, brandFilter, missingFilter])

  const handleCellUpdate = useCallback(
    (rowIndex: number, columnId: string, value: string) => {
      const product = rows[rowIndex]
      if (!product) return

      const fieldMap: Record<string, string> = {
        name: 'name',
        category: 'category',
        default_unit: 'default_unit',
        brand: 'brand',
      }

      const field = fieldMap[columnId]
      if (!field) return

      updateMutation.mutate({
        id: product.id,
        data: { [field]: value || undefined },
      })
    },
    [rows, updateMutation],
  )

  const handleAddProduct = useCallback(() => {
    const name = window.prompt('Product name:')
    if (name && name.trim()) {
      createMutation.mutate({ name: name.trim() })
    }
  }, [createMutation])

  const columns: ColumnDef<ProductRow, unknown>[] = useMemo(
    () => [
      {
        accessorKey: 'name',
        header: 'Name',
        size: 240,
        meta: { editable: true, cellType: 'text' as const },
        cell: ({ getValue, row }: CellContext<ProductRow, unknown>) => {
          const name = getValue() as string
          const product = row.original
          return (
            <span className="flex items-center gap-1.5">
              <Link
                to={`/products/${product.id}`}
                className="text-brand hover:underline"
                onClick={(e) => e.stopPropagation()}
              >
                {name}
              </Link>
              {product.product_group_id && (
                <span className="inline-block w-2 h-2 rounded-full bg-brand flex-shrink-0" title="In a product group" />
              )}
            </span>
          )
        },
      },
      {
        accessorKey: 'brand',
        header: 'Brand',
        size: 130,
        meta: { editable: true, cellType: 'text' as const },
        cell: ({ getValue }) => {
          const val = getValue() as string | undefined
          return val ?? '\u2014'
        },
      },
      {
        accessorKey: 'category',
        header: 'Category',
        size: 160,
        meta: { editable: true, cellType: 'text' as const },
        cell: ({ getValue }) => {
          const val = getValue() as string | null
          return val ?? '\u2014'
        },
      },
      {
        accessorKey: 'default_unit',
        header: 'Default Unit',
        size: 120,
        meta: { editable: true, cellType: 'text' as const },
        cell: ({ getValue }) => {
          const val = getValue() as string | null
          return val ?? '\u2014'
        },
      },
      {
        accessorKey: 'alias_count',
        header: 'Aliases',
        size: 80,
        cell: ({ getValue }) => {
          const val = getValue() as number
          return val
        },
      },
      {
        accessorKey: 'last_price',
        header: 'Last Price',
        size: 100,
        cell: ({ getValue }) => {
          const val = getValue() as string | null
          if (!val) return '\u2014'
          const num = parseFloat(val)
          if (isNaN(num)) return '\u2014'
          return `$${num.toFixed(2)}`
        },
      },
      {
        id: 'sparkline',
        header: () => <span title="Green = sale price">Trend</span>,
        size: 100,
        cell: ({ row }: CellContext<ProductRow, unknown>) => {
          const t = row.original.trend
          if (!t) return <span className="text-small text-neutral-400">&mdash;</span>
          const points = t.sparkline ?? []
          const sparkData = points.map((p) => parseFloat(p.price)).filter((n) => !isNaN(n))
          const sparkHighlights = points.map((p) => p.is_sale)
          if (sparkData.length < 2) return <span className="text-small text-neutral-400">&mdash;</span>
          return <Sparkline data={sparkData} highlights={sparkHighlights} />
        },
      },
      {
        id: 'price_change',
        header: '% Change',
        size: 90,
        cell: ({ row }: CellContext<ProductRow, unknown>) => {
          const t = row.original.trend
          if (!t) return <span className="text-small text-neutral-400">&mdash;</span>
          const pct = t.percent_change
          const variant = pct > 0 ? 'warning' : pct < 0 ? 'success' : 'neutral'
          return (
            <Badge variant={variant}>
              {pct > 0 ? '+' : ''}{pct.toFixed(1)}%
            </Badge>
          )
        },
      },
    ],
    [],
  )

  return (
    <div className="py-8">
      <div className="flex items-center justify-between mb-6">
        <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight">
          Products
        </h1>
        <div className="flex items-center gap-2">
          {activeTab === 'products' && (
            <Button size="sm" onClick={handleAddProduct}>
              Add Product
            </Button>
          )}
        </div>
      </div>

      {/* Tab toggle */}
      <div className="flex gap-1 mb-6 bg-neutral-100 rounded-xl p-1 w-fit">
        <button
          type="button"
          className={`px-4 py-2 text-body rounded-lg transition-colors cursor-pointer ${
            activeTab === 'products'
              ? 'bg-white shadow-sm text-neutral-900 font-medium'
              : 'text-neutral-500 hover:text-neutral-700'
          }`}
          onClick={() => setActiveTab('products')}
        >
          All Products
        </button>
        <button
          type="button"
          className={`px-4 py-2 text-body rounded-lg transition-colors cursor-pointer ${
            activeTab === 'groups'
              ? 'bg-white shadow-sm text-neutral-900 font-medium'
              : 'text-neutral-500 hover:text-neutral-700'
          }`}
          onClick={() => setActiveTab('groups')}
        >
          Groups
        </button>
      </div>

      {activeTab === 'groups' ? (
        <GroupsView />
      ) : (
      <>
      <div className="mb-4">
        <div className="flex items-center gap-3 flex-wrap">
          <input
            type="text"
            value={searchTerm}
            onChange={(e) => setSearchTerm(e.target.value)}
            placeholder="Search products and aliases..."
            className="w-full max-w-sm px-3 py-2 text-body border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
          />
          <select
            value={brandFilter}
            onChange={(e) => setBrandFilter(e.target.value)}
            className="px-3 py-2 text-body border border-neutral-200 rounded-xl bg-white focus:outline-none focus:ring-2 focus:ring-brand"
          >
            <option value="">All brands</option>
            {distinctBrands.map((b) => (
              <option key={b} value={b}>{b}</option>
            ))}
          </select>
          <select
            value={missingFilter}
            onChange={(e) => setMissingFilter(e.target.value as '' | 'missing_brand' | 'missing_pack')}
            className="px-3 py-2 text-body border border-neutral-200 rounded-xl bg-white focus:outline-none focus:ring-2 focus:ring-brand"
          >
            <option value="">All products</option>
            <option value="missing_brand">Missing brand</option>
            <option value="missing_pack">Missing pack size</option>
          </select>
          {debouncedSearch && !isLoading && (
            <span className="text-caption text-neutral-400">
              {rows.length} result{rows.length !== 1 ? 's' : ''}
            </span>
          )}
        </div>
        {debouncedSearch && (
          <p className="mt-1 text-small text-neutral-400">
            Searching product names and aliases
          </p>
        )}
      </div>

      {isLoading ? (
        <p className="text-body text-neutral-400">Loading products...</p>
      ) : rows.length === 0 ? (
        <div className="text-center py-16">
          <p className="text-body text-neutral-400">
            {debouncedSearch ? 'No products match your search.' : 'No products yet.'}
          </p>
          {!debouncedSearch && (
            <div className="mt-4">
              <Button onClick={handleAddProduct}>Add Your First Product</Button>
            </div>
          )}
        </div>
      ) : (
        <EditableTable
          columns={columns}
          data={rows}
          onCellUpdate={handleCellUpdate}
          virtualizeRows={rows.length > 100}
        />
      )}
      </>
      )}
    </div>
  )
}

export default ProductsPage
