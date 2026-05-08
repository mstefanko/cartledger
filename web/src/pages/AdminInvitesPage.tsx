import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, Copy, Mail, Plus, Send } from 'lucide-react'
import {
  createInvite,
  listInvites,
  resendInvite,
  revokeInvite,
  type InviteCreateResponse,
  type InviteListItem,
} from '@/api/invites'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Modal } from '@/components/ui/Modal'
import { ApiClientError } from '@/api/client'

const statusClass: Record<InviteListItem['status'], string> = {
  pending: 'bg-success-subtle text-success-dark',
  consumed: 'bg-neutral-50 text-neutral-600',
  revoked: 'bg-expensive-subtle text-expensive',
  expired: 'bg-neutral-50 text-neutral-400',
}

function AdminInvitesPage() {
  const queryClient = useQueryClient()
  const [modalOpen, setModalOpen] = useState(false)
  const [email, setEmail] = useState('')
  const [ttlDays, setTtlDays] = useState(7)
  const [knownURLs, setKnownURLs] = useState<Record<string, string>>({})
  const [copiedID, setCopiedID] = useState<string | null>(null)

  const invitesQuery = useQuery({
    queryKey: ['invites'],
    queryFn: listInvites,
  })

  function rememberInvite(resp: InviteCreateResponse) {
    setKnownURLs((current) => ({ ...current, [resp.id]: resp.url }))
  }

  const createMutation = useMutation({
    mutationFn: () => createInvite({ email: email.trim() || undefined, ttl_days: ttlDays }),
    onSuccess: (resp) => {
      rememberInvite(resp)
      setEmail('')
      setTtlDays(7)
      setModalOpen(false)
      void queryClient.invalidateQueries({ queryKey: ['invites'] })
    },
  })

  const revokeMutation = useMutation({
    mutationFn: revokeInvite,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['invites'] })
    },
  })

  const resendMutation = useMutation({
    mutationFn: resendInvite,
    onSuccess: (resp) => {
      rememberInvite(resp)
      void queryClient.invalidateQueries({ queryKey: ['invites'] })
    },
  })

  async function copyInvite(id: string) {
    const url = knownURLs[id]
    if (!url) return
    await navigator.clipboard.writeText(url)
    setCopiedID(id)
    window.setTimeout(() => setCopiedID(null), 2000)
  }

  const createError =
    createMutation.error instanceof ApiClientError
      ? createMutation.error.message
      : createMutation.error
        ? 'Unable to create invite.'
        : null

  const invites = invitesQuery.data ?? []

  return (
    <div className="py-8 max-w-5xl">
      <div className="flex flex-wrap items-center justify-between gap-4 mb-6">
        <div>
          <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight">
            Invites
          </h1>
          <p className="mt-1 text-caption text-neutral-500">
            Create, send, and revoke household invite links.
          </p>
        </div>
        <Button size="sm" onClick={() => setModalOpen(true)} className="gap-2">
          <Plus size={16} aria-hidden="true" />
          Create Invite
        </Button>
      </div>

      <div className="overflow-hidden rounded-2xl border border-neutral-200 bg-white shadow-subtle">
        <div className="grid grid-cols-[1.3fr_1fr_1fr_1.4fr] gap-4 px-4 py-3 border-b border-neutral-200 text-small font-semibold uppercase text-neutral-400">
          <span>Email</span>
          <span>Status</span>
          <span>Expires</span>
          <span className="text-right">Actions</span>
        </div>
        {invitesQuery.isLoading ? (
          <p className="px-4 py-6 text-body text-neutral-400">Loading...</p>
        ) : invites.length === 0 ? (
          <p className="px-4 py-6 text-body text-neutral-400">No invites yet.</p>
        ) : (
          invites.map((invite) => (
            <div
              key={invite.id}
              className="grid grid-cols-[1.3fr_1fr_1fr_1.4fr] gap-4 px-4 py-3 border-b border-neutral-200 last:border-b-0 items-center text-caption"
            >
              <span className="min-w-0 truncate text-neutral-900">{invite.email ?? 'Open invite'}</span>
              <span>
                <span className={`inline-flex rounded-full px-2 py-1 text-small font-medium ${statusClass[invite.status]}`}>
                  {invite.status}
                </span>
              </span>
              <span className="text-neutral-500">{formatDate(invite.expires_at)}</span>
              <div className="flex justify-end gap-2">
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  className="gap-1.5"
                  onClick={() => void copyInvite(invite.id)}
                  disabled={!knownURLs[invite.id]}
                  title={knownURLs[invite.id] ? 'Copy link' : 'Create or resend to copy this link'}
                >
                  <Copy size={14} aria-hidden="true" />
                  {copiedID === invite.id ? 'Copied' : 'Copy'}
                </Button>
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  className="gap-1.5"
                  onClick={() => resendMutation.mutate(invite.id)}
                  disabled={!invite.email || resendMutation.isPending}
                  title={invite.email ? 'Send email' : 'No email associated'}
                >
                  <Send size={14} aria-hidden="true" />
                  Send
                </Button>
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  className="gap-1.5 text-expensive"
                  onClick={() => revokeMutation.mutate(invite.id)}
                  disabled={invite.status !== 'pending' || revokeMutation.isPending}
                >
                  <Ban size={14} aria-hidden="true" />
                  Revoke
                </Button>
              </div>
            </div>
          ))
        )}
      </div>

      <Modal
        open={modalOpen}
        onClose={() => setModalOpen(false)}
        title="Create Invite"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="sm"
              className="gap-2"
              onClick={() => createMutation.mutate()}
              disabled={createMutation.isPending || ttlDays < 1 || ttlDays > 30}
            >
              <Mail size={14} aria-hidden="true" />
              {createMutation.isPending ? 'Creating...' : 'Create'}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4">
          <Input
            label="Email"
            type="email"
            placeholder="optional@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <Input
            label="TTL Days"
            type="number"
            min={1}
            max={30}
            value={ttlDays}
            onChange={(e) => setTtlDays(Number(e.target.value))}
          />
          {createError && (
            <p className="text-small text-expensive" role="alert">
              {createError}
            </p>
          )}
        </div>
      </Modal>
    </div>
  )
}

function formatDate(value: string): string {
  const date = new Date(value.replace(' ', 'T') + 'Z')
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}

export default AdminInvitesPage
