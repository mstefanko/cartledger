import { useState, useRef, useEffect } from 'react'
import { useMutation } from '@tanstack/react-query'
import { stageRestore } from '@/api/backup'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Modal } from '@/components/ui/Modal'
import { ApiClientError } from '@/api/client'

// localStorage key that stores a truthy flag when a restore has been
// staged. Consumed by <RestoreBanner /> at the top of SettingsPage.
export const RESTORE_STAGED_KEY = 'cartledger.restore_staged'

function RestoreCard() {
  const [file, setFile] = useState<File | null>(null)
  const [password, setPassword] = useState('')
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const mutation = useMutation({
    mutationFn: () => {
      if (!file) throw new Error('No file selected')
      return stageRestore(file, password)
    },
    onSuccess: (resp) => {
      setConfirmOpen(false)
      setError(null)
      setSuccess(resp.message || 'Restore staged.')
      window.localStorage.setItem(RESTORE_STAGED_KEY, '1')
      // Emit a storage event to sibling tabs; also fire our own custom
      // event so the banner in the same tab can react synchronously.
      window.dispatchEvent(new Event('cartledger:restore-staged'))
      setFile(null)
      setPassword('')
      if (fileInputRef.current) fileInputRef.current.value = ''
    },
    onError: (err) => {
      setConfirmOpen(false)
      if (err instanceof ApiClientError) {
        switch (err.status) {
          case 401:
            setError('Password incorrect.')
            break
          case 400:
            setError(
              err.details
                ? `Archive validation failed: ${err.details}`
                : err.message || 'Archive validation failed.',
            )
            break
          case 413:
            setError('Archive exceeds 5GB limit.')
            break
          case 507:
            setError('Not enough disk space.')
            break
          default:
            setError(err.message || 'Restore failed.')
        }
      } else {
        setError('Restore failed.')
      }
    },
  })

  const canStage = file !== null && password.trim().length > 0

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-6 border border-red-200">
      <h2 className="font-display text-feature font-semibold text-red-700 mb-2">
        Restore from backup
      </h2>
      <p className="text-body text-neutral-600 mb-4">
        This replaces <span className="font-semibold">ALL</span> current data and requires a
        server restart.
      </p>

      <div className="flex flex-col gap-4 max-w-md">
        <div className="flex flex-col gap-1.5">
          <label htmlFor="restore-file" className="text-caption font-medium text-neutral-900">
            Backup archive
          </label>
          <input
            ref={fileInputRef}
            id="restore-file"
            type="file"
            accept=".tar.gz,.tgz,application/gzip,application/x-gzip,application/x-tar"
            onChange={(e) => {
              const f = e.target.files?.[0] ?? null
              setFile(f)
              setError(null)
              setSuccess(null)
            }}
            className="block w-full text-body text-neutral-700
              file:mr-3 file:py-1.5 file:px-3 file:rounded-xl file:border-0
              file:text-caption file:font-medium
              file:bg-brand-subtle file:text-brand hover:file:opacity-80"
          />
          <p className="text-small text-neutral-400">Accepts .tar.gz or .tgz archives.</p>
        </div>

        <Input
          label="Password"
          type="password"
          value={password}
          onChange={(e) => {
            setPassword(e.target.value)
            setError(null)
          }}
          placeholder="Archive password"
        />

        {error && (
          <p className="text-small text-expensive" role="alert">
            {error}
          </p>
        )}
        {success && (
          <p className="text-small text-green-600">{success}</p>
        )}

        <div className="flex justify-end">
          <Button
            className="bg-red-600 text-white hover:bg-red-700"
            size="sm"
            disabled={!canStage || mutation.isPending}
            onClick={() => {
              setError(null)
              setSuccess(null)
              setConfirmOpen(true)
            }}
          >
            {mutation.isPending ? 'Staging...' : 'Stage restore'}
          </Button>
        </div>
      </div>

      <Modal
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        title="Are you sure?"
        footer={
          <>
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setConfirmOpen(false)}
              disabled={mutation.isPending}
            >
              Cancel
            </Button>
            <Button
              className="bg-red-600 text-white hover:bg-red-700"
              size="sm"
              onClick={() => mutation.mutate()}
              disabled={mutation.isPending}
            >
              {mutation.isPending ? 'Staging...' : 'Stage restore'}
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-600">
          This will replace the entire database the next time the server restarts. All current
          data will be lost. This action cannot be undone.
        </p>
      </Modal>
    </div>
  )
}

// Shown at the top of SettingsPage once a restore is staged. Persists
// via localStorage so the banner survives page reload (the actual
// swap happens on the next server restart; we have no in-browser way
// to know when that completes, so we leave a Dismiss affordance).
function RestoreBanner() {
  const [staged, setStaged] = useState<boolean>(() =>
    typeof window !== 'undefined' && window.localStorage.getItem(RESTORE_STAGED_KEY) === '1',
  )

  useEffect(() => {
    function handler() {
      setStaged(window.localStorage.getItem(RESTORE_STAGED_KEY) === '1')
    }
    window.addEventListener('storage', handler)
    window.addEventListener('cartledger:restore-staged', handler)
    return () => {
      window.removeEventListener('storage', handler)
      window.removeEventListener('cartledger:restore-staged', handler)
    }
  }, [])

  if (!staged) return null

  return (
    <div className="mb-4 rounded-2xl bg-amber-50 border border-amber-200 px-4 py-3 flex items-center justify-between gap-3">
      <p className="text-body text-amber-900">
        <span className="font-semibold">Restore staged</span> — restart the server to complete.
      </p>
      <button
        type="button"
        onClick={() => {
          window.localStorage.removeItem(RESTORE_STAGED_KEY)
          setStaged(false)
        }}
        className="text-caption font-medium text-amber-800 hover:text-amber-900"
      >
        Dismiss
      </button>
    </div>
  )
}

export { RestoreBanner }
export default RestoreCard
