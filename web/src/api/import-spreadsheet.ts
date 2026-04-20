import { useMutation, useQuery, type UseMutationResult, type UseQueryResult } from '@tanstack/react-query'
import { del, get, post, postMultipart, ApiClientError } from './client'

// ---------------------------------------------------------------------------
// Wire types (snake_case; mirror internal/api/import_spreadsheet.go exactly).
// ---------------------------------------------------------------------------

export interface TypeCoverage {
  nonblank_pct: number
  date_pct: number
  money_pct: number
  integer_pct: number
  text_pct: number
}

export interface UploadSheet {
  name: string
  row_count: number
  headers: string[]
  column_samples: Record<string, string[]>
  type_coverage: Record<string, TypeCoverage>
}

export type DateFormat =
  | 'YYYY-MM-DD'
  | 'MM/DD/YYYY'
  | 'DD/MM/YYYY'
  | 'YYYY/MM/DD'
  | 'excel_serial'
  | 'ISO-8601'

export interface CSVOptions {
  delimiter: number // rune encoded as int
  has_header: boolean
  skip_start: number
  skip_end: number
}

export interface UnitOptions {
  price_multiplier: number
  total_unit_price_strategy: string // "trust_total" | "compute_missing" | "ignore_qty"
  negative_handling: string // "discount" | "refund" | "reject"
  default_unit: string
}

export type GroupingStrategy =
  | 'date_store'
  | 'date_store_total'
  | 'trip_id_column'
  | 'explicit_marker'

export interface Grouping {
  strategy: GroupingStrategy
  default_store_id: string
  explicit_ranges?: [number, number][]
}

export interface SuggestedConfig {
  sheet: string
  mapping: Record<string, number>
  date_format: DateFormat
  csv_options: CSVOptions
  unit_options: UnitOptions
  grouping: Grouping
  confidence: number
}

export interface SavedMapping {
  id: string
  name: string
  last_used_at?: string
}

export interface UploadResponse {
  import_id: string
  sheets: UploadSheet[]
  suggested: SuggestedConfig
  saved_mappings: SavedMapping[] | null
  fingerprint: string
  auto_applied_mapping_id: string | null
  // Full config to apply verbatim when the server matched either a
  // user-named mapping (chip-visible) OR the silent __last_used__ row for
  // this household. Null means "no saved state matched — fall back to
  // `suggested`."
  auto_applied_config: SuggestedConfig | null
}

// Listed mapping entry for the upload-screen chip rail. Shape mirrors
// internal/api/import_spreadsheet.go listMappingEntry.
export interface ListedMapping {
  id: string
  name: string
  source_type: string
  source_fingerprint: string
  last_used_at?: string
}

export interface ListMappingsResponse {
  mappings: ListedMapping[]
}

// Detail response for GET /import/spreadsheet/mappings/:id — carries the
// full config so the client can overwrite local state on chip click.
export interface GetMappingResponse {
  id: string
  name: string
  source_type: string
  source_fingerprint: string
  last_used_at?: string
  config: SuggestedConfig
}

export interface GetSheetResponse {
  sheet: UploadSheet
  suggested: SuggestedConfig
}

export interface TransformBody {
  kind: 'override_cell' | 'skip_row'
  row_index: number
  col_index?: number
  new_value?: string
  row_indices?: number[]
}

export interface TransformResponse {
  transform_id: string
  import_revision: number
}

export interface PreviewBody {
  sheet: string
  mapping: Record<string, number>
  date_format: DateFormat
  csv_options: CSVOptions
  unit_options: UnitOptions
  grouping: Grouping
  since_date?: string
  skip_row_indices?: number[]
  import_revision: number
}

export interface ParsedValue {
  row_index: number
  date: string
  store: string
  item: string
  qty: number
  unit: string
  unit_price_cents: number
  total_cents: number
  trip_id: string
  notes: string
  cell_errors?: Record<string, string>
}

export interface PreviewRow {
  row_index: number
  raw: string[]
  parsed: ParsedValue
  cell_errors?: Record<string, string>
  duplicate_of_receipt_id: string | null
  would_create_store: boolean
  receipt_group_id: string
  transform_origin: string | null
}

export interface PreviewGroup {
  group_id: string
  store: string
  date: string
  row_indices: number[]
  total_cents: number
  duplicate_of_receipt_id: string | null
  split_suggested: boolean
}

export interface PreviewSummary {
  receipts: number
  items: number
  duplicates: number
  new_stores: string[] | null
  rows_with_errors: number
  rows_after_since_filter: number
}

export interface PreviewResponse {
  rows: PreviewRow[]
  groups: PreviewGroup[]
  summary: PreviewSummary
  import_revision: number
}

export interface GroupOverride {
  include?: boolean
  is_duplicate_ok?: boolean
}

export interface CommitBody extends PreviewBody {
  save_mapping_as?: string
  group_overrides?: Record<string, GroupOverride>
}

export interface CommitErrorDetail {
  group_id: string
  message: string
  fatal: boolean
}

export interface CommitResponse {
  batch_id: string
  receipts_created: number
  line_items_created: number
  unmatched: number
  duplicates_skipped: number
  errors?: CommitErrorDetail[]
}

export interface ConflictDetail {
  error: string
  current_import_revision: number
  client_import_revision: number
}

// ---------------------------------------------------------------------------
// Hooks
// ---------------------------------------------------------------------------

export function useUploadSpreadsheet(): UseMutationResult<UploadResponse, Error, File> {
  return useMutation({
    mutationFn: async (file: File) => {
      const fd = new FormData()
      fd.append('file', file)
      return postMultipart<UploadResponse>('/import/spreadsheet/upload', fd)
    },
  })
}

export function useGetSheet(
  importId: string | null,
  sheetName: string | null,
): UseQueryResult<GetSheetResponse, Error> {
  return useQuery({
    queryKey: ['import-spreadsheet', importId, 'sheet', sheetName],
    queryFn: async () => {
      if (!importId || !sheetName) throw new Error('missing import_id/sheet_name')
      return get<GetSheetResponse>(
        `/import/spreadsheet/${encodeURIComponent(importId)}/sheet/${encodeURIComponent(sheetName)}`,
      )
    },
    enabled: Boolean(importId && sheetName),
  })
}

export function useTransform(
  importId: string,
): UseMutationResult<TransformResponse, Error, TransformBody> {
  return useMutation({
    mutationFn: async (body: TransformBody) =>
      post<TransformResponse>(
        `/import/spreadsheet/${encodeURIComponent(importId)}/transform`,
        body,
      ),
  })
}

export function usePreview(
  importId: string,
): UseMutationResult<PreviewResponse, Error, PreviewBody> {
  return useMutation({
    mutationFn: async (body: PreviewBody) =>
      post<PreviewResponse>(
        `/import/spreadsheet/${encodeURIComponent(importId)}/preview`,
        body,
      ),
  })
}

export function useCommit(
  importId: string,
): UseMutationResult<CommitResponse, Error, CommitBody> {
  return useMutation({
    mutationFn: async (body: CommitBody) =>
      post<CommitResponse>(
        `/import/spreadsheet/${encodeURIComponent(importId)}/commit`,
        body,
      ),
  })
}

export function useDeleteImport(
  importId: string,
): UseMutationResult<void, Error, void> {
  return useMutation({
    mutationFn: async () =>
      del<void>(`/import/spreadsheet/${encodeURIComponent(importId)}`),
  })
}

// List the household's saved mappings. Used on the upload screen to render
// "Reuse" chips before a file has been picked. Excludes the __last_used__
// sentinel (filtered server-side).
export function useListMappings(
  enabled: boolean = true,
): UseQueryResult<ListMappingsResponse, Error> {
  return useQuery({
    queryKey: ['import-spreadsheet', 'mappings'],
    queryFn: async () => get<ListMappingsResponse>('/import/spreadsheet/mappings'),
    enabled,
    staleTime: 60_000,
  })
}

// Fetch a single saved mapping (config included) so the configure screen
// can overwrite local state on chip click. Uses a mutation because it's a
// one-shot trigger driven by a user action, not a subscription.
export function useGetMapping(): UseMutationResult<GetMappingResponse, Error, string> {
  return useMutation({
    mutationFn: async (id: string) =>
      get<GetMappingResponse>(`/import/spreadsheet/mappings/${encodeURIComponent(id)}`),
  })
}

// Helper: extract retry-after seconds and a human message from a 429 ApiClientError.
export function parseRateLimit(err: unknown): { retryAfter: number; message: string } | null {
  if (!(err instanceof ApiClientError) || err.status !== 429) return null
  // ApiClientError.details is populated from the response body "details" key;
  // fall back to a generic string. The server emits {error:"rate_limited",retry_after:60}.
  const retry = 60
  return {
    retryAfter: retry,
    message: `Rate limited — try again in ${retry}s.`,
  }
}

// Helper: detect the revision-conflict shape (409 on commit).
export function parseRevisionConflict(err: unknown): ConflictDetail | null {
  if (!(err instanceof ApiClientError) || err.status !== 409) return null
  return {
    error: err.message,
    current_import_revision: -1,
    client_import_revision: -1,
  }
}
