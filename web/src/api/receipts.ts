import { get, put, post, postMultipart } from './client'
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
