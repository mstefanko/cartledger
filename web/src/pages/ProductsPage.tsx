import { useState, useMemo, useCallback, useEffect, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { listProducts, createProduct, updateProduct } from '@/api/products'
import { getProductsWithTrends } from '@/api/analytics'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { Sparkline } from '@/components/ui/Sparkline'
import { EditableTable } from '@/components/ui/EditableTable'
import type { ColumnDef, CellContext } from '@tanstack/react-table'
import type { Product, ProductTrend } from '@/types'

interface ProductRow extends Product {
  alias_count: number
  last_price: string | null
  trend: ProductTrend | null
}

function ProductsPage() {
  const queryClient = useQueryClient()
  const [searchTerm, setSearchTerm] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
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
    mutationFn: ({ id, data }: { id: string; data: { name?: string; category?: string; default_unit?: string } }) =>
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
    const map = new Map<string, ProductTrend>()
    if (productsWithTrends) {
      for (const pwt of productsWithTrends) {
        if (pwt.trend) map.set(pwt.product.id, pwt.trend)
      }
    }
    return map
  }, [productsWithTrends])

  const rows: ProductRow[] = useMemo(() => {
    if (!products || !Array.isArray(products)) return []
    return products.map((p) => ({
      ...p,
      alias_count: 0,
      last_price: null,
      trend: trendMap.get(p.id) ?? null,
    }))
  }, [products, trendMap])

  const handleCellUpdate = useCallback(
    (rowIndex: number, columnId: string, value: string) => {
      const product = rows[rowIndex]
      if (!product) return

      const fieldMap: Record<string, string> = {
        name: 'name',
        category: 'category',
        default_unit: 'default_unit',
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
            <Link
              to={`/products/${product.id}`}
              className="text-brand hover:underline"
              onClick={(e) => e.stopPropagation()}
            >
              {name}
            </Link>
          )
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
        header: 'Trend',
        size: 100,
        cell: ({ row }: CellContext<ProductRow, unknown>) => {
          const t = row.original.trend
          if (!t) return <span className="text-small text-neutral-400">&mdash;</span>
          const sparkData = t.sparkline.map((p) => parseFloat(p.price)).filter((n) => !isNaN(n))
          if (sparkData.length < 2) return <span className="text-small text-neutral-400">&mdash;</span>
          return <Sparkline data={sparkData} />
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
        <Button size="sm" onClick={handleAddProduct}>
          Add Product
        </Button>
      </div>

      <div className="mb-4">
        <div className="flex items-center gap-3">
          <input
            type="text"
            value={searchTerm}
            onChange={(e) => setSearchTerm(e.target.value)}
            placeholder="Search products and aliases..."
            className="w-full max-w-sm px-3 py-2 text-body border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
          />
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
    </div>
  )
}

export default ProductsPage
