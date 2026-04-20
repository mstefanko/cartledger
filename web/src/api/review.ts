import { get, post } from './client'

export interface PossibleListItemMatch {
  list_id: string
  list_name: string
  item_id: string
  item_name: string
}

export interface UnmatchedLineItem {
  id: string
  receipt_id: string
  receipt_date: string
  store_id?: string
  store_name?: string
  raw_text: string
  quantity: string
  unit?: string
  unit_price?: string
  total_price: string
  possible_list_items?: PossibleListItemMatch[]
}

export interface ImportBatchHeader {
  id: string
  source_type: string
  filename: string
  created_at: string
  receipts_count: number
  items_count: number
  unmatched_count: number
}

// batchId narrows the list/count to a single import batch. When omitted the
// endpoint returns the household-wide unmatched set (unchanged from P7).
export function listUnmatchedLineItems(batchId?: string): Promise<UnmatchedLineItem[]> {
  const qs = batchId ? `?batch_id=${encodeURIComponent(batchId)}` : ''
  return get<UnmatchedLineItem[]>(`/line-items/unmatched${qs}`)
}

export function getUnmatchedCount(batchId?: string): Promise<{ count: number }> {
  const qs = batchId ? `?batch_id=${encodeURIComponent(batchId)}` : ''
  return get<{ count: number }>(`/line-items/unmatched/count${qs}`)
}

export function getBatchHeader(batchId: string): Promise<ImportBatchHeader> {
  return get<ImportBatchHeader>(`/import/batches/${encodeURIComponent(batchId)}`)
}

export const linkListItem = (
  lineItemId: string,
  req: { list_item_id: string; also_assign_product_id?: string }
) => post<void>(`/line-items/${encodeURIComponent(lineItemId)}/link-list-item`, req)
