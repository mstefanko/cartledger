import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { changePassword, getProfile, updateProfile, updateHousehold, deleteAllData } from '@/api/auth'
import { ApiClientError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Modal } from '@/components/ui/Modal'
import ConversionsPage from './ConversionsPage'
import IntegrationsTab from '@/components/settings/IntegrationsTab'
import BackupCard from '@/components/settings/BackupCard'
import RestoreCard, { RestoreBanner } from '@/components/settings/RestoreCard'
import ExportCard from '@/components/settings/ExportCard'

const ALL_TABS = [
  'profile',
  'household',
  'conversions',
  'integrations',
  'data',
  'danger',
] as const
type Tab = (typeof ALL_TABS)[number]

const tabLabels: Record<Tab, string> = {
  profile: 'Profile',
  household: 'Household',
  conversions: 'Unit Conversions',
  integrations: 'Integrations',
  data: 'Data',
  danger: 'Danger Zone',
}

function SettingsPage() {
  const [searchParams, setSearchParams] = useSearchParams()

  // The profile response is the only place the SPA learns `is_admin`
  // (useAuth's User state omits it). This query is already cached by
  // the ProfileTab/HouseholdTab, so reusing the same key is free.
  const { data: profile } = useQuery({
    queryKey: ['profile'],
    queryFn: getProfile,
  })
  const isAdmin = profile?.user.is_admin === true

  // Admin-gated tabs are stripped from the visible list for non-admins.
  // A non-admin who URL-hacks ?tab=data will be redirected to profile
  // because the backend already rejects their API calls — no need for
  // an inline "Forbidden" surface.
  const visibleTabs = ALL_TABS.filter((t) => (t === 'data' ? isAdmin : true))

  const rawTab = (searchParams.get('tab') as Tab) || 'profile'
  const activeTab: Tab = visibleTabs.includes(rawTab) ? rawTab : 'profile'

  function setTab(tab: Tab) {
    setSearchParams({ tab })
  }

  return (
    <div className="py-8 max-w-4xl">
      <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight mb-6">
        Settings
      </h1>

      <RestoreBanner />

      {/* Tab bar */}
      <div className="flex gap-1 border-b border-neutral-200 mb-6 flex-wrap">
        {visibleTabs.map((tab) => (
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
      {activeTab === 'data' && isAdmin && <DataTab />}
      {activeTab === 'danger' && <DangerTab />}
    </div>
  )
}

function DataTab() {
  return (
    <div className="flex flex-col gap-6 max-w-2xl">
      <BackupCard />
      <RestoreCard />
      <ExportCard />
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
    <div className="flex flex-col gap-6 max-w-lg">
      <div className="bg-white rounded-2xl shadow-subtle p-6">
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
      <ChangePasswordCard />
    </div>
  )
}

function ChangePasswordCard() {
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')

  const mutation = useMutation({
    mutationFn: () => changePassword(currentPassword, newPassword),
    onSuccess: () => {
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
    },
  })

  const mismatch = newPassword !== '' && confirmPassword !== '' && newPassword !== confirmPassword
  const errorMessage =
    mismatch
      ? 'Passwords do not match.'
      : mutation.error instanceof ApiClientError
        ? mutation.error.message
        : mutation.error
          ? 'Failed to change password.'
          : null

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-6">
      <h2 className="font-display text-feature font-semibold text-neutral-900 mb-4">
        Change Password
      </h2>
      <div className="flex flex-col gap-4">
        <Input
          label="Current Password"
          type="password"
          value={currentPassword}
          onChange={(e) => setCurrentPassword(e.target.value)}
        />
        <Input
          label="New Password"
          type="password"
          value={newPassword}
          onChange={(e) => setNewPassword(e.target.value)}
          minLength={8}
          helperText="This logs you in here. Other devices stay signed in until their session expires."
        />
        <Input
          label="Confirm New Password"
          type="password"
          value={confirmPassword}
          onChange={(e) => setConfirmPassword(e.target.value)}
          minLength={8}
        />
        {errorMessage && (
          <p className="text-small text-expensive" role="alert">
            {errorMessage}
          </p>
        )}
        {mutation.isSuccess && (
          <p className="text-small text-green-600">Password changed.</p>
        )}
        <div className="flex justify-end">
          <Button
            size="sm"
            onClick={() => mutation.mutate()}
            disabled={
              mutation.isPending ||
              !currentPassword ||
              newPassword.length < 8 ||
              newPassword !== confirmPassword
            }
          >
            {mutation.isPending ? 'Updating...' : 'Update Password'}
          </Button>
        </div>
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
