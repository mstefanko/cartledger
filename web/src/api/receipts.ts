import { get, put, post, del, postMultipart } from './client'
import type {
  Receipt,
  LineItem,
  UpdateLineItemRequest,
  AcceptSuggestionsRequest,
  AcceptSuggestionsResponse,
} from '@/types'

export async function scanReceipt(images: File[]): Promise<Receipt> {
  const formData = new FormData()
  for (const image of images) {
    formData.append('images', image)
  }
  return postMultipart<Receipt>('/receipts/scan', formData)
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

export interface ManualLineItemInput {
  raw_name: string
  product_id?: string
  quantity?: string
  unit?: string
  unit_price?: string
  total_price: string
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
