import { useState, useRef, useEffect } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  listUnmatchedLineItems,
  linkListItem,
  getBatchHeader,
  type UnmatchedLineItem,
  type ImportBatchHeader,
} from '@/api/review'
import { listProducts } from '@/api/products'
import { matchLineItem } from '@/api/matching'
import { useDuplicateCandidates } from '@/api/products-duplicates'
import DuplicatePairsList from '@/pages/review/DuplicatePairsList'

type ReviewTab = 'items' | 'dupes'

function ReviewPage() {
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()

  // Batch-scoped review lane: /review?batch=<id>. Empty means the global
  // lane (P1–P7 behavior, unchanged). Only applies to the "items" tab —
  // duplicate candidates are catalog-wide, so the batch filter is
  // deliberately ignored when the dupes tab is active.
  const batchId = searchParams.get('batch') ?? ''
  const tabParam = searchParams.get('tab')
  const activeTab: ReviewTab = tabParam === 'dupes' ? 'dupes' : 'items'

  const setActiveTab = (next: ReviewTab) => {
    const params = new URLSearchParams(searchParams)
    if (next === 'items') {
      params.delete('tab')
    } else {
      params.set('tab', next)
    }
    setSearchParams(params, { replace: true })
  }

  // Dupes count pill. Fetched lazily — only once the user first views the
  // tab or we need the count for the header pill. We keep it enabled on
  // mount so the pill is accurate; the react-query cache means this is a
  // single request per session unless mutations invalidate it.
  const { data: dupesData } = useDuplicateCandidates({})
  const dupesCount = dupesData?.count ?? 0

  const {
    data: items = [],
    isLoading,
    isError: itemsError,
  } = useQuery({
    queryKey: ['unmatched-line-items', batchId],
    queryFn: () => listUnmatchedLineItems(batchId || undefined),
    retry: false,
  })

  // Header query runs only when a batch is present. An invalid or deleted
  // batch yields 404 from the server; we surface that as an explicit error
  // state instead of silently rendering "All caught up".
  const {
    data: batchHeader,
    isError: batchHeaderError,
  } = useQuery<ImportBatchHeader>({
    queryKey: ['import-batch-header', batchId],
    queryFn: () => getBatchHeader(batchId),
    enabled: batchId !== '',
    retry: false,
  })

  // Invalid-batch state: we have a ?batch= param but either the header or
  // the list came back in error. Don't fall through to the empty list —
  // the user needs to know the link is stale.
  const invalidBatch = batchId !== '' && (batchHeaderError || itemsError)

  const clearBatch = () => {
    const next = new URLSearchParams(searchParams)
    next.delete('batch')
    setSearchParams(next, { replace: true })
  }

  const onMatched = () => {
    void queryClient.invalidateQueries({ queryKey: ['unmatched-line-items'] })
    void queryClient.invalidateQueries({ queryKey: ['unmatched-count'] })
    if (batchId) {
      void queryClient.invalidateQueries({ queryKey: ['import-batch-header', batchId] })
    }
  }

  // Chrome (batch strip + tab bar) stays identical whether the items tab is
  // loading, empty, or populated. Inner body swaps based on activeTab.
  const tabBar = (
    <ReviewTabBar
      activeTab={activeTab}
      onSelect={setActiveTab}
      itemsCount={items.length}
      dupesCount={dupesCount}
    />
  )

  const header =
    batchId && batchHeader && activeTab === 'items' ? (
      <BatchHeaderStrip header={batchHeader} onDone={clearBatch} />
    ) : (
      <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight mb-4">
        Review Queue
      </h1>
    )

  // Dupes tab ignores batch filter entirely (catalog-wide catalogue cleanup
  // doesn't slice by import).
  if (activeTab === 'dupes') {
    return (
      <div className="py-8 max-w-3xl">
        {header}
        {tabBar}
        <DuplicatePairsList />
      </div>
    )
  }

  if (invalidBatch) {
    return (
      <div className="py-8 max-w-2xl">
        <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight mb-4">
          Review Queue
        </h1>
        {tabBar}
        <div className="mt-6 rounded-2xl bg-white shadow-subtle p-5">
          <p className="font-display text-feature font-semibold text-neutral-900 mb-1">
            That import link isn’t available
          </p>
          <p className="text-body text-neutral-500 mb-3">
            This batch may have been deleted or the link is out of date.
          </p>
          <button
            onClick={clearBatch}
            className="text-caption text-brand hover:underline"
          >
            Back to global review →
          </button>
        </div>
      </div>
    )
  }

  if (isLoading) {
    return (
      <div className="py-8">
        {header}
        {tabBar}
        <p className="text-body text-neutral-400">Loading…</p>
      </div>
    )
  }

  if (items.length === 0) {
    return (
      <div className="py-8 max-w-2xl">
        {header}
        {tabBar}
        {batchId && batchHeader ? (
          <div className="mt-6 rounded-2xl bg-white shadow-subtle p-5">
            <p className="font-display text-feature font-semibold text-neutral-900 mb-1">
              Import fully reviewed
            </p>
            <p className="text-body text-neutral-500 mb-3">
              Every line item in this import has been matched or linked.
            </p>
            <div className="flex flex-wrap gap-2">
              <Link to="/analytics" className="text-caption text-brand hover:underline">
                View analytics →
              </Link>
              <button
                onClick={clearBatch}
                className="text-caption text-brand hover:underline"
              >
                Back to global review →
              </button>
            </div>
          </div>
        ) : (
          <p className="text-body text-neutral-400">All caught up — no unmatched line items.</p>
        )}
      </div>
    )
  }

  return (
    <div className="py-8 max-w-2xl">
      {header}
      {tabBar}
      <div className="space-y-4">
        {items.map((item) => (
          <ReviewCard
            key={item.id}
            item={item}
            onMatched={onMatched}
          />
        ))}
      </div>
    </div>
  )
}

interface ReviewTabBarProps {
  activeTab: ReviewTab
  onSelect: (t: ReviewTab) => void
  itemsCount: number
  dupesCount: number
}

// Tab bar styled to match the app's rounded-pill visual language (see
// ImportPage). Only two lanes: line items (default) and duplicate products.
function ReviewTabBar({ activeTab, onSelect, itemsCount, dupesCount }: ReviewTabBarProps) {
  const tabs: { key: ReviewTab; label: string; count: number }[] = [
    { key: 'items', label: 'Line items', count: itemsCount },
    { key: 'dupes', label: 'Duplicate products', count: dupesCount },
  ]
  return (
    <div role="tablist" className="mb-6 flex gap-1 rounded-xl bg-neutral-50 p-1 w-fit">
      {tabs.map((t) => {
        const isActive = t.key === activeTab
        return (
          <button
            key={t.key}
            role="tab"
            aria-selected={isActive}
            type="button"
            onClick={() => onSelect(t.key)}
            className={[
              'rounded-lg px-3 py-1.5 text-caption font-medium transition-colors',
              isActive
                ? 'bg-white text-neutral-900 shadow-subtle'
                : 'text-neutral-500 hover:text-neutral-900',
            ].join(' ')}
          >
            {t.label}
            <span className="ml-1.5 text-neutral-400">({t.count})</span>
          </button>
        )
      })}
    </div>
  )
}

interface BatchHeaderStripProps {
  header: ImportBatchHeader
  onDone: () => void
}

function BatchHeaderStrip({ header, onDone }: BatchHeaderStripProps) {
  // Date rendering — fall back to the raw ISO string if Date() can't parse
  // the server timestamp; don't let a formatter error blank the strip.
  let dateDisplay = header.created_at
  try {
    const d = new Date(header.created_at)
    if (!Number.isNaN(d.getTime())) {
      dateDisplay = d.toLocaleDateString(undefined, {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
      })
    }
  } catch {
    // keep raw value
  }
  const filename = header.filename || 'Untitled import'
  const remaining = header.unmatched_count
  const remainingLabel = `${remaining} item${remaining === 1 ? '' : 's'} remaining`

  return (
    <div className="mb-6 rounded-2xl border border-brand/20 bg-brand-subtle p-4">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="text-small uppercase tracking-wide text-brand-dark/70">
            Reviewing import
          </div>
          <div className="mt-1 truncate font-display text-feature font-semibold text-neutral-900">
            {filename}
          </div>
          <div className="mt-1 text-caption text-neutral-500">
            {dateDisplay} · {remainingLabel}
          </div>
        </div>
        <button
          type="button"
          onClick={onDone}
          className="shrink-0 rounded-xl bg-white px-3 py-1.5 text-caption font-medium text-brand-dark border border-brand-dark hover:bg-brand-subtle active:bg-brand-subtle"
        >
          Done
        </button>
      </div>
    </div>
  )
}

interface ReviewCardProps {
  item: UnmatchedLineItem
  onMatched: () => void
}

function ReviewCard({ item, onMatched }: ReviewCardProps) {
  const [inputValue, setInputValue] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [showDropdown, setShowDropdown] = useState(false)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [])

  const { data: products = [], isFetching } = useQuery({
    queryKey: ['products', debouncedSearch],
    queryFn: () => listProducts({ search: debouncedSearch }),
    enabled: debouncedSearch.length > 0,
  })

  const queryClient = useQueryClient()

  const matchMutation = useMutation({
    mutationFn: (productId: string) => matchLineItem(item.id, { product_id: productId }),
    onSuccess: onMatched,
  })

  const linkMutation = useMutation({
    mutationFn: (listItemId: string) => linkListItem(item.id, { list_item_id: listItemId }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['unmatched-line-items'] })
      void queryClient.invalidateQueries({ queryKey: ['unmatched-count'] })
      void queryClient.invalidateQueries({ queryKey: ['import-batch-header'] })
    },
  })

  const handleInputChange = (val: string) => {
    setInputValue(val)
    setShowDropdown(true)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => setDebouncedSearch(val), 200)
  }

  const unitPrice = item.unit_price ? `$${parseFloat(item.unit_price).toFixed(2)}` : null
  const qty = parseFloat(item.quantity)
  const qtyDisplay = item.unit ? `${qty} ${item.unit}` : `${qty}`
  const priceDisplay = unitPrice ? `${qtyDisplay} × ${unitPrice}` : qtyDisplay

  // "Searching…" while debounce is pending or fetch is in-flight
  const isPending = debouncedSearch !== inputValue || isFetching
  const hasResults = products.length > 0
  const noMatch = !isPending && debouncedSearch.length > 0 && !hasResults

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-5">
      <div className="flex items-start justify-between gap-4">
        <div className="flex-1 min-w-0">
          <p className="text-body-medium font-semibold text-neutral-900 truncate">{item.raw_text}</p>
          <div className="flex items-center gap-3 mt-1 text-caption text-neutral-400">
            <span>{item.receipt_date}</span>
            {item.store_name && <span>{item.store_name}</span>}
            <span>{priceDisplay}</span>
          </div>
          <Link
            to={`/receipts/${item.receipt_id}`}
            className="mt-1 inline-block text-caption text-brand hover:underline"
          >
            Edit on receipt →
          </Link>
          {item.possible_list_items && item.possible_list_items.length > 0 && (
            <div className="mt-2 rounded border border-neutral-200 bg-neutral-50 p-2 text-xs space-y-1">
              {item.possible_list_items.map((match) => (
                <div key={match.item_id} className="flex items-center justify-between gap-2">
                  <span className="text-neutral-700">
                    Matches <span className="font-medium">"{match.item_name}"</span> on {match.list_name}
                  </span>
                  <button
                    onClick={() => linkMutation.mutate(match.item_id)}
                    disabled={linkMutation.isPending}
                    className="shrink-0 rounded px-2 py-0.5 text-xs font-medium bg-brand text-white hover:bg-brand/80 disabled:opacity-50"
                  >
                    Link & check off
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="min-w-[200px]">
          <div className="relative">
            <input
              type="text"
              value={inputValue}
              onChange={(e) => handleInputChange(e.target.value)}
              onFocus={() => { if (inputValue.length > 0) setShowDropdown(true) }}
              onBlur={() => setTimeout(() => setShowDropdown(false), 150)}
              placeholder="Search product…"
              className="w-full px-3 py-2 text-caption border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
              disabled={matchMutation.isPending}
            />
            {showDropdown && inputValue.length > 0 && (
              <div className="absolute z-10 left-0 right-0 top-full mt-1 bg-white border border-neutral-200 rounded-xl shadow-subtle max-h-48 overflow-y-auto">
                {isPending ? (
                  <div className="px-3 py-2 text-caption text-neutral-400">Searching…</div>
                ) : hasResults ? (
                  products.map((p) => (
                    <button
                      key={p.id}
                      type="button"
                      className="w-full text-left px-3 py-2 text-caption text-neutral-900 hover:bg-neutral-50 transition-colors cursor-pointer"
                      onMouseDown={() => {
                        matchMutation.mutate(p.id)
                        setInputValue('')
                        setDebouncedSearch('')
                        setShowDropdown(false)
                      }}
                    >
                      {p.name}
                    </button>
                  ))
                ) : noMatch ? (
                  <div className="px-3 py-2 text-caption text-neutral-500">
                    No match —{' '}
                    <Link
                      to={`/receipts/${item.receipt_id}`}
                      className="text-brand hover:underline"
                    >
                      edit on receipt →
                    </Link>
                  </div>
                ) : null}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}

export default ReviewPage
