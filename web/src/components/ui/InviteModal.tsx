import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { invite } from '@/api/auth'
import { Modal } from '@/components/ui/Modal'
import { Button } from '@/components/ui/Button'

interface InviteModalProps {
  open: boolean
  onClose: () => void
}

function InviteModal({ open, onClose }: InviteModalProps) {
  const [copied, setCopied] = useState(false)

  const inviteMutation = useMutation({
    mutationFn: invite,
    onSuccess: () => {
      setCopied(false)
    },
  })

  const link = inviteMutation.data?.link ?? null
  const expiresIn = inviteMutation.data?.expires_in ?? null

  function handleGenerate() {
    inviteMutation.mutate()
  }

  async function handleCopy() {
    if (!link) return
    try {
      await navigator.clipboard.writeText(link)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Fallback: select text in a temporary input
    }
  }

  async function handleShare() {
    if (!link) return
    if (navigator.share) {
      try {
        await navigator.share({
          title: 'Join my household on CartLedger',
          url: link,
        })
      } catch {
        // User cancelled or share failed — ignore
      }
    }
  }

  function handleClose() {
    inviteMutation.reset()
    setCopied(false)
    onClose()
  }

  return (
    <Modal
      open={open}
      onClose={handleClose}
      title="Invite to Household"
      footer={
        <Button variant="secondary" size="sm" onClick={handleClose}>
          Done
        </Button>
      }
    >
      {!link ? (
        <div className="text-center py-4">
          <p className="text-body text-neutral-600 mb-4">
            Generate a link to invite someone to your household. They will be able to view and
            collaborate on your shopping lists and receipts.
          </p>
          <Button
            onClick={handleGenerate}
            disabled={inviteMutation.isPending}
          >
            {inviteMutation.isPending ? 'Generating...' : 'Generate Invite Link'}
          </Button>
          {inviteMutation.isError && (
            <p className="mt-3 text-small text-expensive">
              Failed to generate invite. Please try again.
            </p>
          )}
        </div>
      ) : (
        <div className="py-2">
          <p className="text-small font-medium text-neutral-600 mb-2">Share this link:</p>
          <div className="flex items-center gap-2">
            <input
              type="text"
              readOnly
              value={link}
              className="flex-1 px-3 py-2 rounded-lg border border-neutral-200 text-caption text-neutral-900 bg-neutral-50 focus:outline-none select-all"
              onFocus={(e) => e.target.select()}
            />
            <Button size="sm" variant="subtle" onClick={handleCopy}>
              {copied ? 'Copied!' : 'Copy'}
            </Button>
          </div>
          {typeof navigator !== 'undefined' && 'share' in navigator && (
            <Button
              size="sm"
              variant="secondary"
              className="mt-3 w-full"
              onClick={handleShare}
            >
              Share via...
            </Button>
          )}
          {expiresIn && (
            <p className="mt-3 text-small text-neutral-400">
              Expires in {expiresIn}
            </p>
          )}
        </div>
      )}
    </Modal>
  )
}

export { InviteModal }
