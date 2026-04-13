import { NavLink } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { listStores } from '@/api/stores'

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

const pageLinks = [
  { to: '/analytics', label: 'Analytics', icon: 'chart-bar' },
  { to: '/products', label: 'Products', icon: 'cube' },
  { to: '/rules', label: 'Rules', icon: 'adjustments' },
  { to: '/receipts', label: 'Receipts', icon: 'receipt' },
  { to: '/import', label: 'Import', icon: 'upload' },
] as const

// Simple inline SVG icons to avoid an icon library dependency
function StoreIcon({ icon }: { icon: string | null }) {
  // Use the first letter of the icon or a default store emoji
  if (icon) {
    return <span className="text-base leading-none">{icon}</span>
  }
  return <span className="text-base leading-none">&#x1F3EA;</span>
}

function PageIcon({ icon }: { icon: string }) {
  const icons: Record<string, string> = {
    'chart-bar': '\u2248',
    cube: '\u25A2',
    adjustments: '\u2699',
    receipt: '\u2630',
    upload: '\u2912',
  }
  return <span className="text-sm leading-none font-mono">{icons[icon] ?? '\u2022'}</span>
}

function Sidebar({ open, onClose }: SidebarProps) {
  const storesQuery = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  const stores = storesQuery.data ?? []

  const sidebarContent = (
    <nav className="flex flex-col h-full">
      {/* Header */}
      <div className="px-4 py-5 border-b border-neutral-200">
        <NavLink to="/" className="font-display text-feature font-semibold text-neutral-900">
          CartLedger
        </NavLink>
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-4 flex flex-col gap-6">
        {/* Lists section — placeholder for future */}
        <div>
          <p className="px-3 mb-2 text-small font-semibold text-neutral-400 uppercase tracking-wide">
            Lists
          </p>
          <p className="px-3 text-small text-neutral-400">No lists yet</p>
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
            {pageLinks.map((link) => (
              <NavLink key={link.to} to={link.to} className={navLinkClass} onClick={onClose}>
                <PageIcon icon={link.icon} />
                {link.label}
              </NavLink>
            ))}
          </div>
        </div>
      </div>

      {/* Scan Receipt button */}
      <div className="p-3 border-t border-neutral-200">
        <NavLink
          to="/scan"
          className="flex items-center justify-center gap-2 w-full px-4 py-3 bg-brand text-white font-medium rounded-xl hover:bg-brand-dark transition-colors"
          onClick={onClose}
        >
          Scan Receipt
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
