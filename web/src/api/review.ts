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

export function listUnmatchedLineItems(): Promise<UnmatchedLineItem[]> {
  return get<UnmatchedLineItem[]>('/line-items/unmatched')
}

export function getUnmatchedCount(): Promise<{ count: number }> {
  return get<{ count: number }>('/line-items/unmatched/count')
}

export const linkListItem = (
  lineItemId: string,
  req: { list_item_id: string; also_assign_product_id?: string }
) => post<void>(`/line-items/${encodeURIComponent(lineItemId)}/link-list-item`, req)
