import { get, put, post, del, postMultipart } from './client'
import type {
  Receipt,
  LineItem,
  UpdateLineItemRequest,
  UpdateReceiptRequest,
  AcceptSuggestionsRequest,
  AcceptSuggestionsResponse,
} from '@/types'
import type { ReceiptUploadPage } from '@/lib/receiptUpload'
import type { ProductEnrichmentJob } from '@/types'

export interface ScanReceiptResponse {
  id: string
  status: Receipt['status']
  duplicate_candidates?: number
}

export async function scanReceipt(pages: ReceiptUploadPage[]): Promise<ScanReceiptResponse> {
  const formData = new FormData()
  for (const page of pages) {
    formData.append('images', page.file)
  }
  formData.append('page_sources', JSON.stringify(pages.map((page) => page.source)))
  return postMultipart<ScanReceiptResponse>('/receipts/scan', formData)
}

export async function listReceipts(): Promise<Receipt[]> {
  return get<Receipt[]>('/receipts')
}

export interface ReceiptDetail extends Receipt {
  line_items: LineItem[]
}

export async function getReceipt(id: string): Promise<ReceiptDetail> {
  return get<ReceiptDetail>(`/receipts/${encodeURIComponent(id)}`)
}

export async function updateLineItem(
  receiptId: string,
  itemId: string,
  data: UpdateLineItemRequest,
): Promise<LineItem> {
  return put<LineItem>(
    `/receipts/${encodeURIComponent(receiptId)}/line-items/${encodeURIComponent(itemId)}`,
    data,
  )
}

export interface CreateLineItemRequest {
  raw_name: string
  product_id?: string
  upc?: string
  quantity?: string
  unit?: string
  unit_price?: string
  total_price: string
  line_number?: number
  count_contribution?: string
  pack_quantity_override?: string
  pack_unit_override?: string
}

export async function createLineItem(
  receiptId: string,
  data: CreateLineItemRequest,
): Promise<LineItem> {
  return post<LineItem>(
    `/receipts/${encodeURIComponent(receiptId)}/line-items`,
    data,
  )
}

export interface CreateLineItemsResponse {
  created_count: number
  status: string
}

export async function createLineItems(
  receiptId: string,
  items: ManualLineItemInput[],
): Promise<CreateLineItemsResponse> {
  return post<CreateLineItemsResponse>(
    `/receipts/${encodeURIComponent(receiptId)}/line-items/bulk`,
    { items },
  )
}

export async function acceptSuggestions(
  receiptId: string,
  data: AcceptSuggestionsRequest,
): Promise<AcceptSuggestionsResponse> {
  return post<AcceptSuggestionsResponse>(
    `/receipts/${encodeURIComponent(receiptId)}/accept-suggestions`,
    data,
  )
}

export async function deleteReceipt(receiptId: string): Promise<{ status: string }> {
  return del(`/receipts/${encodeURIComponent(receiptId)}`)
}

export async function confirmReceipt(
  receiptId: string,
): Promise<{ status: string }> {
  return put<{ status: string }>(
    `/receipts/${encodeURIComponent(receiptId)}`,
    { status: 'reviewed' },
  )
}

export async function updateReceipt(
  receiptId: string,
  data: UpdateReceiptRequest,
): Promise<{ status: string }> {
  return put<{ status: string }>(
    `/receipts/${encodeURIComponent(receiptId)}`,
    data,
  )
}

// Re-enqueue a failed (or still-pending) receipt for background processing.
// Server returns 202 with {id, status: 'pending'}. The UI should flip the
// card to "processing" and wait for the 'receipt.complete' WS event (which
// invalidates the receipts/receipt caches).
export async function reprocessReceipt(
  receiptId: string,
): Promise<{ id: string; status: string }> {
  return post<{ id: string; status: string }>(
    `/receipts/${encodeURIComponent(receiptId)}/reprocess`,
  )
}

export interface RepairPreviewItem {
  raw_name: string
  store_item_code?: string | null
  receipt_description?: string | null
  suggested_name: string
  quantity: number
  unit: string | null
  unit_price: number | null
  total_price: number
  regular_price: number | null
  discount_amount: number | null
  line_number: number
}

export interface RepairPreviewResponse {
  date: string
  time: string | null
  items_sold_count: number | null
  items: RepairPreviewItem[]
  subtotal: number
  tax: number
  total: number
}

export async function repairReceiptPreview(
  receiptId: string,
  note: string,
): Promise<RepairPreviewResponse> {
  return post<RepairPreviewResponse>(
    `/receipts/${encodeURIComponent(receiptId)}/repair-preview`,
    { note },
  )
}

export async function applyRepairPreview(
  receiptId: string,
  preview: RepairPreviewResponse,
): Promise<{ status: string }> {
  return post<{ status: string }>(
    `/receipts/${encodeURIComponent(receiptId)}/apply-repair`,
    preview,
  )
}

export interface ManualLineItemInput {
  raw_name: string
  product_id?: string
  upc?: string
  quantity?: string
  unit?: string
  unit_price?: string
  total_price: string
  pack_quantity_override?: string
  pack_unit_override?: string
  pack_override_source?: 'user'
}

export interface CreateManualReceiptRequest {
  store_id?: string
  receipt_date: string
  subtotal?: string
  tax?: string
  total?: string
  items: ManualLineItemInput[]
}

export async function createManualReceipt(
  data: CreateManualReceiptRequest,
): Promise<{ id: string }> {
  return post<{ id: string }>('/receipts/manual', data)
}

export interface LineItemBarcodePreview {
  upc: string
  matched_product?: { id: string; name: string }
  create_product_allowed: boolean
  lookup_available: boolean
  lookup_skipped_reason?: 'env_disabled' | 'household_manual_lookup_disabled' | 'no_provider_configured' | 'queue_failed'
  conflict?: {
    code: string
    message: string
    existing_product_id?: string
    existing_product_name?: string
    suggested_merge: boolean
  }
}

export interface LineItemBarcodeApplyResponse {
  line_item_id: string
  upc: string
  product_id: string
  product_name: string
  created_product: boolean
  job?: ProductEnrichmentJob
  lookup_skipped_reason?: 'env_disabled' | 'household_manual_lookup_disabled' | 'no_provider_configured' | 'queue_failed'
}

export async function previewLineItemBarcode(
  receiptId: string,
  itemId: string,
  upc: string,
  createProduct = false,
): Promise<LineItemBarcodePreview> {
  return post<LineItemBarcodePreview>(
    `/receipts/${encodeURIComponent(receiptId)}/line-items/${encodeURIComponent(itemId)}/barcode/preview`,
    { upc, create_product: createProduct },
  )
}

export async function applyLineItemBarcode(
  receiptId: string,
  itemId: string,
  data: { upc: string; create_product?: boolean },
): Promise<LineItemBarcodeApplyResponse> {
  return post<LineItemBarcodeApplyResponse>(
    `/receipts/${encodeURIComponent(receiptId)}/line-items/${encodeURIComponent(itemId)}/barcode`,
    { upc: data.upc, create_product: Boolean(data.create_product) },
  )
}
