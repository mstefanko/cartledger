import { useState, useRef, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listUnmatchedLineItems, linkListItem, type UnmatchedLineItem } from '@/api/review'
import { listProducts } from '@/api/products'
import { matchLineItem } from '@/api/matching'

function ReviewPage() {
  const queryClient = useQueryClient()

  const { data: items = [], isLoading } = useQuery({
    queryKey: ['unmatched-line-items'],
    queryFn: listUnmatchedLineItems,
  })

  if (isLoading) {
    return (
      <div className="py-8">
        <p className="text-body text-neutral-400">Loading…</p>
      </div>
    )
  }

  if (items.length === 0) {
    return (
      <div className="py-8 max-w-2xl">
        <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight mb-2">
          Review Queue
        </h1>
        <p className="text-body text-neutral-400">All caught up — no unmatched line items.</p>
      </div>
    )
  }

  return (
    <div className="py-8 max-w-2xl">
      <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight mb-6">
        Review Queue
      </h1>
      <div className="space-y-4">
        {items.map((item) => (
          <ReviewCard
            key={item.id}
            item={item}
            onMatched={() => {
              void queryClient.invalidateQueries({ queryKey: ['unmatched-line-items'] })
              void queryClient.invalidateQueries({ queryKey: ['unmatched-count'] })
            }}
          />
        ))}
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
