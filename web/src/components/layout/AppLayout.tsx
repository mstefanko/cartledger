import { useState } from 'react'
import { Outlet } from 'react-router-dom'
import { Sidebar } from '@/components/ui/Sidebar'

function AppLayout() {
  const [sidebarOpen, setSidebarOpen] = useState(false)

  return (
    <div className="flex h-screen overflow-hidden bg-white">
      <Sidebar open={sidebarOpen} onClose={() => setSidebarOpen(false)} />

      <div className="flex-1 flex flex-col min-w-0">
        {/* Mobile header with menu toggle */}
        <header className="flex items-center gap-3 px-4 py-3 border-b border-neutral-200 lg:hidden">
          <button
            type="button"
            onClick={() => setSidebarOpen(true)}
            className="p-2 -ml-2 text-neutral-500 hover:text-neutral-900 rounded-lg hover:bg-neutral-50 transition-colors"
            aria-label="Open menu"
          >
            {/* Hamburger icon */}
            <svg
              className="w-5 h-5"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              strokeWidth={2}
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </button>
          <span className="font-display text-body-medium font-semibold text-neutral-900">
            CartLedger
          </span>
        </header>

        {/* Main content area */}
        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export { AppLayout }
