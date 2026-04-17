import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getProfile, updateProfile, updateHousehold, deleteAllData } from '@/api/auth'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Modal } from '@/components/ui/Modal'
import { InviteModal } from '@/components/ui/InviteModal'
import ConversionsPage from './ConversionsPage'
import IntegrationsTab from '@/components/settings/IntegrationsTab'

const TABS = ['profile', 'household', 'conversions', 'integrations', 'invite', 'danger'] as const
type Tab = (typeof TABS)[number]

const tabLabels: Record<Tab, string> = {
  profile: 'Profile',
  household: 'Household',
  conversions: 'Unit Conversions',
  integrations: 'Integrations',
  invite: 'Invite Members',
  danger: 'Danger Zone',
}

function SettingsPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = (searchParams.get('tab') as Tab) || 'profile'

  function setTab(tab: Tab) {
    setSearchParams({ tab })
  }

  return (
    <div className="py-8 max-w-4xl">
      <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight mb-6">
        Settings
      </h1>

      {/* Tab bar */}
      <div className="flex gap-1 border-b border-neutral-200 mb-6 flex-wrap">
        {TABS.map((tab) => (
          <button
            key={tab}
            type="button"
            onClick={() => setTab(tab)}
            className={[
              'px-4 py-2.5 text-caption font-medium whitespace-nowrap transition-colors -mb-px border-b-2',
              activeTab === tab
                ? 'border-brand text-brand'
                : 'border-transparent text-neutral-500 hover:text-neutral-900',
              tab === 'danger' ? 'ml-auto text-red-500 hover:text-red-700' : '',
            ].join(' ')}
          >
            {tabLabels[tab]}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {activeTab === 'profile' && <ProfileTab />}
      {activeTab === 'household' && <HouseholdTab />}
      {activeTab === 'conversions' && <ConversionsPage />}
      {activeTab === 'integrations' && <IntegrationsTab />}
      {activeTab === 'invite' && <InviteTab />}
      {activeTab === 'danger' && <DangerTab />}
    </div>
  )
}

function ProfileTab() {
  const queryClient = useQueryClient()
  const { data: profile, isLoading } = useQuery({
    queryKey: ['profile'],
    queryFn: getProfile,
  })

  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [initialized, setInitialized] = useState(false)

  if (profile && !initialized) {
    setName(profile.user.name)
    setEmail(profile.user.email)
    setInitialized(true)
  }

  const mutation = useMutation({
    mutationFn: updateProfile,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['profile'] })
    },
  })

  if (isLoading) return <p className="text-body text-neutral-400">Loading...</p>

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-6 max-w-lg">
      <h2 className="font-display text-feature font-semibold text-neutral-900 mb-4">
        Your Profile
      </h2>
      <div className="flex flex-col gap-4">
        <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} />
        <Input label="Email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        <div className="flex justify-end">
          <Button
            size="sm"
            onClick={() => mutation.mutate({ name, email })}
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Saving...' : 'Save Changes'}
          </Button>
        </div>
        {mutation.isSuccess && (
          <p className="text-small text-green-600">Profile updated.</p>
        )}
        {mutation.isError && (
          <p className="text-small text-expensive">Failed to update profile.</p>
        )}
      </div>
    </div>
  )
}

function HouseholdTab() {
  const queryClient = useQueryClient()
  const { data: profile, isLoading } = useQuery({
    queryKey: ['profile'],
    queryFn: getProfile,
  })

  const [householdName, setHouseholdName] = useState('')
  const [initialized, setInitialized] = useState(false)

  if (profile && !initialized) {
    setHouseholdName(profile.household_name)
    setInitialized(true)
  }

  const mutation = useMutation({
    mutationFn: updateHousehold,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['profile'] })
    },
  })

  if (isLoading) return <p className="text-body text-neutral-400">Loading...</p>

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-6 max-w-lg">
      <h2 className="font-display text-feature font-semibold text-neutral-900 mb-4">
        Household
      </h2>
      <div className="flex flex-col gap-4">
        <Input
          label="Household Name"
          value={householdName}
          onChange={(e) => setHouseholdName(e.target.value)}
        />
        <div className="flex justify-end">
          <Button
            size="sm"
            onClick={() => mutation.mutate({ name: householdName })}
            disabled={mutation.isPending || !householdName.trim()}
          >
            {mutation.isPending ? 'Saving...' : 'Save Changes'}
          </Button>
        </div>
        {mutation.isSuccess && (
          <p className="text-small text-green-600">Household updated.</p>
        )}
      </div>
    </div>
  )
}

function InviteTab() {
  const [showInvite, setShowInvite] = useState(false)

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-6 max-w-lg">
      <h2 className="font-display text-feature font-semibold text-neutral-900 mb-2">
        Invite Members
      </h2>
      <p className="text-body text-neutral-500 mb-4">
        Generate an invite link to add someone to your household. They will be able to view and
        collaborate on your shopping lists and receipts.
      </p>
      <Button onClick={() => setShowInvite(true)}>Generate Invite Link</Button>
      <InviteModal open={showInvite} onClose={() => setShowInvite(false)} />
    </div>
  )
}

function DangerTab() {
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [confirmText, setConfirmText] = useState('')

  const mutation = useMutation({
    mutationFn: deleteAllData,
    onSuccess: () => {
      setConfirmOpen(false)
      setConfirmText('')
      void queryClient.invalidateQueries()
    },
  })

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-6 max-w-lg border border-red-200">
      <h2 className="font-display text-feature font-semibold text-red-700 mb-2">
        Danger Zone
      </h2>
      <p className="text-body text-neutral-600 mb-4">
        Permanently delete all your household data including receipts, products, stores,
        shopping lists, and matching rules. Your user account and household will remain intact.
      </p>
      <Button
        className="bg-red-600 text-white hover:bg-red-700"
        size="sm"
        onClick={() => setConfirmOpen(true)}
      >
        Delete All Data
      </Button>

      <Modal
        open={confirmOpen}
        onClose={() => { setConfirmOpen(false); setConfirmText('') }}
        title="Delete All Data"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => { setConfirmOpen(false); setConfirmText('') }}>
              Cancel
            </Button>
            <Button
              className="bg-red-600 text-white hover:bg-red-700"
              size="sm"
              onClick={() => mutation.mutate()}
              disabled={confirmText !== 'DELETE' || mutation.isPending}
            >
              {mutation.isPending ? 'Deleting...' : 'Delete Everything'}
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-600 mb-4">
          This will permanently delete <span className="font-semibold">all</span> receipts,
          products, stores, shopping lists, matching rules, and price history.
          This action cannot be undone.
        </p>
        <Input
          label='Type "DELETE" to confirm'
          value={confirmText}
          onChange={(e) => setConfirmText(e.target.value)}
          placeholder="DELETE"
        />
      </Modal>
    </div>
  )
}

export default SettingsPage
