import { useCallback, useRef, useState } from 'react'
import { Button } from '@/components/ui/Button'

interface SpreadsheetUploadProps {
  onUpload: (file: File) => void
  isUploading: boolean
  errorMessage: string | null
}

const ACCEPTED_EXT = ['.csv', '.tsv', '.xlsx']

function SpreadsheetUpload({ onUpload, isUploading, errorMessage }: SpreadsheetUploadProps) {
  const [dragActive, setDragActive] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  const pickFile = useCallback((file: File | undefined) => {
    if (!file) return
    const lower = file.name.toLowerCase()
    if (!ACCEPTED_EXT.some((ext) => lower.endsWith(ext))) {
      alert('Unsupported file type. Upload .csv, .tsv, or .xlsx.')
      return
    }
    onUpload(file)
  }, [onUpload])

  return (
    <div className="max-w-3xl">
      <div className="bg-white rounded-2xl shadow-subtle p-8">
        <label
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
