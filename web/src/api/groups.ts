import { get, post, put, del } from './client'
import type {
  ProductGroup,
  CreateGroupRequest,
  UpdateGroupRequest,
  GroupSuggestion,
} from '@/types'

export async function fetchGroups(): Promise<ProductGroup[]> {
  return get<ProductGroup[]>('/product-groups')
}

export async function fetchGroup(id: string): Promise<ProductGroup> {
  return get<ProductGroup>(`/product-groups/${encodeURIComponent(id)}`)
}

export async function createGroup(data: CreateGroupRequest): Promise<ProductGroup> {
  return post<ProductGroup>('/product-groups', data)
}

export async function updateGroup(id: string, data: UpdateGroupRequest): Promise<ProductGroup> {
  return put<ProductGroup>(`/product-groups/${encodeURIComponent(id)}`, data)
}

export async function deleteGroup(id: string): Promise<void> {
  return del<void>(`/product-groups/${encodeURIComponent(id)}`)
}

export async function fetchGroupSuggestions(productId: string): Promise<GroupSuggestion[]> {
  return get<GroupSuggestion[]>(`/product-groups/suggestions?product_id=${encodeURIComponent(productId)}`)
}
