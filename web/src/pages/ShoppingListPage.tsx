import { useState, useCallback, useRef, useEffect, useMemo } from 'react'
import { useParams, useNavigate, Link, useSearchParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  getList,
  updateList,
  updateItem,
  addItem,
  deleteItem,
  bulkUpdateItems,
  bulkDeleteItems,
} from '@/api/lists'
import { listProducts } from '@/api/products'
import { fetchGroups, createGroup } from '@/api/groups'
import { listStores } from '@/api/stores'
import { useAuth } from '@/hooks/useAuth'
import { useListWebSocket } from '@/hooks/useWebSocket'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { ShareListModal } from '@/components/lists/ShareListModal'
import { StorePicker } from '@/components/lists/StorePicker'
import { ItemPriceDetail } from '@/components/lists/ItemPriceDetail'
import { AddItemsModal } from '@/components/lists/AddItemsModal'
import { StoreAssignDropdown } from '@/components/lists/StoreAssignDropdown'
import { ListTotalsBar } from '@/components/lists/ListTotalsBar'
import { BatchActionBar } from '@/components/lists/BatchActionBar'
import type {
  ListItemWithPrice,
  ShoppingListDetail,
  Product,
  ProductGroup,
  CreateListItemRequest,
  Store,
} from '@/types'

function ShoppingListPage() {
  const { id: listId } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { user } = useAuth()
  const [searchParams] = useSearchParams()
  const isCleanView = searchParams.get('share') === '1'
  const [expandedItemId, setExpandedItemId] = useState<string | null>(null)

  const [editingName, setEditingName] = useState(false)
  const [nameValue, setNameValue] = useState('')
  const [showShare, setShowShare] = useState(false)
  const [showAddItems, setShowAddItems] = useState(false)
  const [remoteIndicator, setRemoteIndicator] = useState(false)

  // Undo toast state — stacks bursts of adds into a single toast.
  const [recentlyAdded, setRecentlyAdded] = useState<{
    ids: string[]
    label: string
  } | null>(null)
  const recentlyAddedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Separate "batch-deleted" toast. Holds the full snapshot of each deleted
  // item so Undo can replay them via the existing addItem mutation.
  const [recentlyDeleted, setRecentlyDeleted] = useState<{
    items: ListItemWithPrice[]
    label: string
  } | null>(null)
  const recentlyDeletedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Multi-store mode — toggled by header button or auto-engaged when the
  // fetched list already has items assigned to >1 distinct stores.
  // v1: session-only (no localStorage); reload re-detects via useEffect below.
  const [multiStoreMode, setMultiStoreMode] = useState(false)
  const [selectedItemIds, setSelectedItemIds] = useState<Set<string>>(new Set())
  // Guard so auto-engage only fires once per page-load: if the user toggles
  // the mode off, we do NOT re-engage on subsequent list refreshes.
  const hasAutoEngagedRef = useRef(false)

  // Add item state
  const [addInput, setAddInput] = useState('')
  const [showSuggestions, setShowSuggestions] = useState(false)
  const [highlightedIdx, setHighlightedIdx] = useState(0)
  const [selectedTarget, setSelectedTarget] = useState<
    | { kind: 'product'; product: Product }
    | { kind: 'group'; group: ProductGroup }
    | null
  >(null)
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

  // Fetch stores for store picker
  const storesQuery = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  // Product search for autocomplete
  const productsQuery = useQuery({
    queryKey: ['products', { search: searchQuery }],
    queryFn: () => listProducts({ search: searchQuery }),
    enabled: searchQuery.length >= 2,
  })

  // Groups search for autocomplete
  const groupsQuery = useQuery({
    queryKey: ['product-groups', { search: searchQuery }],
    queryFn: () => fetchGroups({ search: searchQuery }),
    enabled: searchQuery.length >= 2,
  })

  const list = listQuery.data

  type SuggestionItem =
    | { kind: 'product'; item: Product }
    | { kind: 'group'; item: ProductGroup }

  const suggestions = useMemo((): SuggestionItem[] => {
    const groups = (groupsQuery.data ?? []).map((g): SuggestionItem => ({ kind: 'group', item: g }))
    const products = (productsQuery.data ?? []).map((p): SuggestionItem => ({ kind: 'product', item: p }))
    return [...groups, ...products]
  }, [groupsQuery.data, productsQuery.data])

  // Separate checked and unchecked items
  const { uncheckedItems, checkedItems } = useMemo(() => {
    if (!list) return { uncheckedItems: [], checkedItems: [] }
    const unchecked = list.items.filter((i) => !i.checked)
    const checked = list.items.filter((i) => i.checked)
    return { uncheckedItems: unchecked, checkedItems: checked }
  }, [list])

  // Estimated total for unchecked items — prefers store_price when a preferred store is set
  const estimatedTotal = useMemo(() => {
    return uncheckedItems.reduce((sum, item) => {
      const price = item.store_price ?? item.estimated_price
      if (price) return sum + parseFloat(price)
      return sum
    }, 0)
  }, [uncheckedItems])

  // Phase 4: group unchecked items by assigned_store_id for multi-store mode.
  // Unassigned bucket (storeId = null) first, then assigned buckets alphabetical
  // by store name. Checked items remain ungrouped in the bottom bucket.
  // Subtotal uses the same price source as `estimatedTotal` so the sum of all
  // section subtotals equals the grand total shown in <ListTotalsBar>.
  const groupedItems = useMemo(() => {
    if (!multiStoreMode) return []
    const buckets = new Map<
      string,
      { storeId: string | null; storeName: string; items: ListItemWithPrice[]; subtotal: number }
    >()
    // Seed Unassigned so it's present even if empty-ordered.
    const UNASSIGNED_KEY = '__unassigned__'
    for (const item of uncheckedItems) {
      const key = item.assigned_store_id ?? UNASSIGNED_KEY
      const storeId = item.assigned_store_id
      const storeName = item.assigned_store_id
        ? item.assigned_store_name ?? 'Store'
        : 'Unassigned'
      let bucket = buckets.get(key)
      if (!bucket) {
        bucket = { storeId, storeName, items: [], subtotal: 0 }
        buckets.set(key, bucket)
      }
      bucket.items.push(item)
      const price = item.store_price ?? item.estimated_price
      if (price) bucket.subtotal += parseFloat(price)
    }
    const unassigned = buckets.get(UNASSIGNED_KEY)
    const assigned = Array.from(buckets.values())
      .filter((b) => b.storeId !== null)
      .sort((a, b) => a.storeName.localeCompare(b.storeName))
    return unassigned ? [unassigned, ...assigned] : assigned
  }, [multiStoreMode, uncheckedItems])

  // Update list name mutation
  const updateNameMutation = useMutation({
    mutationFn: (name: string) => updateList(listId!, { name }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
      setEditingName(false)
    },
  })

  // Update preferred store mutation
  const updateStoreMutation = useMutation({
    mutationFn: (storeId: string | null) =>
      updateList(listId!, { preferred_store_id: storeId ?? '' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
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

  // Inline-edit title for unmatched items — commits a new name via PUT.
  const updateItemNameMutation = useMutation({
    mutationFn: ({ itemId, name }: { itemId: string; name: string }) =>
      updateItem(listId!, itemId, { name }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
    },
  })

  // Create group and immediately select it
  const createGroupMutation = useMutation({
    mutationFn: (name: string) => createGroup({ name }),
    onSuccess: (group) => {
      setSelectedTarget({ kind: 'group', group })
      void queryClient.invalidateQueries({ queryKey: ['product-groups'] })
    },
  })

  // Handle add input changes with debounced search
  const handleAddInputChange = useCallback((value: string) => {
    setAddInput(value)
    setSelectedTarget(null)
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
      if (recentlyAddedTimerRef.current) clearTimeout(recentlyAddedTimerRef.current)
      if (recentlyDeletedTimerRef.current) clearTimeout(recentlyDeletedTimerRef.current)
    }
  }, [])

  // Auto-engage multi-store mode when the list has >1 distinct assigned stores.
  // Only fires when mode is its default `false` AND we haven't auto-engaged
  // this session — so toggling off isn't fought by the next list refresh.
  useEffect(() => {
    if (!list || hasAutoEngagedRef.current || multiStoreMode) return
    const distinct = new Set<string>()
    for (const it of list.items) {
      if (it.assigned_store_id) distinct.add(it.assigned_store_id)
      if (distinct.size > 1) break
    }
    if (distinct.size > 1) {
      hasAutoEngagedRef.current = true
      setMultiStoreMode(true)
    }
  }, [list, multiStoreMode])

  // Toggle the mode button. Clears selection and latches hasAutoEngaged so
  // we don't bounce back on next list refresh.
  const toggleMultiStoreMode = useCallback(() => {
    hasAutoEngagedRef.current = true
    setMultiStoreMode((prev) => !prev)
    setSelectedItemIds(new Set())
  }, [])

  // Row checkbox / row-click toggler — add/remove the item from the selection set.
  const onToggleSelected = useCallback((id: string) => {
    setSelectedItemIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  // Chip tap in single-store mode: enter multi-store + pre-select this row.
  const enterMultiStoreWithRow = useCallback((id: string) => {
    hasAutoEngagedRef.current = true
    setMultiStoreMode(true)
    setSelectedItemIds((prev) => {
      const next = new Set(prev)
      next.add(id)
      return next
    })
  }, [])

  // Per-row store assignment from the dropdown. Server treats "" as NULL clear.
  const assignStoreMutation = useMutation({
    mutationFn: ({ itemId, storeId }: { itemId: string; storeId: string | null }) =>
      updateItem(listId!, itemId, { assigned_store_id: storeId ?? '' }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
    },
  })

  // Push a newly-added item onto the undo toast. Stacks bursts — if the toast
  // is already visible, append the id and update the label to reflect count.
  const pushRecentlyAdded = useCallback((newId: string, newName: string) => {
    if (recentlyAddedTimerRef.current) clearTimeout(recentlyAddedTimerRef.current)
    setRecentlyAdded((prev) => {
      if (prev) {
        const ids = [...prev.ids, newId]
        return { ids, label: `Added ${ids.length} items` }
      }
      return { ids: [newId], label: `Added "${newName}"` }
    })
    recentlyAddedTimerRef.current = setTimeout(() => {
      setRecentlyAdded(null)
      recentlyAddedTimerRef.current = null
    }, 3000)
  }, [])

  const handleUndoRecent = useCallback(() => {
    if (!listId || !recentlyAdded) return
    const ids = recentlyAdded.ids
    if (recentlyAddedTimerRef.current) {
      clearTimeout(recentlyAddedTimerRef.current)
      recentlyAddedTimerRef.current = null
    }
    setRecentlyAdded(null)
    // Fire-and-forget deletes in parallel; invalidate once at end.
    Promise.allSettled(ids.map((id) => deleteItem(listId, id))).then(() => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
    })
  }, [listId, recentlyAdded, queryClient])

  // --- Batch action handlers (Phase 5) ---

  // Apply assigned_store_id to every selected item via the bulk endpoint.
  // storeId === null clears (server treats "" as NULL). On success, clear
  // selection so the BatchActionBar dismisses and ListTotalsBar returns.
  const handleBatchAssignStore = useCallback(
    (storeId: string | null) => {
      if (!listId || selectedItemIds.size === 0) return
      const ids = [...selectedItemIds]
      bulkUpdateItems(listId, {
        item_ids: ids,
        patch: { assigned_store_id: storeId ?? '' },
      }).then(
        () => {
          void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
          void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
          setSelectedItemIds(new Set())
        },
        () => {
          // Error — leave selection in place so user can retry.
        },
      )
    },
    [listId, selectedItemIds, queryClient],
  )

  // Snapshot selected items, call bulk-delete, then show an undo toast with
  // enough data to reconstruct via the existing addItem mutation.
  const handleBatchDelete = useCallback(() => {
    if (!listId || !list || selectedItemIds.size === 0) return
    const ids = [...selectedItemIds]
    const snapshot = list.items.filter((it) => selectedItemIds.has(it.id))

    bulkDeleteItems(listId, ids).then(
      () => {
        void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
        void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
        setSelectedItemIds(new Set())

        // Show the batch-undo toast.
        if (recentlyDeletedTimerRef.current) clearTimeout(recentlyDeletedTimerRef.current)
        const only = snapshot[0]
        const label = snapshot.length === 1 && only
          ? `Deleted "${only.name}"`
          : `${snapshot.length} items deleted`
        setRecentlyDeleted({ items: snapshot, label })
        recentlyDeletedTimerRef.current = setTimeout(() => {
          setRecentlyDeleted(null)
          recentlyDeletedTimerRef.current = null
        }, 6000)
      },
      () => {
        // Error — leave selection in place so user can retry.
      },
    )
  }, [listId, list, selectedItemIds, queryClient])

  const handleBatchCancel = useCallback(() => {
    setSelectedItemIds(new Set())
  }, [])

  // Undo batch-delete — replay each snapshot item via the existing addItem
  // path. Findings §2.4 accepts N parallel POSTs at expected list sizes.
  const handleUndoRecentDelete = useCallback(() => {
    if (!listId || !recentlyDeleted) return
    const items = recentlyDeleted.items
    if (recentlyDeletedTimerRef.current) {
      clearTimeout(recentlyDeletedTimerRef.current)
      recentlyDeletedTimerRef.current = null
    }
    setRecentlyDeleted(null)
    Promise.allSettled(
      items.map((it) =>
        addItem(listId, {
          name: it.name,
          quantity: it.quantity,
          unit: it.unit ?? undefined,
          product_id: it.product_id ?? undefined,
          product_group_id: it.product_group_id ?? undefined,
          assigned_store_id: it.assigned_store_id ?? undefined,
        }),
      ),
    ).then(() => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
    })
  }, [listId, recentlyDeleted, queryClient])

  // addItemDirect accepts an explicit target so click/Enter-on-suggestion can
  // commit without racing setState. Called with no args, uses the current
  // input text + currently-selected target from state (the "Add" button path).
  const addItemDirect = useCallback(
    (explicitTarget?: SuggestionItem) => {
      if (!listId) return

      let name: string
      let productId: string | undefined
      let productGroupId: string | undefined
      let unit: string | undefined

      if (explicitTarget) {
        name = explicitTarget.item.name
        if (explicitTarget.kind === 'product') {
          productId = explicitTarget.item.id
          unit = explicitTarget.item.default_unit ?? undefined
        } else {
          productGroupId = explicitTarget.item.id
        }
      } else {
        name = addInput.trim()
        if (!name) return
        if (selectedTarget?.kind === 'product') {
          productId = selectedTarget.product.id
          unit = selectedTarget.product.default_unit ?? undefined
        } else if (selectedTarget?.kind === 'group') {
          productGroupId = selectedTarget.group.id
        }
      }

      const req: CreateListItemRequest = {
        name,
        product_id: productId,
        product_group_id: productGroupId,
        unit,
      }

      addItem(listId, req).then(
        (created) => {
          void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
          void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
          setAddInput('')
          setSelectedTarget(null)
          setShowSuggestions(false)
          setSearchQuery('')
          addInputRef.current?.focus()
          pushRecentlyAdded(created.id, name)
        },
        () => {
          // Error — swallow for now (existing behavior)
        },
      )
    },
    [listId, addInput, selectedTarget, queryClient, pushRecentlyAdded],
  )

  function handleSelectTarget(suggestion: SuggestionItem) {
    // Commit immediately — no priming of selectedTarget + second click needed.
    addItemDirect(suggestion)
  }

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
          // Enter on a highlighted suggestion commits it immediately.
          addItemDirect(selected)
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
            {!isCleanView && (
              <>
                <StorePicker
                  preferredStoreId={list.preferred_store_id}
                  stores={storesQuery.data ?? []}
                  onChange={(storeId) => updateStoreMutation.mutate(storeId)}
                  disabled={updateStoreMutation.isPending}
                />
                {estimatedTotal > 0 && (
                  <span className="text-caption font-medium text-brand">
                    Est. ${estimatedTotal.toFixed(2)}
                  </span>
                )}
              </>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2 ml-3 shrink-0">
          <Button variant="outlined" size="sm" onClick={() => setShowAddItems(true)}>
            Add items
          </Button>
          <Button
            variant={multiStoreMode ? 'primary' : 'outlined'}
            size="sm"
            onClick={toggleMultiStoreMode}
            aria-pressed={multiStoreMode}
            title={multiStoreMode ? 'Exit multi-store mode' : 'Enter multi-store mode'}
            aria-label="Toggle multi-store mode"
          >
            <svg
              className="w-4 h-4 sm:mr-1"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              strokeWidth={2}
              aria-hidden="true"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M3 3h2l.4 2M7 13h10l4-8H5.4M7 13L5.4 5M7 13l-2.293 2.293c-.63.63-.184 1.707.707 1.707H17m0 0a2 2 0 100 4 2 2 0 000-4zm-8 2a2 2 0 11-4 0 2 2 0 014 0z"
              />
            </svg>
            <span className="hidden sm:inline">Multi-store</span>
          </Button>
          <Button variant="subtle" size="sm" onClick={() => setShowShare(true)}>
            Share
          </Button>
          <Button variant="secondary" size="sm" onClick={() => navigate('/lists')}>
            Back
          </Button>
        </div>
      </div>

      {/* Item list — unchecked. In multi-store mode, grouped by assigned store
          with per-section subtotals. In single-store mode, flat render. */}
      {multiStoreMode ? (
        <div className="flex flex-col gap-4">
          {groupedItems.map((group) => (
            <div key={group.storeId ?? '__unassigned__'} className="flex flex-col gap-1">
              <div className="flex items-center justify-between mb-1">
                <p className="text-small font-semibold text-neutral-400 uppercase tracking-wide">
                  {group.storeName} ({group.items.length})
                </p>
                {group.subtotal > 0 && (
                  <span className="text-small font-medium text-brand">
                    Est. ${group.subtotal.toFixed(2)}
                  </span>
                )}
              </div>
              {group.items.map((item) => (
                <ListItemRow
                  key={item.id}
                  item={item}
                  onToggle={(checked) =>
                    toggleCheckMutation.mutate({ itemId: item.id, checked })
                  }
                  onDelete={() => deleteItemMutation.mutate(item.id)}
                  onRename={(name) =>
                    updateItemNameMutation.mutate({ itemId: item.id, name })
                  }
                  isExpanded={expandedItemId === item.id}
                  onToggleExpand={() => setExpandedItemId(expandedItemId === item.id ? null : item.id)}
                  isCleanView={isCleanView}
                  multiStoreMode={multiStoreMode}
                  isSelected={selectedItemIds.has(item.id)}
                  onToggleSelected={() => onToggleSelected(item.id)}
                  onEnterMultiStore={() => enterMultiStoreWithRow(item.id)}
                  stores={storesQuery.data ?? []}
                  onAssignStore={(storeId) =>
                    assignStoreMutation.mutate({ itemId: item.id, storeId })
                  }
                />
              ))}
            </div>
          ))}
        </div>
      ) : (
        <div className="flex flex-col gap-1">
          {uncheckedItems.map((item) => (
            <ListItemRow
              key={item.id}
              item={item}
              onToggle={(checked) =>
                toggleCheckMutation.mutate({ itemId: item.id, checked })
              }
              onDelete={() => deleteItemMutation.mutate(item.id)}
              onRename={(name) =>
                updateItemNameMutation.mutate({ itemId: item.id, name })
              }
              isExpanded={expandedItemId === item.id}
              onToggleExpand={() => setExpandedItemId(expandedItemId === item.id ? null : item.id)}
              isCleanView={isCleanView}
              multiStoreMode={multiStoreMode}
              isSelected={selectedItemIds.has(item.id)}
              onToggleSelected={() => onToggleSelected(item.id)}
              onEnterMultiStore={() => enterMultiStoreWithRow(item.id)}
              stores={storesQuery.data ?? []}
              onAssignStore={(storeId) =>
                assignStoreMutation.mutate({ itemId: item.id, storeId })
              }
            />
          ))}
        </div>
      )}

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
            {selectedTarget && (
              <span className="absolute right-3 top-1/2 -translate-y-1/2 text-small text-success-dark">
                {selectedTarget.kind === 'group' ? 'Group' : 'Linked'}
              </span>
            )}

            {/* Autocomplete suggestions */}
            {showSuggestions && (suggestions.length > 0 || addInput.trim().length >= 2) && (
              <div
                ref={suggestionsRef}
                className="absolute z-40 left-0 right-0 top-full mt-1 bg-white border border-neutral-200 rounded-lg shadow-subtle max-h-48 overflow-y-auto"
              >
                {suggestions.map((suggestion, idx) => (
                  <div
                    key={suggestion.item.id}
                    onMouseDown={(e) => {
                      e.preventDefault()
                      handleSelectTarget(suggestion)
                    }}
                    onMouseEnter={() => setHighlightedIdx(idx)}
                    className={[
                      'px-3 py-2 text-caption cursor-pointer flex items-center',
                      idx === highlightedIdx
                        ? 'bg-brand-subtle text-neutral-900'
                        : 'text-neutral-900',
                    ].join(' ')}
                  >
                    {suggestion.kind === 'product' ? (
                      <>
                        <span className="font-medium">{suggestion.item.name}</span>
                        {suggestion.item.category && (
                          <span className="ml-2 text-neutral-400">{suggestion.item.category}</span>
                        )}
                      </>
                    ) : (
                      <>
                        <span className="font-medium">{suggestion.item.name}</span>
                        <span className="text-xs bg-blue-100 text-blue-700 px-1.5 py-0.5 rounded ml-1">Group</span>
                      </>
                    )}
                  </div>
                ))}
                {addInput.trim().length >= 2 && !suggestions.some(
                  (s) => s.item.name.toLowerCase() === addInput.trim().toLowerCase()
                ) && (
                  <div
                    onMouseDown={(e) => {
                      e.preventDefault()
                      createGroupMutation.mutate(addInput.trim())
                      setShowSuggestions(false)
                    }}
                    className="px-3 py-2 text-caption cursor-pointer text-blue-700 hover:bg-brand-subtle"
                  >
                    + Use &ldquo;{addInput.trim()}&rdquo; as a group
                  </div>
                )}
              </div>
            )}
          </div>
          <Button size="sm" onClick={() => addItemDirect()} disabled={!addInput.trim()}>
            Add
          </Button>
        </div>

        {/* Undo toast — anchored under the input, same token palette as remoteIndicator. */}
        {recentlyAdded && (
          <div
            className="mt-2 flex items-center justify-between bg-brand-subtle text-brand text-small font-medium px-3 py-2 rounded-lg shadow-subtle"
            role="status"
            aria-live="polite"
          >
            <span className="truncate">{recentlyAdded.label}</span>
            <button
              type="button"
              onClick={handleUndoRecent}
              className="ml-3 shrink-0 font-semibold underline hover:no-underline focus:outline-none focus-visible:ring-2 focus-visible:ring-brand rounded"
            >
              Undo
            </button>
          </div>
        )}
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
                onRename={(name) =>
                  updateItemNameMutation.mutate({ itemId: item.id, name })
                }
                isExpanded={expandedItemId === item.id}
                onToggleExpand={() => setExpandedItemId(expandedItemId === item.id ? null : item.id)}
                isCleanView={isCleanView}
                multiStoreMode={multiStoreMode}
                isSelected={selectedItemIds.has(item.id)}
                onToggleSelected={() => onToggleSelected(item.id)}
                onEnterMultiStore={() => enterMultiStoreWithRow(item.id)}
                stores={storesQuery.data ?? []}
                onAssignStore={(storeId) =>
                  assignStoreMutation.mutate({ itemId: item.id, storeId })
                }
              />
            ))}
          </div>
        </div>
      )}

      {/* Footer — estimated total for unchecked (single-store only; multi-store
          mode uses <ListTotalsBar> instead per Phase 4). */}
      {!multiStoreMode && uncheckedItems.length > 0 && estimatedTotal > 0 && (
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

      {/* Sticky footer — multi-store mode only.
          When rows are selected, BatchActionBar preempts ListTotalsBar in the
          same sticky slot (Phase 5). */}
      {multiStoreMode && selectedItemIds.size > 0 && (
        <BatchActionBar
          count={selectedItemIds.size}
          stores={storesQuery.data ?? []}
          onAssignStore={handleBatchAssignStore}
          onDelete={handleBatchDelete}
          onCancel={handleBatchCancel}
        />
      )}
      {multiStoreMode && selectedItemIds.size === 0 && (
        <ListTotalsBar
          itemCount={uncheckedItems.length}
          grandTotal={estimatedTotal}
        />
      )}

      {/* Batch-delete undo toast — separate from the add-toast so the two can
          coexist; 6s timer per Phase 5 spec. */}
      {recentlyDeleted && (
        <div
          className="fixed bottom-20 left-1/2 -translate-x-1/2 z-40 max-w-md w-[calc(100%-2rem)] flex items-center justify-between bg-neutral-900 text-white text-small font-medium px-3 py-2 rounded-lg shadow-subtle"
          role="status"
          aria-live="polite"
        >
          <span className="truncate">{recentlyDeleted.label}</span>
          <button
            type="button"
            onClick={handleUndoRecentDelete}
            className="ml-3 shrink-0 font-semibold underline hover:no-underline focus:outline-none focus-visible:ring-2 focus-visible:ring-white rounded"
          >
            Undo
          </button>
        </div>
      )}

      {/* Share Modal */}
      <ShareListModal
        open={showShare}
        onClose={() => setShowShare(false)}
        listId={listId}
        listName={list.name}
      />

      {/* Bulk Add Items Modal */}
      <AddItemsModal
        open={showAddItems}
        onClose={() => setShowAddItems(false)}
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
  onRename: (name: string) => void
  isExpanded: boolean
  onToggleExpand: () => void
  isCleanView: boolean
  multiStoreMode: boolean
  isSelected: boolean
  onToggleSelected: () => void
  onEnterMultiStore: () => void
  stores: Store[]
  onAssignStore: (storeId: string | null) => void
}

function ListItemRow({
  item,
  onToggle,
  onDelete,
  onRename,
  isExpanded,
  onToggleExpand,
  isCleanView,
  multiStoreMode,
  isSelected,
  onToggleSelected,
  onEnterMultiStore,
  stores,
  onAssignStore,
}: ListItemRowProps) {
  const [swiping, setSwiping] = useState(false)
  const [swipeX, setSwipeX] = useState(0)
  const [editingTitle, setEditingTitle] = useState(false)
  const [titleValue, setTitleValue] = useState(item.name)
  const touchStartRef = useRef<{ x: number; y: number } | null>(null)

  // Derive the cheapest store's id by matching the store name on the wire
  // (no cheapest_store_id column in the API response). Defer per-store
  // price hints to a follow-up — v1 marks only the single best store.
  const cheapestStoreId = useMemo(() => {
    if (!item.cheapest_store) return null
    const match = stores.find(
      (s) => (s.nickname ?? s.name) === item.cheapest_store,
    )
    return match?.id ?? null
  }, [stores, item.cheapest_store])

  // Keep local titleValue in sync if the item name changes externally while we aren't editing.
  useEffect(() => {
    if (!editingTitle) setTitleValue(item.name)
  }, [item.name, editingTitle])

  const isUnmatched = !item.product_id && !item.product_group_id
  const canEditTitle = isUnmatched && !item.checked && !isCleanView

  function startEditTitle() {
    setTitleValue(item.name)
    setEditingTitle(true)
  }

  function commitEditTitle() {
    const trimmed = titleValue.trim()
    if (trimmed && trimmed !== item.name) {
      onRename(trimmed)
    } else {
      setTitleValue(item.name)
    }
    setEditingTitle(false)
  }

  function cancelEditTitle() {
    setTitleValue(item.name)
    setEditingTitle(false)
  }

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

  // Quantity-only pill — unit is rendered inside the price hint sub-row below.
  const qtyLabel = item.quantity !== '1' ? item.quantity : null

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
        className={[
          'relative bg-white border rounded-xl px-3 py-3 flex items-center gap-3 transition-transform',
          multiStoreMode && isSelected ? 'border-brand ring-1 ring-brand' : 'border-neutral-200',
        ].join(' ')}
        style={{ transform: swiping ? `translateX(${swipeX}px)` : undefined }}
        onTouchStart={handleTouchStart}
        onTouchMove={handleTouchMove}
        onTouchEnd={handleTouchEnd}
        onClick={multiStoreMode ? onToggleSelected : undefined}
        role={multiStoreMode ? 'button' : undefined}
        aria-pressed={multiStoreMode ? isSelected : undefined}
      >
        {/* Left control — check-off circle (single-store) OR selection checkbox (multi-store) */}
        {multiStoreMode ? (
          <button
            type="button"
            className="shrink-0 w-11 h-11 rounded-md flex items-center justify-center transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-brand -m-1"
            onClick={(e) => {
              e.stopPropagation()
              onToggleSelected()
            }}
            aria-label={isSelected ? `Deselect ${item.name}` : `Select ${item.name}`}
            aria-pressed={isSelected}
          >
            <span
              className="w-6 h-6 rounded-md border-2 flex items-center justify-center"
              style={{
                borderColor: isSelected ? '#149e61' : '#dedee5',
                backgroundColor: isSelected ? '#149e61' : 'transparent',
              }}
            >
              {isSelected && (
                <svg className="w-4 h-4 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={3}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                </svg>
              )}
            </span>
          </button>
        ) : (
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
        )}

        {/* Item details */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            {canEditTitle && editingTitle ? (
              <input
                type="text"
                value={titleValue}
                onChange={(e) => setTitleValue(e.target.value)}
                onBlur={commitEditTitle}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    commitEditTitle()
                  } else if (e.key === 'Escape') {
                    e.preventDefault()
                    cancelEditTitle()
                  }
                }}
                onClick={(e) => e.stopPropagation()}
                className="flex-1 min-w-0 text-body font-medium text-neutral-900 bg-transparent border-b-2 border-brand focus:outline-none"
                autoFocus
              />
            ) : canEditTitle ? (
              <span
                role="button"
                tabIndex={0}
                onClick={(e) => {
                  e.stopPropagation()
                  startEditTitle()
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    startEditTitle()
                  }
                }}
                title="Click to edit name"
                className="text-body font-medium truncate text-neutral-900 border-b border-dashed border-neutral-200 hover:border-neutral-400 hover:bg-neutral-50/50 transition-colors cursor-pointer"
              >
                {item.name}
              </span>
            ) : (
              <span
                className={[
                  'text-body font-medium truncate',
                  item.checked ? 'line-through text-neutral-400' : 'text-neutral-900',
                ].join(' ')}
              >
                {item.name}
              </span>
            )}
            {item.product_group_id && (
              <span className="text-xs bg-blue-100 text-blue-700 px-1.5 py-0.5 rounded ml-1">Group</span>
            )}
            {qtyLabel && (
              <span className="text-small text-neutral-400 shrink-0">{qtyLabel}</span>
            )}
          </div>
          {/* Store price indicator — hidden in clean view and in multi-store mode */}
          {!item.checked && !isCleanView && !multiStoreMode && (
            <>
              {item.store_price && item.store_price_store ? (
                <>
                  <p className="text-small text-success-dark mt-0.5">
                    At {item.store_price_store}{item.unit ? `/${item.unit}` : ''} ${item.store_price}
                  </p>
                  {item.cheapest_price && item.cheapest_store &&
                   item.store_price !== item.cheapest_price && (
                    <p className="text-small text-neutral-400 mt-0.5">
                      Best at {item.cheapest_store} ${item.cheapest_price}
                    </p>
                  )}
                </>
              ) : (
                item.cheapest_store && item.cheapest_price && (
                  <p className="text-small text-success-dark mt-0.5">
                    Best at {item.cheapest_store}{item.unit ? `/${item.unit}` : ''} ${item.cheapest_price}
                  </p>
                )
              )}
            </>
          )}
          {isExpanded && !isCleanView && (
            <ItemPriceDetail item={item} />
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
          onClick={(e) => {
            e.stopPropagation()
            onDelete()
          }}
          aria-label={`Delete ${item.name}`}
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
          </svg>
        </button>

        {/* Right-edge cell — dedicated 44x44 min size. Renders:
            - multi-store mode: the store-assignment dropdown.
            - single-store + assigned row: a store chip (replaces chevron per findings §2.6).
            - otherwise: the price-detail expand chevron. */}
        <div
          className="shrink-0 min-w-11 min-h-11 flex items-center justify-center"
          onClick={(e) => e.stopPropagation()}
        >
          {multiStoreMode && !isCleanView && !item.checked ? (
            <StoreAssignDropdown
              value={item.assigned_store_id}
              onChange={onAssignStore}
              stores={stores}
              cheapestStoreId={cheapestStoreId}
            />
          ) : !multiStoreMode && !isCleanView && !item.checked && item.assigned_store_id ? (
            <button
              type="button"
              className="inline-flex items-center px-2 py-1 rounded-lg bg-brand-subtle text-brand text-xs font-medium hover:opacity-80 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand max-w-[120px] truncate"
              onClick={(e) => {
                e.stopPropagation()
                onEnterMultiStore()
              }}
              title="Tap to edit store assignments"
              aria-label={`Assigned to ${item.assigned_store_name ?? 'store'} — tap to manage`}
            >
              <span className="truncate">{item.assigned_store_name ?? 'Store'}</span>
            </button>
          ) : (
            !isCleanView && !item.checked && (item.product_id || item.product_group_id) && (
              <button
                type="button"
                className="w-11 h-11 flex items-center justify-center text-neutral-400 hover:text-brand rounded-lg hover:bg-neutral-50 transition-colors"
                onClick={onToggleExpand}
                aria-label={isExpanded ? 'Collapse price detail' : 'Expand price detail'}
              >
                <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
                  {isExpanded ? (
                    <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
                  ) : (
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9 5l7 7-7 7" />
                  )}
                </svg>
              </button>
            )
          )}
        </div>
      </div>
    </div>
  )
}

export default ShoppingListPage
