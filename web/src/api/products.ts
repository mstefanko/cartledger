import { get, post, put, del, postMultipart } from './client'
import type {
  Product,
  ProductAlias,
  ProductImage,
  ProductLink,
  CreateProductRequest,
  UpdateProductRequest,
  CreateAliasRequest,
} from '@/types'

export async function listProducts(params?: { search?: string }): Promise<Product[]> {
  const searchParams = new URLSearchParams()
  if (params?.search) {
    searchParams.set('search', params.search)
  }
  const query = searchParams.toString()
  return get<Product[]>(`/products${query ? `?${query}` : ''}`)
}

export async function createProduct(data: CreateProductRequest): Promise<Product> {
  return post<Product>('/products', data)
}

export async function updateProduct(id: string, data: UpdateProductRequest): Promise<Product> {
  return put<Product>(`/products/${encodeURIComponent(id)}`, data)
}

export async function deleteProduct(id: string): Promise<void> {
  return del<void>(`/products/${encodeURIComponent(id)}`)
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
