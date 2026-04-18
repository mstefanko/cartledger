export interface ExportParams {
  from?: string // YYYY-MM-DD
  to?: string // YYYY-MM-DD
  store_id?: string
  format: 'csv' | 'markdown'
}

// Build the absolute export URL. We intentionally live on the same origin
// as the Go server (see client.ts getBaseUrl), so a bare path works for
// the browser's native download (cookie auth flows automatically).
export function exportReceiptsURL(params: ExportParams): string {
  const qs = new URLSearchParams()
  qs.set('format', params.format)
  if (params.from) qs.set('from', params.from)
  if (params.to) qs.set('to', params.to)
  if (params.store_id) qs.set('store_id', params.store_id)
  return `/api/v1/export/receipts?${qs.toString()}`
}

// Kick the browser into downloading the export. window.location.href is
// preferable to a programmatic <a>.click() because it lets the browser
// handle the Content-Disposition attachment natively and does not
// require DOM mutation.
export function triggerExportDownload(params: ExportParams): void {
  window.location.href = exportReceiptsURL(params)
}
