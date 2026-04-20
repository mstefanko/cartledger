import { Button } from '@/components/ui/Button'
import type { PreviewResponse } from '@/api/import-spreadsheet'
import type { ImportConfig } from '../SpreadsheetImportTab'

interface CommitBarProps {
  preview: PreviewResponse | null
  config: ImportConfig
  needsDefaultStore: boolean
  skipErrors: boolean
  onSkipErrorsChange: (skip: boolean) => void
  saveMappingAs: string
  onSaveMappingAsChange: (name: string) => void
  onCommit: () => void
  isCommitting: boolean
  commitError: string | null
}

function CommitBar({
  preview,
  config,
  needsDefaultStore,
  skipErrors,
  onSkipErrorsChange,
  saveMappingAs,
  onSaveMappingAsChange,
  onCommit,
  isCommitting,
  commitError,
}: CommitBarProps) {
  const receiptsIncluded = preview
    ? preview.groups.filter((g) => {
        const o = config.groupOverrides[g.group_id]
        return o?.include !== false
      }).length
    : 0
  const itemsIncluded = preview?.summary.items ?? 0
  const duplicates = preview?.summary.duplicates ?? 0
  const newStores = preview?.summary.new_stores?.length ?? 0
  const errors = preview?.summary.rows_with_errors ?? 0

  const defaultStoreUnset =
    needsDefaultStore && !config.grouping.default_store_id

  const blockedByErrors = errors > 0 && !skipErrors
  const disabled =
    !preview || isCommitting || defaultStoreUnset || blockedByErrors || receiptsIncluded === 0

  let disabledReason = ''
  if (defaultStoreUnset) disabledReason = 'Pick a default store or map a Store column'
  else if (blockedByErrors) disabledReason = 'Some rows have errors — check “skip rows with errors” or fix them'
  else if (receiptsIncluded === 0) disabledReason = 'At least one receipt must be included'

  return (
    <div className="fixed bottom-0 left-0 right-0 z-20 bg-white border-t border-neutral-200 shadow-subtle">
      <div className="max-w-5xl mx-auto px-6 py-4 flex items-center gap-4 flex-wrap">
        <div className="flex flex-col min-w-0 flex-1">
          <div className="text-caption text-neutral-500">
            {duplicates > 0 && <>{duplicates} duplicate{duplicates === 1 ? '' : 's'} will be skipped · </>}
            {newStores > 0 && <>{newStores} new store{newStores === 1 ? '' : 's'} will be created · </>}
            {errors > 0 ? (
              <span className={errors > 0 && !skipErrors ? 'text-expensive' : ''}>
                {errors} row{errors === 1 ? '' : 's'} have errors
              </span>
            ) : (
              <span>All rows parsed cleanly</span>
            )}
          </div>
          {commitError && (
            <div className="text-caption text-expensive mt-1" role="alert">{commitError}</div>
          )}
        </div>

        {errors > 0 && (
          <label className="inline-flex items-center gap-2 text-caption text-neutral-700 cursor-pointer">
            <input
              type="checkbox"
              checked={skipErrors}
              onChange={(e) => onSkipErrorsChange(e.target.checked)}
              className="accent-brand"
            />
            Skip rows with errors
          </label>
        )}

        <input
          type="text"
          value={saveMappingAs}
          onChange={(e) => onSaveMappingAsChange(e.target.value)}
          placeholder="Name this mapping (optional)"
          aria-label="Name this mapping (optional)"
          className="max-w-[220px] px-3 py-2 rounded-xl border border-neutral-200 text-caption bg-white focus:outline-none focus:ring-2 focus:ring-brand"
        />

        <div className="relative" title={disabled ? disabledReason : undefined}>
          <Button
            onClick={onCommit}
            disabled={disabled}
            size="md"
          >
            {isCommitting
              ? 'Importing…'
              : `Import ${receiptsIncluded} receipt${receiptsIncluded === 1 ? '' : 's'} (${itemsIncluded} items)`}
          </Button>
        </div>
      </div>
    </div>
  )
}

export default CommitBar
