import { useCallback, useRef, useState } from 'react'
import { Button } from '@/components/ui/Button'
import { useListMappings, type ListedMapping } from '@/api/import-spreadsheet'

interface SpreadsheetUploadProps {
  onUpload: (file: File) => void
  isUploading: boolean
  errorMessage: string | null
}

const ACCEPTED_EXT = ['.csv', '.tsv', '.xlsx']

function SpreadsheetUpload({ onUpload, isUploading, errorMessage }: SpreadsheetUploadProps) {
  const [dragActive, setDragActive] = useState(false)
  const [hint, setHint] = useState<string | null>(null)
  const [flashedId, setFlashedId] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const dropRef = useRef<HTMLLabelElement>(null)

  // Ask the server for the household's saved mappings so we can show a
  // "Reuse:" chip rail. Fail silently — the upload flow is usable without it.
  const { data: mappingsData } = useListMappings()
  const mappings: ListedMapping[] = mappingsData?.mappings ?? []

  const pickFile = useCallback((file: File | undefined) => {
    if (!file) return
    const lower = file.name.toLowerCase()
    if (!ACCEPTED_EXT.some((ext) => lower.endsWith(ext))) {
      alert('Unsupported file type. Upload .csv, .tsv, or .xlsx.')
      return
    }
    onUpload(file)
  }, [onUpload])

  const handleChipClick = useCallback((m: ListedMapping) => {
    // The chip is informational until a file is picked — actual auto-apply
    // is driven by the server's fingerprint match on upload. Flash the chip
    // and scroll the drop-zone into focus so the user knows what to do next.
    setFlashedId(m.id)
    setHint(`Upload a file — “${m.name}” will be auto-applied if the layout matches.`)
    dropRef.current?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    // Clear the highlight after ~1.6s so it doesn't linger.
    window.setTimeout(() => setFlashedId((id) => (id === m.id ? null : id)), 1600)
  }, [])

  return (
    <div className="max-w-3xl">
      <div className="bg-white rounded-2xl shadow-subtle p-8">
        {mappings.length > 0 && (
          <div className="mb-6">
            <p className="text-small font-semibold text-neutral-400 uppercase tracking-wide mb-2">
              Reuse a saved mapping
            </p>
            <div className="flex flex-wrap gap-2">
              {mappings.map((m) => (
                <button
                  key={m.id}
                  type="button"
                  onClick={() => handleChipClick(m)}
                  className={[
                    'inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full',
                    'border text-caption transition-all cursor-pointer',
                    flashedId === m.id
                      ? 'border-brand bg-brand-subtle text-brand-dark scale-105'
                      : 'border-neutral-200 bg-white text-neutral-700 hover:border-brand hover:bg-brand-subtle',
                  ].join(' ')}
                  title={
                    m.last_used_at
                      ? `Last used ${new Date(m.last_used_at).toLocaleDateString()}`
                      : m.name
                  }
                >
                  <span className="truncate max-w-[220px]">{m.name}</span>
                  <span className="text-neutral-400 uppercase text-[10px] tracking-wide">
                    {m.source_type}
                  </span>
                </button>
              ))}
            </div>
            {hint && (
              <p className="mt-2 text-caption text-neutral-500" role="status" aria-live="polite">
                {hint}
              </p>
            )}
          </div>
        )}

        <label
          ref={dropRef}
          htmlFor="spreadsheet-file-input"
          onDragOver={(e) => {
            e.preventDefault()
            setDragActive(true)
          }}
          onDragLeave={() => setDragActive(false)}
          onDrop={(e) => {
            e.preventDefault()
            setDragActive(false)
            pickFile(e.dataTransfer.files?.[0])
          }}
          className={[
            'block border-2 border-dashed rounded-xl px-8 py-12 text-center cursor-pointer transition-colors',
            dragActive
              ? 'border-brand bg-brand-subtle'
              : 'border-neutral-200 hover:border-brand hover:bg-neutral-50',
          ].join(' ')}
        >
          <div className="font-display text-feature font-semibold text-neutral-900 mb-1">
            Drop a spreadsheet, or click to browse
          </div>
          <div className="text-caption text-neutral-500 mb-4">
            .csv, .tsv, or .xlsx — up to 10 MB
          </div>
          <Button
            type="button"
            variant="outlined"
            size="sm"
            disabled={isUploading}
            onClick={(e) => {
              e.preventDefault()
              inputRef.current?.click()
            }}
          >
            {isUploading ? 'Uploading…' : 'Choose file'}
          </Button>
          <input
            ref={inputRef}
            id="spreadsheet-file-input"
            type="file"
            className="hidden"
            accept={ACCEPTED_EXT.join(',')}
            onChange={(e) => pickFile(e.target.files?.[0] ?? undefined)}
          />
        </label>

        {errorMessage && (
          <p className="mt-4 text-caption text-expensive" role="alert">
            {errorMessage}
          </p>
        )}

        <div className="mt-6">
          <p className="text-small font-semibold text-neutral-400 uppercase tracking-wide mb-2">
            What to expect
          </p>
          <ul className="text-caption text-neutral-600 space-y-1">
            <li>Map each column to a role (date, store, item, price…)</li>
            <li>Preview receipts grouped from your rows</li>
            <li>Commit — imported receipts land alongside scanned ones</li>
          </ul>
        </div>
      </div>
    </div>
  )
}

export default SpreadsheetUpload
