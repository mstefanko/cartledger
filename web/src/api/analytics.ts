import { get } from './client'
import type {
  AnalyticsOverview,
  ProductTrend,
  ProductTrendResponse,
  ProductWithTrend,
  StoreSummary,
  Trip,
  Deal,
  BuyAgainItem,
  Rhythm,
  CategoryBreakdown,
  Savings,
  Staple,
  PriceMoves,
  InflationIndex,
} from '@/types'

export async function getOverview(): Promise<AnalyticsOverview> {
  return get<AnalyticsOverview>('/analytics/overview')
}

export async function getProductTrend(id: string): Promise<ProductTrend> {
  return get<ProductTrend>(`/analytics/products/${encodeURIComponent(id)}/trend`)
}

export async function getProductsWithTrends(params?: {
  sort?: string
  order?: string
}): Promise<ProductWithTrend[]> {
  const searchParams = new URLSearchParams()
  if (params?.sort) searchParams.set('sort', params.sort)
  if (params?.order) searchParams.set('order', params.order)
  const query = searchParams.toString()
  const resp = await get<{ products: ProductWithTrend[]; total: number }>(`/analytics/products${query ? `?${query}` : ''}`)
  return resp?.products ?? []
}

export async function getStoreSummary(id: string): Promise<StoreSummary> {
  return get<StoreSummary>(`/analytics/stores/${encodeURIComponent(id)}/summary`)
}

export async function getTrips(): Promise<Trip[]> {
  return get<Trip[]>('/analytics/trips')
}

export async function getDeals(): Promise<Deal[]> {
  return get<Deal[]>('/analytics/deals')
}

export async function getBuyAgain(): Promise<BuyAgainItem[]> {
  return get<BuyAgainItem[]>('/analytics/buy-again')
}

export async function getRhythm(): Promise<Rhythm> {
  return get<Rhythm>('/analytics/rhythm')
}

export const fetchProductTrend = (productId: string) =>
  get<ProductTrendResponse>(`/analytics/products/${encodeURIComponent(productId)}/trend`)

export const fetchGroupTrend = (groupId: string) =>
  get<ProductTrendResponse>(`/analytics/product-groups/${encodeURIComponent(groupId)}/trend`)

export async function getCategoryBreakdown(): Promise<CategoryBreakdown> {
  return get<CategoryBreakdown>('/analytics/category-breakdown')
}

export async function getSavings(): Promise<Savings> {
  return get<Savings>('/analytics/savings')
}

export async function getStaples(): Promise<Staple[]> {
  const resp = await get<Staple[]>('/analytics/staples')
  return resp ?? []
}

export async function getPriceMoves(): Promise<PriceMoves> {
  return get<PriceMoves>('/analytics/price-moves')
}

export async function getInflation(): Promise<InflationIndex> {
  return get<InflationIndex>('/analytics/inflation')
}
