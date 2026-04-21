import { NavLink, useNavigate } from 'react-router-dom'
import { useState, type ComponentType } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  BarChart3,
  Package,
  Wand2,
  Receipt,
  Upload,
  Settings,
  ListChecks,
  ShoppingCart,
  Store,
  ScanLine,
  Plus,
  PencilLine,
  ClipboardCheck,
  type LucideProps,
} from 'lucide-react'
import { listStores } from '@/api/stores'
import { listLists, createList } from '@/api/lists'
import { getUnmatchedCount } from '@/api/review'
import { useHasIntegrations } from '@/hooks/useHasIntegrations'

interface SidebarProps {
  open: boolean
  onClose: () => void
}

const navLinkClass = ({ isActive }: { isActive: boolean }) =>
  [
    'flex items-center gap-2.5 px-3 py-2 rounded-lg text-caption font-medium transition-colors',
    isActive
      ? 'bg-brand-subtle text-brand'
      : 'text-neutral-500 hover:bg-neutral-50 hover:text-neutral-900',
  ].join(' ')

type IconComponent = ComponentType<LucideProps>

const pageLinks: { to: string; label: string; Icon: IconComponent }[] = [
  { to: '/analytics', label: 'Analytics', Icon: BarChart3 },
  { to: '/review', label: 'Review', Icon: ClipboardCheck },
  { to: '/products', label: 'Products', Icon: Package },
  { to: '/rules', label: 'Auto-Match', Icon: Wand2 },
  { to: '/receipts', label: 'Receipts', Icon: Receipt },
]

// Import is gated on having at least one configured+enabled integration. It's
// appended dynamically inside the component so useHasIntegrations hides it
// during load (no flash-show) and when no integration is connected.
const IMPORT_LINK: { to: string; label: string; Icon: IconComponent } = {
  to: '/import',
  label: 'Import',
  Icon: Upload,
}

const ICON_SIZE = 16

function StoreIcon({ icon }: { icon: string | null }) {
  if (icon) {
    // Legacy data may store a short string/emoji — preserve it.
    return <span className="text-base leading-none">{icon}</span>
  }
  return <Store size={ICON_SIZE} className="text-neutral-400" aria-hidden="true" />
}

function Sidebar({ open, onClose }: SidebarProps) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [creatingList, setCreatingList] = useState(false)
  const [newListName, setNewListName] = useState('')

  const storesQuery = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  const unmatchedCountQuery = useQuery({
    queryKey: ['unmatched-count'],
    queryFn: () => getUnmatchedCount(),
  })

  const unmatchedCount = unmatchedCountQuery.data?.count ?? 0

  const { hasAny: hasIntegrations, isLoading: integrationsLoading } = useHasIntegrations()
  // While loading, hide the Import link entirely so it doesn't flash in and
  // then disappear once we learn there are no configured integrations.
  const visiblePageLinks = !integrationsLoading && hasIntegrations
    ? [...pageLinks, IMPORT_LINK]
    : pageLinks

  const listsQuery = useQuery({
    queryKey: ['shopping-lists'],
    queryFn: listLists,
  })

  const createListMutation = useMutation({
    mutationFn: createList,
    onSuccess: (newList) => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
      setCreatingList(false)
      setNewListName('')
      navigate(`/lists/${newList.id}`)
      onClose()
    },
  })

  const stores = storesQuery.data ?? []
  const lists = (listsQuery.data ?? []).filter((l) => l.status === 'active')

  const sidebarContent = (
    <nav className="flex flex-col h-full">
      {/* Header */}
      <div className="px-4 py-5 border-b border-neutral-200">
        <NavLink to="/" className="font-display text-feature font-semibold text-neutral-900">
          CartLedger
        </NavLink>
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-4 flex flex-col gap-6">
        {/* Lists section */}
        <div>
          <div className="flex items-center justify-between px-3 mb-2">
            <p className="text-small font-semibold text-neutral-400 uppercase tracking-wide">
              Lists
            </p>
            <button
              type="button"
              className="text-brand hover:text-brand-dark transition-colors"
              onClick={() => setCreatingList(true)}
              aria-label="New list"
            >
              <Plus size={ICON_SIZE} />
            </button>
          </div>
          {creatingList && (
            <div className="px-3 mb-2">
              <input
                type="text"
                value={newListName}
                onChange={(e) => setNewListName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && newListName.trim()) {
                    createListMutation.mutate({ name: newListName.trim() })
                  }
                  if (e.key === 'Escape') {
                    setCreatingList(false)
                    setNewListName('')
                  }
                }}
                onBlur={() => {
                  if (!newListName.trim()) {
                    setCreatingList(false)
                  }
                }}
                placeholder="List name..."
                className="w-full px-2 py-1 text-caption rounded-lg border border-neutral-200 focus:outline-none focus:ring-1 focus:ring-brand"
                autoFocus
              />
            </div>
          )}
          <div className="flex flex-col gap-0.5">
            <NavLink
              to="/lists"
              className={navLinkClass}
              onClick={onClose}
              end
            >
              <ListChecks size={ICON_SIZE} aria-hidden="true" />
              All Lists
            </NavLink>
            {lists.map((list) => (
              <NavLink
                key={list.id}
                to={`/lists/${list.id}`}
                className={navLinkClass}
                onClick={onClose}
              >
                <ShoppingCart size={ICON_SIZE} aria-hidden="true" />
                <span className="truncate flex-1">{list.name}</span>
                <span className="text-small text-neutral-400 shrink-0">
                  {list.checked_count}/{list.item_count}
                </span>
              </NavLink>
            ))}
            {lists.length === 0 && !creatingList && (
              <p className="px-3 text-small text-neutral-400">No active lists</p>
            )}
          </div>
        </div>

        {/* Stores section */}
        <div>
          <p className="px-3 mb-2 text-small font-semibold text-neutral-400 uppercase tracking-wide">
            Stores
          </p>
          <div className="flex flex-col gap-0.5">
            {stores.map((store) => (
              <NavLink
                key={store.id}
                to={`/stores/${store.id}`}
                className={navLinkClass}
                onClick={onClose}
              >
                <StoreIcon icon={store.icon} />
                {store.name}
              </NavLink>
            ))}
            {stores.length === 0 && (
              <p className="px-3 text-small text-neutral-400">No stores yet</p>
            )}
          </div>
        </div>

        {/* Pages section */}
        <div>
          <p className="px-3 mb-2 text-small font-semibold text-neutral-400 uppercase tracking-wide">
            Pages
          </p>
          <div className="flex flex-col gap-0.5">
            {visiblePageLinks.map((link) => (
              <NavLink key={link.to} to={link.to} className={navLinkClass} onClick={onClose}>
                <link.Icon size={ICON_SIZE} aria-hidden="true" />
                <span className="flex-1">{link.label}</span>
                {link.to === '/review' && unmatchedCount > 0 && (
                  <span className="text-small text-neutral-400 shrink-0">{unmatchedCount}</span>
                )}
              </NavLink>
            ))}
          </div>
        </div>

        {/* Settings */}
        <div>
          <div className="flex flex-col gap-0.5">
            <NavLink to="/settings" className={navLinkClass} onClick={onClose}>
              <Settings size={ICON_SIZE} aria-hidden="true" />
              Settings
            </NavLink>
          </div>
        </div>
      </div>

      {/* Scan Receipt button + New receipt secondary link */}
      <div className="p-3 border-t border-neutral-200 flex flex-col gap-2">
        <NavLink
          to="/scan"
          className="flex items-center justify-center gap-2 w-full px-4 py-3 bg-brand text-white font-medium rounded-xl hover:bg-brand-dark transition-colors"
          onClick={onClose}
        >
          <ScanLine size={ICON_SIZE} aria-hidden="true" />
          Scan Receipt
        </NavLink>
        <NavLink
          to="/receipts/new"
          className={({ isActive }) =>
            [
              'flex items-center justify-center gap-2 w-full px-4 py-2 rounded-xl text-caption font-medium transition-colors',
              isActive
                ? 'bg-neutral-100 text-neutral-900'
                : 'text-neutral-700 hover:bg-neutral-100',
            ].join(' ')
          }
          onClick={onClose}
        >
          <PencilLine size={ICON_SIZE} aria-hidden="true" />
          New receipt
        </NavLink>
      </div>

    </nav>
  )

  return (
    <>
      {/* Mobile overlay */}
      {open && (
        <div
          className="fixed inset-0 z-40 bg-black/40 lg:hidden"
          onClick={onClose}
          aria-hidden="true"
        />
      )}

      {/* Sidebar — mobile: slide-in overlay, desktop: fixed */}
      <aside
        className={[
          'fixed top-0 left-0 z-50 h-full w-64 bg-white border-r border-neutral-200',
          'transform transition-transform duration-200 ease-in-out',
          'lg:translate-x-0 lg:static lg:z-auto',
          open ? 'translate-x-0' : '-translate-x-full',
        ].join(' ')}
      >
        {sidebarContent}
      </aside>
    </>
  )
}

export { Sidebar }
export type { SidebarProps }
