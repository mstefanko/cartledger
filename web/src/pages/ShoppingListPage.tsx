import { useState, useCallback, useRef, useEffect, useMemo } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getList, updateList, updateItem, addItem, deleteItem } from '@/api/lists'
import { listProducts } from '@/api/products'
import { useAuth } from '@/hooks/useAuth'
import { useListWebSocket } from '@/hooks/useWebSocket'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { ShareListModal } from '@/components/lists/ShareListModal'
import type {
  ListItemWithPrice,
  ShoppingListDetail,
  Product,
} from '@/types'

function ShoppingListPage() {
  const { id: listId } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { user } = useAuth()

  const [editingName, setEditingName] = useState(false)
  const [nameValue, setNameValue] = useState('')
  const [showShare, setShowShare] = useState(false)
  const [remoteIndicator, setRemoteIndicator] = useState(false)

  // Add item state
  const [addInput, setAddInput] = useState('')
  const [showSuggestions, setShowSuggestions] = useState(false)
  const [highlightedIdx, setHighlightedIdx] = useState(0)
  const [selectedProduct, setSelectedProduct] = useState<Product | null>(null)
  const addInputRef = useRef<HTMLInputElement>(null)
  const suggestionsRef = useRef<HTMLDivElement>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const [searchQuery, setSearchQuery] = useState('')

  // Real-time updates
  useListWebSocket({
    listId: listId ?? '',
    onRemoteChange: () => {
      setRemoteIndicator(true)
      setTimeout(() => setRemoteIndicator(false), 2000)
    },
  })

  // Fetch list detail
  const listQuery = useQuery({
    queryKey: ['shopping-list', listId],
    queryFn: () => getList(listId!),
    enabled: !!listId,
  })

  // Product search for autocomplete
  const productsQuery = useQuery({
    queryKey: ['products', { search: searchQuery }],
    queryFn: () => listProducts({ search: searchQuery }),
    enabled: searchQuery.length >= 2,
  })

  const list = listQuery.data
  const suggestions = productsQuery.data ?? []

  // Separate checked and unchecked items
  const { uncheckedItems, checkedItems } = useMemo(() => {
    if (!list) return { uncheckedItems: [], checkedItems: [] }
    const unchecked = list.items.filter((i) => !i.checked)
    const checked = list.items.filter((i) => i.checked)
    return { uncheckedItems: unchecked, checkedItems: checked }
  }, [list])

  // Estimated total for unchecked items
  const estimatedTotal = useMemo(() => {
    return uncheckedItems.reduce((sum, item) => {
      if (item.estimated_price) {
        return sum + parseFloat(item.estimated_price)
      }
      return sum
    }, 0)
  }, [uncheckedItems])

  // Update list name mutation
  const updateNameMutation = useMutation({
    mutationFn: (name: string) => updateList(listId!, { name }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
      setEditingName(false)
    },
  })

  // Toggle check mutation with optimistic update
  const toggleCheckMutation = useMutation({
    mutationFn: ({
      itemId,
      checked,
    }: {
      itemId: string
      checked: boolean
    }) =>
      updateItem(listId!, itemId, {
        checked,
        checked_by: checked ? user?.id : undefined,
      }),
    onMutate: async ({ itemId, checked }) => {
      // Cancel outgoing refetches
      await queryClient.cancelQueries({ queryKey: ['shopping-list', listId] })

      // Snapshot previous value
      const previous = queryClient.getQueryData<ShoppingListDetail>([
        'shopping-list',
        listId,
      ])

      // Optimistically update
      if (previous) {
        queryClient.setQueryData<ShoppingListDetail>(
          ['shopping-list', listId],
          {
            ...previous,
            items: previous.items.map((item) =>
              item.id === itemId
                ? { ...item, checked, checked_by: checked ? user?.id ?? null : null }
                : item,
            ),
          },
        )
      }

      return { previous }
    },
    onError: (_err, _vars, context) => {
      // Rollback on error
      if (context?.previous) {
        queryClient.setQueryData(['shopping-list', listId], context.previous)
      }
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
    },
  })

  // Delete item mutation
  const deleteItemMutation = useMutation({
    mutationFn: (itemId: string) => deleteItem(listId!, itemId),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
    },
  })

  // Handle add input changes with debounced search
  const handleAddInputChange = useCallback((value: string) => {
    setAddInput(value)
    setSelectedProduct(null)
    setHighlightedIdx(0)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    if (value.trim().length >= 2) {
      debounceRef.current = setTimeout(() => {
        setSearchQuery(value.trim())
        setShowSuggestions(true)
      }, 200)
    } else {
      setShowSuggestions(false)
    }
  }, [])

  useEffect(() => {
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [])

  function handleSelectProduct(product: Product) {
    setSelectedProduct(product)
    setAddInput(product.name)
    setShowSuggestions(false)
  }

  const addItemDirect = useCallback(() => {
    if (!listId) return
    const name = addInput.trim()
    if (!name) return

    const req = {
      name,
      product_id: selectedProduct?.id,
      unit: selectedProduct?.default_unit ?? undefined,
    }

    // Use direct API call + invalidate, since the mutation shape is (listId, data)
    addItem(listId, req).then(
      () => {
        void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
        void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
        setAddInput('')
        setSelectedProduct(null)
        setShowSuggestions(false)
        addInputRef.current?.focus()
      },
      () => {
        // Error — could show a toast
      },
    )
  }, [listId, addInput, selectedProduct, queryClient])

  function handleAddKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (showSuggestions && suggestions.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setHighlightedIdx((prev) => Math.min(prev + 1, suggestions.length - 1))
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setHighlightedIdx((prev) => Math.max(prev - 1, 0))
        return
      }
      if (e.key === 'Enter') {
        e.preventDefault()
        const selected = suggestions[highlightedIdx]
        if (selected) {
          handleSelectProduct(selected)
        }
        return
      }
    }
    if (e.key === 'Enter' && !showSuggestions) {
      e.preventDefault()
      addItemDirect()
    }
    if (e.key === 'Escape') {
      setShowSuggestions(false)
    }
  }

  function startEditName() {
    if (!list) return
    setNameValue(list.name)
    setEditingName(true)
  }

  function saveName() {
    const name = nameValue.trim()
    if (!name || name === list?.name) {
      setEditingName(false)
      return
    }
    updateNameMutation.mutate(name)
  }

  // Scroll highlighted suggestion into view
  useEffect(() => {
    if (showSuggestions && suggestionsRef.current) {
      const el = suggestionsRef.current.children[highlightedIdx] as HTMLElement | undefined
      el?.scrollIntoView({ block: 'nearest' })
    }
  }, [highlightedIdx, showSuggestions])

  if (!listId) {
    navigate('/lists')
    return null
  }

  if (listQuery.isLoading) {
    return (
      <div className="max-w-2xl mx-auto py-8">
        <p className="text-body text-neutral-400">Loading list...</p>
      </div>
    )
  }

  if (listQuery.isError || !list) {
    return (
      <div className="max-w-2xl mx-auto py-8">
        <p className="text-body text-expensive">Failed to load list.</p>
        <Button variant="secondary" size="sm" className="mt-4" onClick={() => navigate('/lists')}>
          Back to Lists
        </Button>
      </div>
    )
  }

  const statusVariant = list.status === 'active' ? 'success' : 'neutral'

  return (
    <div className="max-w-2xl mx-auto">
      {/* Remote change indicator */}
      {remoteIndicator && (
        <div className="fixed top-4 right-4 z-50 bg-brand-subtle text-brand text-small font-medium px-3 py-2 rounded-lg shadow-subtle animate-pulse">
          List updated by another user
        </div>
      )}

      {/* Breadcrumb */}
      <div className="mb-4">
        <Link to="/lists" className="text-caption text-brand hover:underline">
          &larr; Back to Lists
        </Link>
      </div>

      {/* Header */}
      <div className="flex items-start justify-between mb-6">
        <div className="flex-1 min-w-0">
          {editingName ? (
            <div className="flex items-center gap-2">
              <input
                type="text"
                value={nameValue}
                onChange={(e) => setNameValue(e.target.value)}
                onBlur={saveName}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') saveName()
                  if (e.key === 'Escape') setEditingName(false)
                }}
                className="font-display text-subhead font-bold text-neutral-900 bg-transparent border-b-2 border-brand focus:outline-none w-full"
                autoFocus
              />
            </div>
          ) : (
            <h1
              className="font-display text-subhead font-bold text-neutral-900 cursor-pointer hover:text-brand transition-colors"
              onClick={startEditName}
              title="Click to edit name"
            >
              {list.name}
            </h1>
          )}
          <div className="flex items-center gap-2 mt-1">
            <Badge variant={statusVariant}>{list.status}</Badge>
            {estimatedTotal > 0 && (
              <span className="text-caption font-medium text-brand">
                Est. ${estimatedTotal.toFixed(2)}
              </span>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2 ml-3 shrink-0">
          <Button variant="subtle" size="sm" onClick={() => setShowShare(true)}>
            Share
          </Button>
          <Button variant="secondary" size="sm" onClick={() => navigate('/lists')}>
            Back
          </Button>
        </div>
      </div>

      {/* Item list — unchecked */}
      <div className="flex flex-col gap-1">
        {uncheckedItems.map((item) => (
          <ListItemRow
            key={item.id}
            item={item}
            onToggle={(checked) =>
              toggleCheckMutation.mutate({ itemId: item.id, checked })
            }
            onDelete={() => deleteItemMutation.mutate(item.id)}
          />
        ))}
      </div>

      {/* Add item input */}
      <div className="relative mt-3 mb-4">
        <div className="flex items-center gap-2">
          <div className="relative flex-1">
            <input
              ref={addInputRef}
              type="text"
              value={addInput}
              onChange={(e) => handleAddInputChange(e.target.value)}
              onKeyDown={handleAddKeyDown}
              onFocus={() => {
                if (addInput.trim().length >= 2 && suggestions.length > 0) {
                  setShowSuggestions(true)
                }
              }}
              onBlur={() => {
                // Delay to allow click on suggestion
                setTimeout(() => setShowSuggestions(false), 150)
              }}
              placeholder="Add an item..."
              className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 text-body text-neutral-900 placeholder:text-neutral-400 focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand transition-colors"
            />
            {selectedProduct && (
              <span className="absolute right-3 top-1/2 -translate-y-1/2 text-small text-success-dark">
                Linked
              </span>
            )}

            {/* Autocomplete suggestions */}
            {showSuggestions && suggestions.length > 0 && (
              <div
                ref={suggestionsRef}
                className="absolute z-40 left-0 right-0 top-full mt-1 bg-white border border-neutral-200 rounded-lg shadow-subtle max-h-48 overflow-y-auto"
              >
                {suggestions.map((product, idx) => (
                  <div
                    key={product.id}
                    onMouseDown={(e) => {
                      e.preventDefault()
                      handleSelectProduct(product)
                    }}
                    onMouseEnter={() => setHighlightedIdx(idx)}
                    className={[
                      'px-3 py-2 text-caption cursor-pointer',
                      idx === highlightedIdx
                        ? 'bg-brand-subtle text-neutral-900'
                        : 'text-neutral-900',
                    ].join(' ')}
                  >
                    <span className="font-medium">{product.name}</span>
                    {product.category && (
                      <span className="ml-2 text-neutral-400">{product.category}</span>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
          <Button size="sm" onClick={addItemDirect} disabled={!addInput.trim()}>
            Add
          </Button>
        </div>
      </div>

      {/* Checked items (bottom section) */}
      {checkedItems.length > 0 && (
        <div className="mt-4">
          <p className="text-small font-semibold text-neutral-400 uppercase tracking-wide mb-2">
            Checked ({checkedItems.length})
          </p>
          <div className="flex flex-col gap-1 opacity-60">
            {checkedItems.map((item) => (
              <ListItemRow
                key={item.id}
                item={item}
                onToggle={(checked) =>
                  toggleCheckMutation.mutate({ itemId: item.id, checked })
                }
                onDelete={() => deleteItemMutation.mutate(item.id)}
              />
            ))}
          </div>
        </div>
      )}

      {/* Footer — estimated total for unchecked */}
      {uncheckedItems.length > 0 && estimatedTotal > 0 && (
        <div className="mt-6 pt-4 border-t border-neutral-200">
          <div className="flex items-center justify-between">
            <span className="text-body-medium font-medium text-neutral-900">Estimated Total</span>
            <span className="font-display text-subhead font-bold text-brand">
              ${estimatedTotal.toFixed(2)}
            </span>
          </div>
          <p className="text-small text-neutral-400 mt-1">
            Based on {uncheckedItems.filter((i) => i.estimated_price).length} of {uncheckedItems.length} items with price data
          </p>
        </div>
      )}

      {/* Share Modal */}
      <ShareListModal
        open={showShare}
        onClose={() => setShowShare(false)}
        listId={listId}
        listName={list.name}
      />
    </div>
  )
}

// --- List Item Row Component ---

interface ListItemRowProps {
  item: ListItemWithPrice
  onToggle: (checked: boolean) => void
  onDelete: () => void
}

function ListItemRow({ item, onToggle, onDelete }: ListItemRowProps) {
  const [swiping, setSwiping] = useState(false)
  const [swipeX, setSwipeX] = useState(0)
  const touchStartRef = useRef<{ x: number; y: number } | null>(null)

  function handleTouchStart(e: React.TouchEvent) {
    const touch = e.touches[0]
    if (!touch) return
    touchStartRef.current = { x: touch.clientX, y: touch.clientY }
  }

  function handleTouchMove(e: React.TouchEvent) {
    if (!touchStartRef.current) return
    const touch = e.touches[0]
    if (!touch) return
    const dx = touch.clientX - touchStartRef.current.x
    const dy = Math.abs(touch.clientY - touchStartRef.current.y)

    // Only swipe horizontally
    if (dy > 30) {
      touchStartRef.current = null
      setSwiping(false)
      setSwipeX(0)
      return
    }

    // Only swipe left
    if (dx < -10) {
      setSwiping(true)
      setSwipeX(Math.min(0, dx))
    }
  }

  function handleTouchEnd() {
    if (swiping && swipeX < -80) {
      onDelete()
    }
    touchStartRef.current = null
    setSwiping(false)
    setSwipeX(0)
  }

  const qtyUnit = [
    item.quantity !== '1' ? item.quantity : null,
    item.unit,
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <div className="relative overflow-hidden rounded-xl">
      {/* Delete background (revealed on swipe) */}
      <div className="absolute inset-y-0 right-0 w-20 bg-expensive flex items-center justify-center rounded-r-xl">
        <svg className="w-5 h-5 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
        </svg>
      </div>

      {/* Item content */}
      <div
        className="relative bg-white border border-neutral-200 rounded-xl px-3 py-3 flex items-center gap-3 transition-transform"
        style={{ transform: swiping ? `translateX(${swipeX}px)` : undefined }}
        onTouchStart={handleTouchStart}
        onTouchMove={handleTouchMove}
        onTouchEnd={handleTouchEnd}
      >
        {/* Checkbox — 44px touch target for mobile accessibility */}
        <button
          type="button"
          className="shrink-0 w-11 h-11 rounded-lg flex items-center justify-center transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-brand -m-1"
          onClick={() => onToggle(!item.checked)}
          aria-label={item.checked ? `Uncheck ${item.name}` : `Check ${item.name}`}
        >
          <span
            className="w-6 h-6 rounded-lg border-2 flex items-center justify-center"
            style={{
              borderColor: item.checked ? '#149e61' : '#dedee5',
              backgroundColor: item.checked ? '#149e61' : 'transparent',
            }}
          >
            {item.checked && (
              <svg className="w-4 h-4 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={3}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
              </svg>
            )}
          </span>
        </button>

        {/* Item details */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span
              className={[
                'text-body font-medium truncate',
                item.checked ? 'line-through text-neutral-400' : 'text-neutral-900',
              ].join(' ')}
            >
              {item.name}
            </span>
            {qtyUnit && (
              <span className="text-small text-neutral-400 shrink-0">{qtyUnit}</span>
            )}
          </div>
          {/* Cheapest store indicator */}
          {item.cheapest_store && item.cheapest_price && !item.checked && (
            <p className="text-small text-success-dark mt-0.5">
              Best at {item.cheapest_store} ${item.cheapest_price}
              {item.unit ? `/${item.unit}` : ''}
            </p>
          )}
        </div>

        {/* Price */}
        <div className="shrink-0 text-right">
          {item.estimated_price && (
            <div className="flex flex-col items-end">
              <span
                className={[
                  'text-caption font-medium',
                  item.checked ? 'text-neutral-400 line-through' : 'text-neutral-900',
                ].join(' ')}
              >
                ${item.estimated_price}
              </span>
              {!item.checked && item.unit && item.cheapest_price && parseFloat(item.quantity) > 0 && (
                <span className="text-small text-neutral-400">
                  {item.quantity} {item.unit} x ${item.cheapest_price}/{item.unit}
                </span>
              )}
            </div>
          )}
        </div>

        {/* Delete button — always visible on mobile for discoverability, hover style on desktop */}
        <button
          type="button"
          className="shrink-0 w-9 h-9 flex items-center justify-center text-neutral-300 sm:text-neutral-200 hover:text-expensive rounded-lg hover:bg-neutral-50 transition-colors"
          onClick={onDelete}
          aria-label={`Delete ${item.name}`}
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
          </svg>
        </button>
      </div>
    </div>
  )
}

export default ShoppingListPage
