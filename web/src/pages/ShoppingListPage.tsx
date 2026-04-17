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
  getShareText,
} from '@/api/lists'
import { listProducts } from '@/api/products'
import { fetchGroups, createGroup } from '@/api/groups'
import { listStores } from '@/api/stores'
import { useAuth } from '@/hooks/useAuth'
import { useListWebSocket } from '@/hooks/useWebSocket'
import { useListLock } from '@/hooks/useListLock'
import { on as onLockEvent, off as offLockEvent } from '@/api/lockEvents'
import { ApiClientError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { StorePicker } from '@/components/lists/StorePicker'
import { ItemPriceDetail } from '@/components/lists/ItemPriceDetail'
import { AddItemsModal } from '@/components/lists/AddItemsModal'
import { StoreAssignDropdown } from '@/components/lists/StoreAssignDropdown'
import { ListTotalsBar } from '@/components/lists/ListTotalsBar'
import { BatchActionBar } from '@/components/lists/BatchActionBar'
import { CircleCheck, CircleAlert } from 'lucide-react'
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
  const [showAddItems, setShowAddItems] = useState(false)
  const [remoteIndicator, setRemoteIndicator] = useState(false)
  const [copyMenuOpen, setCopyMenuOpen] = useState(false)
  const copyMenuRef = useRef<HTMLDivElement>(null)

  // Copy-feedback toast — mirrors the recentlyAdded/recentlyDeleted pattern.
  const [recentlyCopied, setRecentlyCopied] = useState<{ label: string } | null>(null)
  const recentlyCopiedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

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

  // Phase 7: per-list edit lock. Silent while we hold it; surfaces a banner
  // + take-over button when another user is editing.
  const {
    state: lockState,
    takeOver: takeOverLock,
  } = useListLock(listId ?? '', user?.id ?? '')

  // "Your edit session ended" toast. Fires when someone else takes over.
  const [lockEndedToast, setLockEndedToast] = useState<string | null>(null)
  const lockEndedTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // "You took over editing" toast — shown after successful takeover.
  const [tookOverToast, setTookOverToast] = useState<string | null>(null)
  const tookOverTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (!listId || !user?.id) return
    const handler = (p: { list_id: string; prior_user_id?: string }) => {
      if (p.list_id !== listId) return
      if (p.prior_user_id && p.prior_user_id === user.id) {
        if (lockEndedTimerRef.current) clearTimeout(lockEndedTimerRef.current)
        setLockEndedToast('Your edit session ended')
        lockEndedTimerRef.current = setTimeout(() => {
          setLockEndedToast(null)
          lockEndedTimerRef.current = null
        }, 4000)
      }
    }
    onLockEvent('list.lock.taken_over', handler)
    return () => {
      offLockEvent('list.lock.taken_over', handler)
      if (lockEndedTimerRef.current) {
        clearTimeout(lockEndedTimerRef.current)
        lockEndedTimerRef.current = null
      }
    }
  }, [listId, user?.id])

  // Surface a toast when a write lands a 409. The banner re-renders
  // automatically from the WS event that the new holder's acquire broadcast.
  const handleWriteError = useCallback((err: unknown) => {
    if (err instanceof ApiClientError && err.status === 409) {
      if (lockEndedTimerRef.current) clearTimeout(lockEndedTimerRef.current)
      setLockEndedToast('Someone else started editing — your change was rejected')
      lockEndedTimerRef.current = setTimeout(() => {
        setLockEndedToast(null)
        lockEndedTimerRef.current = null
      }, 4000)
    }
  }, [])

  const handleTakeOver = useCallback(async () => {
    try {
      await takeOverLock()
      if (tookOverTimerRef.current) clearTimeout(tookOverTimerRef.current)
      setTookOverToast('You took over editing')
      tookOverTimerRef.current = setTimeout(() => {
        setTookOverToast(null)
        tookOverTimerRef.current = null
      }, 3000)
    } catch {
      // swallow — server-side error surfaces via WS event on success or a
      // visible 409 banner if permissions bounced.
    }
  }, [takeOverLock])

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

  // Potential savings + optimize-CTA signal.
  //
  // canOptimize trips as soon as any item's effective store (toolbar
  // preferred_store_id in single-store mode, assigned_store_id in multi-store
  // mode) is not the cheapest known store for that item. This is independent
  // of whether we can quote a dollar saving — the user still benefits from
  // reassigning even when the current scoped store has no price on record.
  //
  // potentialSavings is a dollars-and-cents estimate for items we can actually
  // price-compare: sums (current - cheapest) for items with both store_price
  // and cheapest_price. May be 0 even when canOptimize is true (e.g. scoped
  // store has no price record → current price falls back to cheapest itself).
  const { potentialSavings, canOptimize } = useMemo(() => {
    const storeList = storesQuery.data ?? []
    const preferredId = listQuery.data?.preferred_store_id ?? null
    let saved = 0
    let canOpt = false
    for (const item of uncheckedItems) {
      if (!item.cheapest_price || !item.cheapest_store) continue
      const cheapestStore = storeList.find(
        (s) => (s.nickname ?? s.name) === item.cheapest_store,
      )
      if (!cheapestStore) continue
      const effectiveStoreId = multiStoreMode
        ? item.assigned_store_id
        : preferredId
      if (effectiveStoreId && effectiveStoreId !== cheapestStore.id) {
        canOpt = true
      }
      if (item.store_price) {
        const current = parseFloat(item.store_price)
        const cheapest = parseFloat(item.cheapest_price)
        if (cheapest < current) saved += current - cheapest
      }
    }
    return { potentialSavings: saved, canOptimize: canOpt }
  }, [uncheckedItems, storesQuery.data, listQuery.data, multiStoreMode])

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
    const UNASSIGNED_KEY = '__unassigned__'
    for (const item of uncheckedItems) {
      // Treat empty-string and null equivalently — the Go API stores NULL for
      // unassigned items, but a ?? null-coalesce would keep "" as a separate
      // bucket key, producing two "Unassigned" sections that both render with
      // the same header text.
      const rawStoreId = item.assigned_store_id
      const normalizedStoreId =
        rawStoreId && rawStoreId !== '' ? rawStoreId : null
      const key = normalizedStoreId ?? UNASSIGNED_KEY
      const storeName = normalizedStoreId
        ? item.assigned_store_name ?? 'Store'
        : 'Unassigned'
      let bucket = buckets.get(key)
      if (!bucket) {
        bucket = { storeId: normalizedStoreId, storeName, items: [], subtotal: 0 }
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
    onError: (err) => {
      handleWriteError(err)
    },
  })

  // Single-store "Shop at:" handler. In single-store mode every item has
  // assigned_store_id === preferred_store_id — so picking a new store from the
  // toolbar must patch BOTH the list's preferred_store_id AND every item's
  // assigned_store_id. Also used by the Multi→Single auto-heal path.
  const shopAtMutation = useMutation({
    mutationFn: async (storeId: string) => {
      await updateList(listId!, { preferred_store_id: storeId })
      const allItemIds = (listQuery.data?.items ?? []).map((i) => i.id)
      if (allItemIds.length > 0) {
        await bulkUpdateItems(listId!, {
          item_ids: allItemIds,
          patch: { assigned_store_id: storeId },
        })
      }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
    },
    onError: handleWriteError,
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
    onError: (err, _vars, context) => {
      // Rollback on error
      if (context?.previous) {
        queryClient.setQueryData(['shopping-list', listId], context.previous)
      }
      handleWriteError(err)
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
    onError: handleWriteError,
  })

  // Inline-edit title for unmatched items — commits a new name via PUT.
  const updateItemNameMutation = useMutation({
    mutationFn: ({ itemId, name }: { itemId: string; name: string }) =>
      updateItem(listId!, itemId, { name }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
    },
    onError: handleWriteError,
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
      if (recentlyCopiedTimerRef.current) clearTimeout(recentlyCopiedTimerRef.current)
    }
  }, [])

  // Close the copy menu on outside-click. Guard on `copyMenuOpen` so we only
  // attach the listener while the menu is actually visible.
  useEffect(() => {
    if (!copyMenuOpen) return
    const handler = (e: MouseEvent) => {
      if (copyMenuRef.current && !copyMenuRef.current.contains(e.target as Node)) {
        setCopyMenuOpen(false)
      }
    }
    document.addEventListener('click', handler)
    return () => document.removeEventListener('click', handler)
  }, [copyMenuOpen])

  // Copy share text for the list (optionally scoped to a store) into the
  // clipboard and surface a toast on success/failure. Matches the
  // recentlyAdded/recentlyDeleted toast convention.
  const showCopyToast = useCallback((label: string) => {
    if (recentlyCopiedTimerRef.current) clearTimeout(recentlyCopiedTimerRef.current)
    setRecentlyCopied({ label })
    recentlyCopiedTimerRef.current = setTimeout(() => {
      setRecentlyCopied(null)
      recentlyCopiedTimerRef.current = null
    }, 3500)
  }, [])

  const handleCopy = useCallback(
    async (storeId: string | undefined, successLabel: string) => {
      if (!listId) return
      setCopyMenuOpen(false)
      try {
        const text = await getShareText(listId, storeId)
        await navigator.clipboard.writeText(text)
        showCopyToast(`Copied ${successLabel} to clipboard`)
      } catch {
        showCopyToast('Failed to copy — check browser permissions')
      }
    },
    [listId, showCopyToast],
  )

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

  // Invariant check: in single-store mode every item must have
  // assigned_store_id === preferred_store_id. If drifted (manual API usage,
  // legacy data), log a warning but do NOT auto-heal silently — the next
  // toolbar change or mode toggle will fix it.
  useEffect(() => {
    if (!list || multiStoreMode) return
    const preferred = list.preferred_store_id
    if (!preferred) return
    const drifted = list.items.some(
      (it) => (it.assigned_store_id ?? null) !== preferred,
    )
    if (drifted) {
      // eslint-disable-next-line no-console
      console.warn(
        '[ShoppingListPage] Single-store invariant violation: items have assigned_store_id that differs from list.preferred_store_id. Next toolbar change or mode toggle will heal.',
      )
    }
  }, [list, multiStoreMode])

  // Toggle the mode button. Clears selection + any per-row expanded state so
  // the row tree re-renders cleanly in the new mode (no stale chevron/detail
  // pane left open). Latches hasAutoEngaged so we don't bounce back on the
  // next list refresh.
  //
  // Multi→Single is DESTRUCTIVE: auto-heals the single-store invariant by
  // bulk-assigning every item to the first store alphabetically (product
  // owner explicitly OK'd this).
  const toggleMultiStoreMode = useCallback(() => {
    if (multiStoreMode) {
      const sorted = [...(storesQuery.data ?? [])].sort((a, b) =>
        (a.nickname ?? a.name).localeCompare(b.nickname ?? b.name),
      )
      const firstStoreId = sorted[0]?.id
      // Edge case: 0 stores in household — just flip the mode, user must
      // create a store before any item prices will render.
      if (firstStoreId && (listQuery.data?.items.length ?? 0) > 0) {
        shopAtMutation.mutate(firstStoreId)
      }
    }
    hasAutoEngagedRef.current = true
    setMultiStoreMode((prev) => !prev)
    setSelectedItemIds(new Set())
    setExpandedItemId(null)
    setCopyMenuOpen(false)
  }, [multiStoreMode, storesQuery.data, listQuery.data, shopAtMutation])

  // Row checkbox / row-click toggler — add/remove the item from the selection set.
  const onToggleSelected = useCallback((id: string) => {
    setSelectedItemIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
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
    onError: handleWriteError,
  })

  // Optimize Price: reassign every unchecked item to its cheapest known store
  // and flip to multi-store mode. Groups items by target store id and issues
  // one bulk-update per group (the PATCH endpoint takes a single patch value
  // per call, so batching by target store is the minimum-call shape).
  // Destructive — overrides any existing assigned_store_id. Product owner OK.
  const optimizePriceMutation = useMutation({
    mutationFn: async () => {
      if (!listId) return
      const storeList = storesQuery.data ?? []
      const byTargetStore = new Map<string, string[]>()
      for (const item of uncheckedItems) {
        if (!item.cheapest_store || !item.cheapest_price) continue
        const target = storeList.find(
          (s) => (s.nickname ?? s.name) === item.cheapest_store,
        )
        if (!target) continue
        const list = byTargetStore.get(target.id) ?? []
        list.push(item.id)
        byTargetStore.set(target.id, list)
      }
      for (const [storeId, itemIds] of byTargetStore) {
        if (itemIds.length === 0) continue
        await bulkUpdateItems(listId, {
          item_ids: itemIds,
          patch: { assigned_store_id: storeId },
        })
      }
    },
    onSuccess: () => {
      hasAutoEngagedRef.current = true
      setMultiStoreMode(true)
      setSelectedItemIds(new Set())
      setExpandedItemId(null)
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
    },
    onError: handleWriteError,
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

      {/* Phase 7: lock banner — visible only when someone else is editing.
          Silent when we hold the lock per findings §6 Q4. */}
      {!lockState.isHeldByMe && lockState.holder && (
        <div
          role="status"
          aria-live="polite"
          className="mb-3 flex items-center justify-between gap-3 bg-brand-subtle border border-brand text-brand text-caption px-3 py-2 rounded-lg"
        >
          <span>
            <span className="font-semibold">
              {lockState.holder.user_name || 'Someone'}
            </span>{' '}
            is editing
          </span>
          <Button
            variant="outlined"
            size="sm"
            onClick={() => void handleTakeOver()}
          >
            Take over
          </Button>
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
          </div>
        </div>
        <div className="flex items-center gap-2 ml-3 shrink-0">
          <Button variant="outlined" size="sm" onClick={() => setShowAddItems(true)}>
            Add items
          </Button>
          {multiStoreMode ? (
            <div className="relative" ref={copyMenuRef}>
              <Button
                variant="subtle"
                size="sm"
                onClick={(e: React.MouseEvent) => {
                  e.stopPropagation()
                  setCopyMenuOpen((prev) => !prev)
                }}
                aria-haspopup="menu"
                aria-expanded={copyMenuOpen}
              >
                Copy
                <svg
                  className="w-3 h-3 ml-1"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                  strokeWidth={2}
                  aria-hidden="true"
                >
                  <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
                </svg>
              </Button>
              {copyMenuOpen && (
                <div
                  role="menu"
                  className="absolute right-0 top-full mt-1 z-40 min-w-[200px] bg-white border border-neutral-200 rounded-lg shadow-subtle overflow-hidden"
                >
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      void handleCopy(undefined, `"${list.name}"`)
                    }}
                    className="w-full px-3 py-2 text-left text-caption text-neutral-900 hover:bg-brand-subtle focus:outline-none focus:bg-brand-subtle"
                  >
                    Copy entire list
                  </button>
                  {groupedItems
                    .filter((g) => g.storeId !== null && g.items.length > 0)
                    .map((g) => (
                      <button
                        key={g.storeId}
                        type="button"
                        role="menuitem"
                        onClick={() => {
                          void handleCopy(g.storeId ?? undefined, `${g.storeName} list`)
                        }}
                        className="w-full px-3 py-2 text-left text-caption text-neutral-900 hover:bg-brand-subtle focus:outline-none focus:bg-brand-subtle border-t border-neutral-100"
                      >
                        Copy {g.storeName} ({g.items.length})
                      </button>
                    ))}
                </div>
              )}
            </div>
          ) : (
            <Button
              variant="subtle"
              size="sm"
              onClick={() => {
                void handleCopy(undefined, `"${list.name}"`)
              }}
            >
              Copy
            </Button>
          )}
        </div>
      </div>

      {/* Toolbar row — mode-dependent.
          Single-store: "Shop at:" dropdown on left, Multi-store toggle on right.
          Multi-store: Select-all control on left, "Single store" toggle on right. */}
      {!isCleanView && (
        <div className="flex items-center justify-between mb-3 gap-2">
          <div className="flex items-center gap-2 min-w-0">
            {multiStoreMode ? (
              <SelectAllToggle
                allItemIds={uncheckedItems.map((i) => i.id)}
                selected={selectedItemIds}
                onSelectAll={(ids) => setSelectedItemIds(new Set(ids))}
                onClear={() => setSelectedItemIds(new Set())}
              />
            ) : (
              <>
                <label
                  htmlFor="shop-at-picker"
                  className="text-caption font-medium text-neutral-900 shrink-0"
                >
                  Shop at:
                </label>
                <StorePicker
                  preferredStoreId={list.preferred_store_id}
                  stores={storesQuery.data ?? []}
                  onChange={(storeId) => {
                    if (storeId) shopAtMutation.mutate(storeId)
                  }}
                  disabled={shopAtMutation.isPending}
                />
              </>
            )}
          </div>
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
            <span className="hidden sm:inline">
              {multiStoreMode ? 'Single store' : 'Multi-store'}
            </span>
          </Button>
        </div>
      )}

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
                  stores={storesQuery.data ?? []}
                  onAssignStore={(storeId) =>
                    assignStoreMutation.mutate({ itemId: item.id, storeId })
                  }
                  preferredStoreId={list.preferred_store_id}
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
              stores={storesQuery.data ?? []}
              onAssignStore={(storeId) =>
                assignStoreMutation.mutate({ itemId: item.id, storeId })
              }
              preferredStoreId={list.preferred_store_id}
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
                stores={storesQuery.data ?? []}
                onAssignStore={(storeId) =>
                  assignStoreMutation.mutate({ itemId: item.id, storeId })
                }
                preferredStoreId={list.preferred_store_id}
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
          {canOptimize ? (
            <div className="flex items-center justify-between gap-2 mt-2">
              <span className="text-small text-neutral-600">
                {potentialSavings > 0
                  ? `Save $${potentialSavings.toFixed(2)} by shopping at different stores.`
                  : 'Some items are cheaper at other stores.'}
              </span>
              <button
                type="button"
                onClick={() => optimizePriceMutation.mutate()}
                disabled={optimizePriceMutation.isPending}
                className="text-small font-medium text-brand hover:underline disabled:opacity-50 disabled:cursor-not-allowed shrink-0"
              >
                {optimizePriceMutation.isPending ? 'Optimizing…' : 'Optimize price'}
              </button>
            </div>
          ) : (
            <p className="text-small text-neutral-500 mt-2">Estimate is the best possible.</p>
          )}
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
          potentialSavings={potentialSavings}
          canOptimize={canOptimize}
          onOptimize={() => optimizePriceMutation.mutate()}
          optimizing={optimizePriceMutation.isPending}
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

      {/* Phase 7: "Your edit session ended" toast. */}
      {lockEndedToast && (
        <div
          className="fixed bottom-20 left-1/2 -translate-x-1/2 z-40 max-w-md w-[calc(100%-2rem)] flex items-center justify-center bg-neutral-900 text-white text-small font-medium px-3 py-2 rounded-lg shadow-subtle"
          role="status"
          aria-live="polite"
        >
          <span className="truncate">{lockEndedToast}</span>
        </div>
      )}

      {/* Phase 7: "You took over editing" toast. */}
      {tookOverToast && (
        <div
          className="fixed bottom-20 left-1/2 -translate-x-1/2 z-40 max-w-md w-[calc(100%-2rem)] flex items-center justify-center bg-brand-subtle text-brand text-small font-medium px-3 py-2 rounded-lg shadow-subtle"
          role="status"
          aria-live="polite"
        >
          <span className="truncate">{tookOverToast}</span>
        </div>
      )}

      {/* Copy-feedback toast — matches recentlyDeleted positioning so it
          anchors above the sticky ListTotalsBar/BatchActionBar when in
          multi-store mode. */}
      {recentlyCopied && (
        <div
          className="fixed bottom-20 left-1/2 -translate-x-1/2 z-40 max-w-md w-[calc(100%-2rem)] flex items-center justify-center bg-neutral-900 text-white text-small font-medium px-3 py-2 rounded-lg shadow-subtle"
          role="status"
          aria-live="polite"
        >
          <span className="truncate">{recentlyCopied.label}</span>
        </div>
      )}

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
  stores: Store[]
  onAssignStore: (storeId: string | null) => void
  preferredStoreId: string | null
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
  stores,
  onAssignStore,
  preferredStoreId,
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
          'relative border rounded-xl px-3 py-3 flex items-center gap-3 transition-transform',
          multiStoreMode && isSelected
            ? 'border-brand ring-2 ring-brand bg-brand-subtle'
            : 'border-neutral-200 bg-white',
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
              className={[
                'w-6 h-6 rounded-md border-2 flex items-center justify-center transition-colors',
                isSelected
                  ? 'bg-brand border-brand'
                  : 'bg-white border-neutral-300',
              ].join(' ')}
              style={{
                // Hard fallback in case design tokens haven't loaded.
                borderColor: isSelected ? '#149e61' : '#dedee5',
                backgroundColor: isSelected ? '#149e61' : '#ffffff',
              }}
            >
              {isSelected && (
                <svg
                  className="w-4 h-4"
                  fill="none"
                  stroke="#ffffff"
                  viewBox="0 0 24 24"
                  strokeWidth={3}
                  aria-hidden="true"
                >
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
          {/* Best-price indicator — always frames as "Best at <store>". Icon
              reflects whether the store the row will be bought at (toolbar
              "Shop at" in single-store, per-row dropdown in multi-store) is
              the cheapest known option. Green check = yes; red alert = a
              different store is cheaper. */}
          {!item.checked && !isCleanView &&
           item.cheapest_store && item.cheapest_price && (() => {
            const effectiveStoreId = multiStoreMode
              ? item.assigned_store_id
              : preferredStoreId
            const effectiveStore = stores.find((s) => s.id === effectiveStoreId)
            const effectiveStoreName = effectiveStore
              ? effectiveStore.nickname ?? effectiveStore.name
              : null
            const isBestHere =
              effectiveStoreName !== null &&
              effectiveStoreName === item.cheapest_store
            return (
              <p
                className={[
                  'text-small mt-0.5 flex items-center gap-1',
                  isBestHere ? 'text-success-dark' : 'text-neutral-600',
                ].join(' ')}
              >
                {isBestHere ? (
                  <CircleCheck className="w-3.5 h-3.5 text-success-dark shrink-0" aria-hidden="true" />
                ) : (
                  <CircleAlert className="w-3.5 h-3.5 text-red-600 shrink-0" aria-hidden="true" />
                )}
                <span className="truncate">
                  Best at {item.cheapest_store}{item.unit ? `/${item.unit}` : ''} ${item.cheapest_price}
                </span>
              </p>
            )
          })()}
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

        {/* Right-edge area. Layout differs by mode:
            - multi-store mode: single dropdown cell replaces the whole area.
            - single-store + active row: TWO cells — a chip/pill cell (fixed width
              so rows align) + a chevron cell (44x44, empty spacer when unmatched).
            - cleanView or checked: nothing renders. */}
        {multiStoreMode && !isCleanView && !item.checked ? (
          <div
            className="shrink-0 min-w-11 min-h-11 flex items-center justify-center"
            onClick={(e) => e.stopPropagation()}
          >
            <StoreAssignDropdown
              value={item.assigned_store_id}
              onChange={onAssignStore}
              stores={stores}
              cheapestStoreId={cheapestStoreId}
            />
          </div>
        ) : !isCleanView && !item.checked ? (
          /* Single-store mode: no pills (invariant guarantees every item is
             assigned to preferred_store_id). Render only the chevron cell so
             matched rows can expand price detail; unmatched rows get a same-
             sized empty spacer so trash stays aligned. */
          <div
            className="shrink-0 w-11 h-11 flex items-center justify-center"
            onClick={(e) => e.stopPropagation()}
          >
            {(item.product_id || item.product_group_id) ? (
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
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  )
}

// --- Select-all toggle (multi-store toolbar) ---
//
// Renders a tri-state checkbox: unchecked when nothing is selected,
// indeterminate when some are, checked when all rows are selected.
// Clicking fully-selected clears; otherwise selects every visible item.

interface SelectAllToggleProps {
  allItemIds: string[]
  selected: Set<string>
  onSelectAll: (ids: string[]) => void
  onClear: () => void
}

function SelectAllToggle({
  allItemIds,
  selected,
  onSelectAll,
  onClear,
}: SelectAllToggleProps) {
  const total = allItemIds.length
  const count = selected.size
  const allSelected = total > 0 && count === total
  const someSelected = count > 0 && count < total

  // Sync the indeterminate DOM flag — React doesn't expose it as a JSX prop.
  const checkboxRef = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (checkboxRef.current) {
      checkboxRef.current.indeterminate = someSelected
    }
  }, [someSelected])

  const handleClick = () => {
    if (allSelected) {
      onClear()
    } else {
      onSelectAll(allItemIds)
    }
  }

  const label = count > 0 ? `${count} selected` : 'Select all'
  const disabled = total === 0

  return (
    <button
      type="button"
      onClick={handleClick}
      disabled={disabled}
      className="flex items-center gap-2 text-caption font-medium text-neutral-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand rounded px-1 py-0.5 disabled:opacity-40 disabled:cursor-not-allowed"
      aria-label={allSelected ? 'Deselect all items' : 'Select all items'}
    >
      <input
        ref={checkboxRef}
        type="checkbox"
        checked={allSelected}
        onChange={handleClick}
        onClick={(e) => e.stopPropagation()}
        disabled={disabled}
        className="w-4 h-4 accent-brand cursor-pointer disabled:cursor-not-allowed"
        aria-hidden="true"
        tabIndex={-1}
      />
      <span>{label}</span>
    </button>
  )
}

export default ShoppingListPage
