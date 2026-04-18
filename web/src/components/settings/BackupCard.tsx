import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  listBackups,
  createBackup,
  deleteBackup,
  downloadBackupURL,
  type Backup,
} from '@/api/backup'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { ApiClientError } from '@/api/client'

// Default BACKUP_RETAIN_COUNT from internal/config/config.go:182 is 5.
// There is no /api/v1/config endpoint yet, so we hardcode the copy.
const RETAIN_COUNT = 5

function BackupCard() {
  const queryClient = useQueryClient()
  const [toast, setToast] = useState<{ kind: 'success' | 'error'; text: string } | null>(null)
  const [pendingDelete, setPendingDelete] = useState<Backup | null>(null)

  const { data, isLoading } = useQuery({
    queryKey: ['backups'],
    queryFn: listBackups,
    refetchInterval: (query) => {
      const rows = query.state.data as Backup[] | undefined
      const hasRunning = rows?.some((b) => b.status === 'running') ?? false
      return hasRunning ? 2000 : false
    },
  })

  const createMutation = useMutation({
    mutationFn: createBackup,
    onSuccess: () => {
      setToast({ kind: 'success', text: 'Backup started.' })
      void queryClient.invalidateQueries({ queryKey: ['backups'] })
    },
    onError: (err) => {
      if (err instanceof ApiClientError) {
        if (err.status === 409) {
          setToast({ kind: 'error', text: 'A backup is already running.' })
          return
        }
        if (err.status === 507) {
          setToast({ kind: 'error', text: 'Not enough disk space to create backup.' })
          return
        }
        setToast({ kind: 'error', text: err.message || 'Failed to start backup.' })
        return
      }
      setToast({ kind: 'error', text: 'Failed to start backup.' })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteBackup(id),
    onSuccess: () => {
      setPendingDelete(null)
      setToast({ kind: 'success', text: 'Backup deleted.' })
      void queryClient.invalidateQueries({ queryKey: ['backups'] })
    },
    onError: (err) => {
      const msg = err instanceof ApiClientError ? err.message : 'Failed to delete backup.'
      setToast({ kind: 'error', text: msg })
    },
  })

  const rows = data ?? []
  const anyRunning = rows.some((b) => b.status === 'running')

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-6">
      <div className="flex items-start justify-between gap-4 mb-4">
        <div>
          <h2 className="font-display text-feature font-semibold text-neutral-900">
            Backups
          </h2>
          <p className="text-body text-neutral-500 mt-1">
            Download a complete archive of your data.
          </p>
        </div>
        <Button
          size="sm"
          onClick={() => {
            setToast(null)
            createMutation.mutate()
          }}
          disabled={anyRunning || createMutation.isPending}
        >
          {anyRunning || createMutation.isPending ? 'Working...' : 'Create backup'}
        </Button>
      </div>

      {toast && (
        <p
          className={[
            'text-small mb-3',
            toast.kind === 'success' ? 'text-green-600' : 'text-expensive',
          ].join(' ')}
        >
          {toast.text}
        </p>
      )}

      {isLoading ? (
        <p className="text-body text-neutral-400">Loading backups...</p>
      ) : rows.length === 0 ? (
        <p className="text-body text-neutral-400">No backups yet.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-caption">
            <thead>
              <tr className="text-left text-neutral-500 border-b border-neutral-100">
                <th className="font-medium py-2 pr-3">Created</th>
                <th className="font-medium py-2 pr-3">Size</th>
                <th className="font-medium py-2 pr-3">Status</th>
                <th className="font-medium py-2 pr-3">Notes</th>
                <th className="font-medium py-2 pr-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((b) => (
                <tr key={b.id} className="border-b border-neutral-50 last:border-b-0">
                  <td className="py-2 pr-3 text-neutral-900" title={b.created_at}>
                    {formatRelative(b.created_at)}
                  </td>
                  <td className="py-2 pr-3 text-neutral-700">
                    {formatBytes(b.size_bytes)}
                  </td>
                  <td className="py-2 pr-3">
                    <StatusPill status={b.status} />
                  </td>
                  <td className="py-2 pr-3 text-neutral-500">
                    {b.missing_images > 0 && (
                      <span
                        title={`${b.missing_images} image files weren't on disk at backup time — they were pruned by retention`}
                        className="text-small"
                      >
                        {b.missing_images} missing image
                        {b.missing_images === 1 ? '' : 's'}
                      </span>
                    )}
                    {b.status === 'failed' && b.error && (
                      <span className="text-small text-expensive" title={b.error}>
                        {b.error.length > 40 ? `${b.error.slice(0, 40)}...` : b.error}
                      </span>
                    )}
                  </td>
                  <td className="py-2 pr-3 text-right whitespace-nowrap">
                    {b.status === 'complete' ? (
                      <a
                        href={downloadBackupURL(b.id)}
                        download
                        className="text-brand hover:text-brand-dark text-caption font-medium mr-3"
                      >
                        Download
                      </a>
                    ) : null}
                    <button
                      type="button"
                      className="text-red-600 hover:text-red-700 text-caption font-medium disabled:opacity-50 disabled:cursor-not-allowed"
                      onClick={() => setPendingDelete(b)}
                      disabled={b.status === 'running'}
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <p className="text-small text-neutral-400 mt-4">
        Keeping the {RETAIN_COUNT} most recent backups.
      </p>
      <p className="text-small text-neutral-400 mt-1">
        Encrypt archives with <code className="text-neutral-500">age</code> or{' '}
        <code className="text-neutral-500">gpg</code> before uploading to untrusted storage.
        See <span className="text-neutral-500">docs/ops/backup.md</span> for details.
      </p>

      <Modal
        open={pendingDelete !== null}
        onClose={() => setPendingDelete(null)}
        title="Delete backup?"
        footer={
          <>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setPendingDelete(null)}
              disabled={deleteMutation.isPending}
            >
              Cancel
            </Button>
            <Button
              className="bg-red-600 text-white hover:bg-red-700"
              size="sm"
              onClick={() => pendingDelete && deleteMutation.mutate(pendingDelete.id)}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-600">
          {pendingDelete
            ? `Delete backup from ${formatAbsolute(pendingDelete.created_at)}? This cannot be undone.`
            : ''}
        </p>
      </Modal>
    </div>
  )
}

function StatusPill({ status }: { status: Backup['status'] }) {
  const base = 'text-small font-medium px-2 py-0.5 rounded-full inline-block'
  if (status === 'running') {
    return (
      <span className={`${base} bg-amber-50 text-amber-700`}>
        <span className="inline-block w-1.5 h-1.5 mr-1.5 rounded-full bg-amber-500 animate-pulse" />
        Running
      </span>
    )
  }
  if (status === 'failed') {
    return <span className={`${base} bg-red-50 text-red-700`}>Failed</span>
  }
  return <span className={`${base} bg-green-50 text-green-700`}>Complete</span>
}

function formatBytes(bytes: number | null): string {
  if (bytes === null || bytes === undefined) return '—'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${units[unit]}`
}

function formatRelative(iso: string): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return iso
  const diffMs = Date.now() - then
  const sec = Math.round(diffMs / 1000)
  if (sec < 60) return 'just now'
  const min = Math.round(sec / 60)
  if (min < 60) return `${min} min${min === 1 ? '' : 's'} ago`
  const hr = Math.round(min / 60)
  if (hr < 24) return `${hr} hour${hr === 1 ? '' : 's'} ago`
  const day = Math.round(hr / 24)
  if (day < 30) return `${day} day${day === 1 ? '' : 's'} ago`
  return formatAbsolute(iso)
}

function formatAbsolute(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

export default BackupCard
