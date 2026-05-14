import { get, post, put, del, postMultipart } from './client'
import type {
  Product,
  ProductListItem,
  ProductAlias,
  ProductImage,
  ProductLink,
  ProductDetail,
  ProductEnrichmentJob,
  ProductEnrichmentSuggestion,
  BulkProductEnrichmentSuggestionsResponse,
  CreateProductRequest,
  UpdateProductRequest,
  CreateAliasRequest,
} from '@/types'

export async function listProducts(params?: {
  search?: string
  sort?: 'last_purchased_at'
}): Promise<ProductListItem[]> {
  const searchParams = new URLSearchParams()
  if (params?.search) {
    searchParams.set('q', params.search)
  }
  if (params?.sort) {
    searchParams.set('sort', params.sort)
  }
  const query = searchParams.toString()
  return get<ProductListItem[]>(`/products${query ? `?${query}` : ''}`)
}

export async function createProduct(data: CreateProductRequest): Promise<Product> {
  return post<Product>('/products', data)
}

export async function updateProduct(id: string, data: UpdateProductRequest): Promise<Product> {
  return put<Product>(`/products/${encodeURIComponent(id)}`, data)
}

export async function deleteProduct(id: string): Promise<{ deleted: true; unmatched_line_items: number }> {
  return del<{ deleted: true; unmatched_line_items: number }>(`/products/${encodeURIComponent(id)}`)
}

export interface ProductUsage {
  line_items: number
  shopping_list_items: number
  matching_rules: number
  aliases: number
  images: number
}

export async function getProductUsage(id: string): Promise<ProductUsage> {
  return get<ProductUsage>(`/products/${encodeURIComponent(id)}/usage`)
}

export async function getProductDetail(id: string): Promise<ProductDetail> {
  const detail = await get<ProductDetail>(`/products/${encodeURIComponent(id)}/detail`)
  return normalizeProductDetail(detail)
}

function arrayOrEmpty<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : []
}

function normalizeProductDetail(detail: ProductDetail): ProductDetail {
  return {
    ...detail,
    aliases: arrayOrEmpty(detail.aliases),
    store_codes: arrayOrEmpty(detail.store_codes),
    images: arrayOrEmpty(detail.images),
    links: arrayOrEmpty(detail.links),
    nutrition: arrayOrEmpty(detail.nutrition),
    external_metadata: arrayOrEmpty(detail.external_metadata),
    enrichment_suggestions: arrayOrEmpty(detail.enrichment_suggestions),
    price_history: arrayOrEmpty(detail.price_history),
    store_comparison: arrayOrEmpty(detail.store_comparison),
  }
}

export async function previewProductPriceRecompute(id: string): Promise<{ affected_count: number }> {
  return get<{ affected_count: number }>(`/products/${encodeURIComponent(id)}/recompute-prices/preview`)
}

export async function recomputeProductPrices(id: string): Promise<{ updated_count: number }> {
  return post<{ updated_count: number }>(`/products/${encodeURIComponent(id)}/recompute-prices`, {})
}

export async function uploadProductImage(productId: string, file: File, type?: string, caption?: string): Promise<ProductImage> {
  const formData = new FormData()
  formData.append('image', file)
  if (type) formData.append('type', type)
  if (caption) formData.append('caption', caption)
  return postMultipart<ProductImage>(`/products/${encodeURIComponent(productId)}/images`, formData)
}

export async function deleteProductImage(productId: string, imageId: string): Promise<void> {
  return del<void>(`/products/${encodeURIComponent(productId)}/images/${encodeURIComponent(imageId)}`)
}

export async function listProductLinks(productId: string): Promise<ProductLink[]> {
  return get<ProductLink[]>(`/products/${encodeURIComponent(productId)}/links`)
}

export async function addProductLink(
  productId: string,
  data: { url: string },
): Promise<{ link: ProductLink; suggestions: ProductEnrichmentSuggestion[] }> {
  return post<{ link: ProductLink; suggestions: ProductEnrichmentSuggestion[] }>(
    `/products/${encodeURIComponent(productId)}/links`,
    data,
  )
}

export async function deleteProductLink(productId: string, linkId: string): Promise<void> {
  return del<void>(`/products/${encodeURIComponent(productId)}/links/${encodeURIComponent(linkId)}`)
}

export async function createProductEnrichmentJob(
  productId: string,
  data: { trigger?: 'manual_lookup' | 'manual_refresh'; sources?: string[]; upc?: string; url?: string },
): Promise<{ job: ProductEnrichmentJob }> {
  return post<{ job: ProductEnrichmentJob }>(
    `/products/${encodeURIComponent(productId)}/enrichment-jobs`,
    data,
  )
}

export async function listProductEnrichmentJobs(
  productId: string,
): Promise<{ jobs: ProductEnrichmentJob[] }> {
  return get<{ jobs: ProductEnrichmentJob[] }>(
    `/products/${encodeURIComponent(productId)}/enrichment-jobs`,
  )
}

export async function enrichProductByUPC(
  productId: string,
  data: { upc: string },
): Promise<{ job: ProductEnrichmentJob }> {
  return post<{ job: ProductEnrichmentJob }>(
    `/products/${encodeURIComponent(productId)}/enrich/upc`,
    data,
  )
}

export async function acceptProductEnrichmentSuggestion(
  productId: string,
  suggestionId: string,
  data: { fields?: string[] } = {},
): Promise<ProductEnrichmentSuggestion> {
  return post<ProductEnrichmentSuggestion>(
    `/products/${encodeURIComponent(productId)}/enrichment-suggestions/${encodeURIComponent(suggestionId)}/accept`,
    data,
  )
}

export async function rejectProductEnrichmentSuggestion(
  productId: string,
  suggestionId: string,
): Promise<void> {
  return post<void>(
    `/products/${encodeURIComponent(productId)}/enrichment-suggestions/${encodeURIComponent(suggestionId)}/reject`,
    {},
  )
}

export async function bulkAcceptProductEnrichmentSuggestions(
  productId: string,
  data: { suggestion_ids: string[]; recompute_prices?: boolean },
): Promise<BulkProductEnrichmentSuggestionsResponse> {
  return post<BulkProductEnrichmentSuggestionsResponse>(
    `/products/${encodeURIComponent(productId)}/enrichment-suggestions/bulk-accept`,
    data,
  )
}

export async function bulkRejectProductEnrichmentSuggestions(
  productId: string,
  data: { suggestion_ids: string[] },
): Promise<{ rejected: number }> {
  return post<{ rejected: number }>(
    `/products/${encodeURIComponent(productId)}/enrichment-suggestions/bulk-reject`,
    data,
  )
}

export async function listProductAliases(productId: string): Promise<ProductAlias[]> {
  return get<ProductAlias[]>(`/products/${encodeURIComponent(productId)}/aliases`)
}

export async function createProductAlias(productId: string, data: CreateAliasRequest): Promise<ProductAlias> {
  return post<ProductAlias>(`/products/${encodeURIComponent(productId)}/aliases`, data)
}

export async function deleteProductAlias(productId: string, aliasId: string): Promise<void> {
  return del<void>(`/products/${encodeURIComponent(productId)}/aliases/${encodeURIComponent(aliasId)}`)
}

export async function mergeProducts(keepId: string, mergeId: string): Promise<void> {
  return post<void>('/products/merge', {
    keep_id: keepId,
    merge_id: mergeId,
  })
}

export async function bulkAssignGroup(
  productIds: string[],
  productGroupId: string | null,
): Promise<{ updated: number }> {
  return post<{ updated: number }>('/products/bulk-group', {
    product_ids: productIds,
    product_group_id: productGroupId,
  })
}
