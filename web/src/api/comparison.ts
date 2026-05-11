import { useQuery } from '@tanstack/react-query'
import { post } from './client'
import type { Receipt } from '@/types'

export interface CompareReceipt {
  id: string
  store_id: string | null
  store_name: string | null
  receipt_date: string
  total: string | null
  line_count: number
  status: Receipt['status']
}

export interface CompareAppearance {
  line_item_id: string
  receipt_id: string
  raw_name: string
  quantity?: string
  unit?: string
  total_price: string
  unit_price?: string
  size_known: boolean
  normalized_price?: string
  normalized_unit?: string
  lines?: CompareLineChoice[]
}

export interface CompareLineChoice {
  line_item_id: string
  raw_name: string
  quantity?: string
  unit?: string
  total_price: string
  unit_price?: string
}

export interface CompareProduct {
  comparison_key: string
  product_id: string
  product_group_id: string | null
  name: string
  category: string | null
  comparable_unit: string | null
  best_appearance_id: string | null
  appearances: CompareAppearance[]
}

export interface CompareReceiptsResponse {
  receipts: CompareReceipt[]
  products: CompareProduct[]
  min_overlap: number
  missing_unit_count: number
}

export async function compareReceipts(
  receiptIds: string[],
  minOverlap?: number,
): Promise<CompareReceiptsResponse> {
  return post<CompareReceiptsResponse>('/receipts/compare', {
    receipt_ids: receiptIds,
    ...(minOverlap ? { min_overlap: minOverlap } : {}),
  })
}

export function useCompareReceipts(receiptIds: string[], minOverlap: number) {
  const orderedIds = [...receiptIds]
  return useQuery({
    queryKey: ['receipts', 'compare', orderedIds, minOverlap],
    queryFn: () => compareReceipts(orderedIds, minOverlap),
    enabled: receiptIds.length >= 2,
  })
}
