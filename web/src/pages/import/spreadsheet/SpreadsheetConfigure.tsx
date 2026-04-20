import { useEffect, useMemo, useRef, useState } from 'react'
import type {
  PreviewResponse,
  UploadResponse,
} from '@/api/import-spreadsheet'
import type { ImportConfig } from './SpreadsheetImportTab'
import SheetPicker from './panels/SheetPicker'
import CsvOptionsPanel from './panels/CsvOptionsPanel'
import DateFormatSelect from './panels/DateFormatSelect'
import IncrementalDateFilter from './panels/IncrementalDateFilter'
import FieldMappings from './panels/FieldMappings'
import DefaultStorePicker from './panels/DefaultStorePicker'
import UnitPriceOptionsPanel from './panels/UnitPriceOptionsPanel'
import GroupingPanel from './panels/GroupingPanel'
import AIAssistButton from './panels/AIAssistButton'
import ReceiptGroupList from './preview/ReceiptGroupList'
import CommitBar from './preview/CommitBar'

interface SpreadsheetConfigureProps {
  upload: UploadResponse
  config: ImportConfig
  onConfigChange: (patch: Partial<ImportConfig>) => void
  onConfigChanged: (next: ImportConfig) => void
  preview: PreviewResponse | null
  isPreviewLoading: boolean
  onCommit: () => void
  isCommitting: boolean
  commitError: string | null
  onStartOver: () => void
}

function SpreadsheetConfigure({
  upload,
  config,
  onConfigChange,
  onConfigChanged,
  preview,
  isPreviewLoading,
  onCommit,
  isCommitting,
  commitError,
  onStartOver,
}: SpreadsheetConfigureProps) {
  // Debounced preview fetch — 300ms after last change.
  const firstRun = useRef(true)
  const [skipErrors, setSkipErrors] = useState(false)
  useEffect(() => {
    const id = setTimeout(() => {
      onConfigChanged(config)
    }, firstRun.current ? 0 : 300)
    firstRun.current = false
    return () => clearTimeout(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    config.sheet,
    JSON.stringify(config.mapping),
    config.dateFormat,
    JSON.stringify(config.csvOptions),
    JSON.stringify(config.unitOptions),
    JSON.stringify(config.grouping),
    config.sinceDate,
    JSON.stringify(config.skipRowIndices),
  ])

  const activeSheet = useMemo(
    () => upload.sheets.find((s) => s.name === config.sheet) ?? upload.sheets[0],
    [upload.sheets, config.sheet],
  )

  const storeMapped = config.mapping['store'] !== undefined && config.mapping['store'] >= 0
  const usingDefaultStore = !storeMapped
  const needsDefaultStore = usingDefaultStore

  return (
    <div className="max-w-5xl">
      {/* Header row */}
      <div className="flex items-start justify-between gap-4 mb-6">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-caption text-neutral-500">
            <span className="truncate">{upload.sheets.length} sheet{upload.sheets.length === 1 ? '' : 's'}</span>
            <span>·</span>
            <span className="truncate">{activeSheet?.row_count ?? 0} rows</span>
            <span>·</span>
            <span className="truncate">fingerprint {upload.fingerprint.slice(0, 8)}</span>
          </div>
          {upload.auto_applied_mapping_id && (
            <div className="mt-1 text-caption text-brand">
              Auto-applied your saved mapping for this layout.
            </div>
          )}
        </div>
        <button
          type="button"
          onClick={onStartOver}
          className="text-caption text-neutral-500 hover:text-neutral-900 cursor-pointer"
        >
          Upload a different file
        </button>
      </div>

      {/* Config panels */}
      <div className="bg-white rounded-2xl shadow-subtle p-6 space-y-6">
        {upload.sheets.length > 1 && (
          <SheetPicker
            sheets={upload.sheets}
            selected={config.sheet}
            onSelect={(name) => onConfigChange({ sheet: name })}
          />
        )}

        <CsvOptionsPanel
          disabled={/* only shown for CSV — hide for xlsx */ false}
          hidden={!isCSV(upload)}
          value={config.csvOptions}
          onChange={(csvOptions) => onConfigChange({ csvOptions })}
        />

        <DateFormatSelect
          value={config.dateFormat}
          onChange={(dateFormat) => onConfigChange({ dateFormat })}
        />

        <IncrementalDateFilter
          value={config.sinceDate}
          onChange={(sinceDate) => onConfigChange({ sinceDate })}
        />

        <FieldMappings
          sheet={activeSheet}
          mapping={config.mapping}
          usingDefaultStore={usingDefaultStore}
          onMappingChange={(mapping) => onConfigChange({ mapping })}
          onUseDefaultStore={() => {
            const next = { ...config.mapping }
            delete next['store']
            onConfigChange({ mapping: next })
          }}
        />

        {usingDefaultStore && (
          <DefaultStorePicker
            value={config.grouping.default_store_id}
            onChange={(id) =>
              onConfigChange({
                grouping: { ...config.grouping, default_store_id: id },
              })
            }
          />
        )}

        <UnitPriceOptionsPanel
          value={config.unitOptions}
          onChange={(unitOptions) => onConfigChange({ unitOptions })}
        />

        <GroupingPanel
          value={config.grouping}
          onChange={(grouping) =>
            onConfigChange({
              grouping: {
                ...grouping,
                default_store_id: config.grouping.default_store_id,
              },
            })
          }
        />

        <AIAssistButton />
      </div>

      {/* Preview */}
      <div className="mt-6">
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-display text-feature font-semibold text-neutral-900">Preview</h2>
          <div className="text-small text-neutral-400" aria-live="polite">
            {isPreviewLoading ? 'Refreshing…' : preview ? `${preview.groups.length} receipt groups` : 'Waiting for data…'}
          </div>
        </div>
        <ReceiptGroupList
          preview={preview}
          config={config}
          onToggleInclude={(groupId, include) => {
            const next = { ...config.groupOverrides }
            const prev = next[groupId] ?? {}
            next[groupId] = { ...prev, include }
            onConfigChange({ groupOverrides: next })
          }}
          onMarkDuplicate={(groupId, isDuplicate) => {
            const next = { ...config.groupOverrides }
            const prev = next[groupId] ?? {}
            next[groupId] = { ...prev, isDuplicate }
            onConfigChange({ groupOverrides: next })
          }}
          onSkipRow={(rowIdx) => {
            const set = new Set(config.skipRowIndices)
            if (set.has(rowIdx)) set.delete(rowIdx)
            else set.add(rowIdx)
            onConfigChange({ skipRowIndices: Array.from(set).sort((a, b) => a - b) })
          }}
        />
      </div>

      <CommitBar
        preview={preview}
        config={config}
        needsDefaultStore={needsDefaultStore}
        skipErrors={skipErrors}
        onSkipErrorsChange={setSkipErrors}
        onSaveMappingAsChange={(name) => onConfigChange({ saveMappingAs: name })}
        saveMappingAs={config.saveMappingAs ?? ''}
        onCommit={onCommit}
        isCommitting={isCommitting}
        commitError={commitError}
      />
    </div>
  )
}

function isCSV(upload: UploadResponse): boolean {
  // Infer from the sheets list: xlsx always yields a named sheet (anything
  // other than "Sheet1"), csv yields exactly one sheet named "Sheet1". This
  // matches internal/api/import_spreadsheet.go:336 (ps.Name = "Sheet1").
  if (upload.sheets.length !== 1) return false
  return upload.sheets[0]?.name === 'Sheet1'
}

export default SpreadsheetConfigure
