import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getShareText } from '@/api/lists'
import { Modal } from '@/components/ui/Modal'
import { Button } from '@/components/ui/Button'

interface ShareListModalProps {
  open: boolean
  onClose: () => void
  listId: string
  listName: string
}

function ShareListModal({ open, onClose, listId, listName }: ShareListModalProps) {
  const [copied, setCopied] = useState(false)

  const shareTextQuery = useQuery({
    queryKey: ['shopping-list-share', listId],
    queryFn: () => getShareText(listId),
    enabled: open,
  })

  const shareText = shareTextQuery.data ?? ''

  async function handleShare() {
    if (!shareText) return

    // Use Web Share API on mobile if available
    if (navigator.share) {
      try {
        await navigator.share({
          title: listName,
          text: shareText,
        })
        onClose()
        return
      } catch {
        // User cancelled or share failed — fall through to clipboard
      }
    }

    // Fallback: copy to clipboard
    await handleCopy()
  }

  async function handleCopy() {
    if (!shareText) return
    try {
      await navigator.clipboard.writeText(shareText)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard API not available
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Share List"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="outlined" onClick={handleCopy} disabled={!shareText}>
            {copied ? 'Copied!' : 'Copy'}
          </Button>
          <Button onClick={handleShare} disabled={!shareText}>
            Share
          </Button>
        </>
      }
    >
      {shareTextQuery.isLoading ? (
        <p className="text-body text-neutral-400">Loading...</p>
      ) : shareTextQuery.isError ? (
        <p className="text-body text-expensive">Failed to load share text.</p>
      ) : (
        <pre className="whitespace-pre-wrap text-caption text-neutral-900 bg-neutral-50 rounded-xl p-4 max-h-64 overflow-y-auto font-body">
          {shareText}
        </pre>
      )}
    </Modal>
  )
}

export { ShareListModal }
export type { ShareListModalProps }
