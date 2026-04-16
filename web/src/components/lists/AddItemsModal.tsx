import { useState, useMemo, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Modal } from '@/components/ui/Modal'
import { Button } from '@/components/ui/Button'
import { bulkAddItems, listLists, getList } from '@/api/lists'
import { listProducts } from '@/api/products'
import { getBuyAgain } from '@/api/analytics'
import type {
  CreateListItemRequest,
  ProductListItem,
  BuyAgainItem,
  ShoppingListWithCounts,
  ListItemWithPrice,
} from '@/types'

interface AddItemsModalProps {
  open: boolean
  onClose: () => void
  listId: string
  listName: string
}

type Tab = 'buy-again' | 'recent' | 'all' | 'from-list'

// Internal row shape used by the selector so each tab can normalize into the
// same minimal set of fields.
interface SelectableRow {
  key: string // unique for keying React + selection set
  name: string
  product_id?: string
  product_group_id?: string
  unit?: string
  // Display-only metadata per tab.
  meta?: string // right-hand line, e.g. "$3.99 at Costco" or "Group (4 products)"
  submeta?: string // small grey line, e.g. "every 5d"
}

function AddItemsModal({ open, onClose, listId, listName }: AddItemsModalProps) {
  const queryClient = useQueryClient()
  const [tab, setTab] = useState<Tab>('buy-again')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<Record<string, SelectableRow>>({})
  const [selectedSourceListId, setSelectedSourceListId] = useState<string | null>(null)

  // Reset on open so the modal is predictable each time.
  useEffect(() => {
    if (open) {
      setTab('buy-again')
      setSearch('')
      setSelected({})
      setSelectedSourceListId(null)
    }
  }, [open])

  // Queries — all gated on `open` so we don't fetch in the background.
  const buyAgainQuery = useQuery<BuyAgainItem[]>({
    queryKey: ['buy-again'],
    queryFn: getBuyAgain,
    enabled: open && tab === 'buy-again',
  })

  const recentQuery = useQuery<ProductListItem[]>({
    queryKey: ['products', { sort: 'last_purchased_at' }],
    queryFn: () => listProducts({ sort: 'last_purchased_at' }),
    enabled: open && tab === 'recent',
  })

  const allQuery = useQuery<ProductListItem[]>({
    queryKey: ['products', { search }],
    queryFn: () => listProducts({ search }),
    enabled: open && tab === 'all' && search.trim().length >= 2,
  })

  const listsQuery = useQuery<ShoppingListWithCounts[]>({
    queryKey: ['shopping-lists'],
    queryFn: listLists,
    enabled: open && tab === 'from-list',
  })

  const sourceListQuery = useQuery({
    queryKey: ['shopping-list', selectedSourceListId],
    queryFn: () => getList(selectedSourceListId!),
    enabled: open && tab === 'from-list' && !!selectedSourceListId,
  })

  // Normalize each tab's items into SelectableRow[]. Search filter is applied
  // in-memory within Buy Again / Recent / From List tabs; the All tab lets the
  // server do the fuzzy search.
  const rows: SelectableRow[] = useMemo(() => {
    const q = search.trim().toLowerCase()

    if (tab === 'buy-again') {
      const items = buyAgainQuery.data ?? []
      return items
        .filter((it) => !q || it.product_name.toLowerCase().includes(q))
        .map((it): SelectableRow => {
          const pricePart = it.last_price ? `$${it.last_price}` : ''
          const storePart = it.last_store_name ?? it.last_store ?? ''
          const meta = [pricePart, storePart && `at ${storePart}`]
            .filter(Boolean)
            .join(' ')
          const daysSince = Math.max(0, Math.round(it.days_since_last))
          const submeta = it.urgency
            ? `${it.urgency} · ${daysSince}d since last`
            : `${daysSince}d since last`
          return {
            key: `p:${it.product_id}`,
            name: it.product_name,
            product_id: it.product_id,
            meta,
            submeta,
          }
        })
    }

    if (tab === 'recent') {
      const items = recentQuery.data ?? []
      return items
        .filter((p) => !q || p.name.toLowerCase().includes(q))
        .map((p): SelectableRow => {
          const meta = p.last_price ? `$${p.last_price}` : ''
          const submeta = p.last_purchased_at
            ? `last ${daysAgo(p.last_purchased_at)}`
            : p.category ?? ''
          return {
            key: `p:${p.id}`,
            name: p.name,
            product_id: p.id,
            unit: p.default_unit ?? undefined,
            meta,
            submeta,
          }
        })
    }

    if (tab === 'all') {
      const items = allQuery.data ?? []
      return items.map((p): SelectableRow => ({
        key: `p:${p.id}`,
        name: p.name,
        product_id: p.id,
        unit: p.default_unit ?? undefined,
        meta: p.last_price ? `$${p.last_price}` : '',
        submeta: p.category ?? '',
      }))
    }

    // from-list
    if (selectedSourceListId && sourceListQuery.data) {
      return sourceListQuery.data.items
        .filter((it: ListItemWithPrice) => !q || it.name.toLowerCase().includes(q))
        .map((it: ListItemWithPrice): SelectableRow => ({
          // Distinct per source item so picking from two lists doesn't dedupe.
          key: `src:${it.id}`,
          name: it.name,
          product_id: it.product_id ?? undefined,
          product_group_id: it.product_group_id ?? undefined,
          unit: it.unit ?? undefined,
          meta: it.estimated_price ? `$${it.estimated_price}` : '',
          submeta: it.product_group_name ? `Group: ${it.product_group_name}` : '',
        }))
    }
    return []
  }, [tab, search, buyAgainQuery.data, recentQuery.data, allQuery.data, sourceListQuery.data, selectedSourceListId])

  const toggleRow = (row: SelectableRow) => {
    setSelected((prev) => {
      const next = { ...prev }
      if (next[row.key]) delete next[row.key]
      else next[row.key] = row
      return next
    })
  }

  const selectedCount = Object.keys(selected).length

  const bulkMutation = useMutation({
    mutationFn: () => {
      const items: CreateListItemRequest[] = Object.values(selected).map((r) => ({
        name: r.name,
        product_id: r.product_id,
        product_group_id: r.product_group_id,
        unit: r.unit,
      }))
      return bulkAddItems(listId, items)
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
      onClose()
    },
  })

  // Derived loading / error / empty signals per tab.
  const tabLoading =
    (tab === 'buy-again' && buyAgainQuery.isLoading) ||
    (tab === 'recent' && recentQuery.isLoading) ||
    (tab === 'all' && allQuery.isLoading) ||
    (tab === 'from-list' && !!selectedSourceListId && sourceListQuery.isLoading)

  // Empty states per plan spec.
  const renderEmpty = () => {
    if (tab === 'buy-again' || tab === 'recent') {
      return (
        <div className="text-center py-10 text-body text-neutral-400">
          <p>Scan a receipt to start tracking what you buy.</p>
        </div>
      )
    }
    if (tab === 'all') {
      if (search.trim().length < 2) {
        return (
          <div className="text-center py-10 text-body text-neutral-400">
            Type 2+ characters to search all products.
          </div>
        )
      }
      return (
        <div className="text-center py-10 text-body text-neutral-400">
          No products match "{search.trim()}".
        </div>
      )
    }
    // from-list
    if (!selectedSourceListId) {
      return null
    }
    return (
      <div className="text-center py-10 text-body text-neutral-400">
        This list has no items.
      </div>
    )
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`Add items to "${listName}"`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={bulkMutation.isPending}>
            Cancel
          </Button>
          <Button
            onClick={() => bulkMutation.mutate()}
            disabled={selectedCount === 0 || bulkMutation.isPending}
          >
            {bulkMutation.isPending
              ? 'Adding…'
              : selectedCount === 0
                ? 'Add items'
                : `Add ${selectedCount} item${selectedCount === 1 ? '' : 's'}`}
          </Button>
        </>
      }
    >
      {/* Search */}
      <input
        type="text"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        placeholder={
          tab === 'all'
            ? 'Search all products…'
            : tab === 'from-list'
              ? 'Filter items…'
              : 'Filter…'
        }
        className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-body text-neutral-900 placeholder:text-neutral-400 focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand transition-colors"
      />

      {/* Tabs */}
      <div className="flex items-center gap-1 mt-3 border-b border-neutral-200">
        {(
          [
            ['buy-again', 'Buy Again'],
            ['recent', 'Recent'],
            ['all', 'All'],
            ['from-list', 'From List'],
          ] as [Tab, string][]
        ).map(([key, label]) => (
          <button
            key={key}
            type="button"
            onClick={() => {
              setTab(key)
              // Selection persists across tabs — users may curate from multiple.
            }}
            className={[
              'px-3 py-2 text-caption font-medium border-b-2 -mb-px transition-colors',
              tab === key
                ? 'text-brand border-brand'
                : 'text-neutral-400 border-transparent hover:text-neutral-900',
            ].join(' ')}
          >
            {label}
          </button>
        ))}
      </div>

      {/* From-list source picker */}
      {tab === 'from-list' && (
        <div className="mt-3">
          <label className="block text-small font-medium text-neutral-900 mb-1">
            Copy from list
          </label>
          <select
            value={selectedSourceListId ?? ''}
            onChange={(e) => setSelectedSourceListId(e.target.value || null)}
            className="w-full px-3 py-2 rounded-xl border border-neutral-200 text-body bg-white text-neutral-900 focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand transition-colors"
          >
            <option value="">Select a list…</option>
            {(listsQuery.data ?? [])
              .filter((l) => l.id !== listId)
              .map((l) => (
                <option key={l.id} value={l.id}>
                  {l.name} ({l.item_count})
                </option>
              ))}
          </select>
        </div>
      )}

      {/* Row list */}
      <div className="mt-3 max-h-80 overflow-y-auto border border-neutral-200 rounded-xl">
        {tabLoading ? (
          <div className="text-center py-10 text-body text-neutral-400">Loading…</div>
        ) : rows.length === 0 ? (
          renderEmpty()
        ) : (
          <ul className="divide-y divide-neutral-100">
            {rows.map((row) => {
              const isChecked = !!selected[row.key]
              return (
                <li key={row.key}>
                  <label className="flex items-center gap-3 px-3 py-2 cursor-pointer hover:bg-neutral-50">
                    <input
                      type="checkbox"
                      checked={isChecked}
                      onChange={() => toggleRow(row)}
                      className="w-4 h-4 accent-brand cursor-pointer"
                    />
                    <div className="flex-1 min-w-0">
                      <div className="text-body font-medium text-neutral-900 truncate">
                        {row.name}
                      </div>
                      {row.submeta && (
                        <div className="text-small text-neutral-400 truncate">{row.submeta}</div>
                      )}
                    </div>
                    {row.meta && (
                      <div className="text-caption text-success-dark shrink-0">{row.meta}</div>
                    )}
                  </label>
                </li>
              )
            })}
          </ul>
        )}
      </div>

      {bulkMutation.isError && (
        <p className="mt-2 text-small text-expensive" role="alert">
          Failed to add items. Please try again.
        </p>
      )}
    </Modal>
  )
}

function daysAgo(iso: string): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const days = Math.floor((Date.now() - then) / (1000 * 60 * 60 * 24))
  if (days <= 0) return 'today'
  if (days === 1) return '1d ago'
  return `${days}d ago`
}

export { AddItemsModal }
export type { AddItemsModalProps }
