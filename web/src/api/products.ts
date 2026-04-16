import { get, post, put, del, postMultipart } from './client'
import type {
  Product,
  ProductListItem,
  ProductAlias,
  ProductImage,
  ProductLink,
  ProductDetail,
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
  return get<ProductDetail>(`/products/${encodeURIComponent(id)}/detail`)
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
