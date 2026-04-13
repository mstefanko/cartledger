import { NavLink } from 'react-router-dom'
import { useAuth } from '@/hooks/useAuth'
import { Button } from '@/components/ui/Button'

function DashboardPage() {
  const { user } = useAuth()

  return (
    <div className="max-w-2xl mx-auto py-12 text-center">
      <h1 className="font-display text-section font-bold text-neutral-900 tracking-tight">
        {user ? `Welcome, ${user.name}` : 'Welcome to CartLedger'}
      </h1>
      <p className="mt-3 text-body text-neutral-400">
        Track grocery prices, compare stores, and find the best deals for your household.
      </p>

      <div className="mt-8">
        <NavLink to="/scan">
          <Button size="lg">Scan Your First Receipt</Button>
        </NavLink>
      </div>

      <div className="mt-12 grid grid-cols-1 sm:grid-cols-3 gap-4 text-left">
        <div className="p-4 rounded-2xl border border-neutral-200">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Receipts</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">0</p>
        </div>
        <div className="p-4 rounded-2xl border border-neutral-200">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Products</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">0</p>
        </div>
        <div className="p-4 rounded-2xl border border-neutral-200">
          <p className="text-caption font-semibold text-neutral-400 uppercase">Stores</p>
          <p className="mt-1 font-display text-subhead font-bold text-neutral-900">0</p>
        </div>
      </div>
    </div>
  )
}

export default DashboardPage
