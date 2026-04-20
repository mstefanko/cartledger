import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiClientError } from '@/api/client'
import {
  type CSVOptions,
  type CommitResponse,
  type DateFormat,
  type Grouping,
  type PreviewResponse,
  type SuggestedConfig,
  type UnitOptions,
  type UploadResponse,
  useCommit,
  usePreview,
  useUploadSpreadsheet,
} from '@/api/import-spreadsheet'
import SpreadsheetUpload from './SpreadsheetUpload'
import SpreadsheetConfigure from './SpreadsheetConfigure'

/**
 * Parent-held config object. A single source of truth fed into every control
 * and into every preview/commit request. Children receive a patch helper and
 * never mutate the object directly.
 */
export interface ImportConfig {
  sheet: string
  mapping: Record<string, number>
  dateFormat: DateFormat
  csvOptions: CSVOptions
  unitOptions: UnitOptions
  grouping: Grouping
  sinceDate: string
  skipRowIndices: number[]
  groupOverrides: Record<string, { include?: boolean; isDuplicate?: boolean }>
  importRevision: number
  saveMappingAs?: string
}

function configFromSuggested(s: SuggestedConfig): ImportConfig {
  return {
    sheet: s.sheet,
    mapping: { ...s.mapping },
    dateFormat: s.date_format,
    csvOptions: { ...s.csv_options },
    unitOptions: { ...s.unit_options },
    grouping: { ...s.grouping },
    sinceDate: '',
    skipRowIndices: [],
    groupOverrides: {},
    importRevision: 0,
  }
}

function SpreadsheetImportTab() {
  const navigate = useNavigate()
  const [upload, setUpload] = useState<UploadResponse | null>(null)
  const [config, setConfig] = useState<ImportConfig | null>(null)
  const [preview, setPreview] = useState<PreviewResponse | null>(null)
  const [rateLimitMessage, setRateLimitMessage] = useState<string | null>(null)
  const [conflictMessage, setConflictMessage] = useState<string | null>(null)

  const uploadMutation = useUploadSpreadsheet()
  const previewMutation = usePreview(upload?.import_id ?? '')
  const commitMutation = useCommit(upload?.import_id ?? '')

  // Handle upload result — seed config from auto_applied_config when the
  // server recognized this layout (named match OR silent __last_used__),
  // falling back to the heuristic `suggested` config otherwise. This is
  // the point at which Phase 7's "silent recall" happens for the
  // __last_used__ path — auto_applied_mapping_id stays null but the
  // committed config still overwrites the heuristic.
  useEffect(() => {
    if (uploadMutation.data && uploadMutation.data !== upload) {
      const u = uploadMutation.data
      setUpload(u)
      const seed = u.auto_applied_config ?? u.suggested
      setConfig(configFromSuggested(seed))
      setPreview(null)
    }
  }, [uploadMutation.data, upload])

  // Rate limit toast — shown for 6s, replaced by newer events.
  useEffect(() => {
    if (!rateLimitMessage) return
    const t = setTimeout(() => setRateLimitMessage(null), 6000)
    return () => clearTimeout(t)
  }, [rateLimitMessage])

  // Upload surface errors (including rate-limit 429).
  useEffect(() => {
    const e = uploadMutation.error
    if (e instanceof ApiClientError && e.status === 429) {
      setRateLimitMessage('Upload rate limit hit — try again in 60 seconds.')
    }
  }, [uploadMutation.error])

  // Refetch preview when config changes (debounced).
  const refetchPreview = useCallback(
    (c: ImportConfig) => {
      if (!upload) return
      previewMutation.mutate(
        {
          sheet: c.sheet,
          mapping: c.mapping,
          date_format: c.dateFormat,
          csv_options: c.csvOptions,
          unit_options: c.unitOptions,
          grouping: c.grouping,
          since_date: c.sinceDate || undefined,
          skip_row_indices: c.skipRowIndices,
          import_revision: c.importRevision,
        },
        {
          onSuccess: (data) => {
            setPreview(data)
            setConfig((prev) =>
              prev ? { ...prev, importRevision: data.import_revision } : prev,
            )
          },
          onError: (err) => {
            if (err instanceof ApiClientError && err.status === 429) {
              setRateLimitMessage('Preview rate limit hit — pausing refresh.')
            }
          },
        },
      )
    },
    [upload, previewMutation],
  )

  const handleCommit = useCallback(() => {
    if (!upload || !config) return
    commitMutation.mutate(
      {
        sheet: config.sheet,
        mapping: config.mapping,
        date_format: config.dateFormat,
        csv_options: config.csvOptions,
        unit_options: config.unitOptions,
        grouping: config.grouping,
        since_date: config.sinceDate || undefined,
        skip_row_indices: config.skipRowIndices,
        import_revision: config.importRevision,
        save_mapping_as: config.saveMappingAs,
        group_overrides: Object.fromEntries(
          Object.entries(config.groupOverrides).map(([k, v]) => [
            k,
            { include: v.include, is_duplicate_ok: v.isDuplicate },
          ]),
        ),
      },
      {
        onSuccess: (result: CommitResponse) => {
          navigate(`/import/spreadsheet/result`, {
            state: { result, importId: upload.import_id },
          })
        },
        onError: (err) => {
          if (err instanceof ApiClientError && err.status === 409) {
            setConflictMessage(
              'Another change landed — refreshing preview. Retry commit after it loads.',
            )
            if (config) refetchPreview(config)
            setTimeout(() => setConflictMessage(null), 6000)
          }
        },
      },
    )
  }, [upload, config, commitMutation, navigate, refetchPreview])

  if (!upload || !config) {
    return (
      <>
        {rateLimitMessage && <InlineBanner variant="warning">{rateLimitMessage}</InlineBanner>}
        <SpreadsheetUpload
          onUpload={(file) => uploadMutation.mutate(file)}
          isUploading={uploadMutation.isPending}
          errorMessage={
            uploadMutation.error instanceof ApiClientError
              ? uploadMutation.error.message
              : null
          }
        />
      </>
    )
  }

  return (
    <div className="pb-32">
      {rateLimitMessage && <InlineBanner variant="warning">{rateLimitMessage}</InlineBanner>}
      {conflictMessage && <InlineBanner variant="warning">{conflictMessage}</InlineBanner>}

      <SpreadsheetConfigure
        upload={upload}
        config={config}
        onConfigChange={(patch) => {
          setConfig((prev) => {
            if (!prev) return prev
            const next = { ...prev, ...patch }
            return next
          })
        }}
        onConfigReplace={(next) => {
          setConfig(next)
        }}
        onConfigChanged={(next) => {
          refetchPreview(next)
        }}
        preview={preview}
        isPreviewLoading={previewMutation.isPending}
        onCommit={handleCommit}
        isCommitting={commitMutation.isPending}
        commitError={
          commitMutation.error instanceof ApiClientError &&
          commitMutation.error.status !== 409
            ? commitMutation.error.message
            : null
        }
        onStartOver={() => {
          setUpload(null)
          setConfig(null)
          setPreview(null)
          uploadMutation.reset()
        }}
      />
    </div>
  )
}

function InlineBanner({
  children,
  variant,
}: {
  children: React.ReactNode
  variant: 'warning' | 'error'
}) {
  const cls =
    variant === 'warning'
      ? 'bg-amber-50 border border-amber-200 text-amber-800'
      : 'bg-expensive-subtle border border-expensive/30 text-expensive'
  return (
    <div className={`rounded-xl px-4 py-2.5 text-caption mb-4 ${cls}`} role="status">
      {children}
    </div>
  )
}

export default SpreadsheetImportTab
