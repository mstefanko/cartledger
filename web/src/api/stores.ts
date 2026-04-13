import { get, post, put, del } from './client'
import type {
  Store,
  CreateStoreRequest,
  UpdateStoreRequest,
  ReorderStoresRequest,
} from '@/types'

export async function listStores(): Promise<Store[]> {
  return get<Store[]>('/stores')
}

export async function createStore(data: CreateStoreRequest): Promise<Store> {
  return post<Store>('/stores', data)
}

export async function updateStore(id: string, data: UpdateStoreRequest): Promise<Store> {
  return put<Store>(`/stores/${encodeURIComponent(id)}`, data)
}

export async function deleteStore(id: string): Promise<void> {
  return del<void>(`/stores/${encodeURIComponent(id)}`)
}

export async function reorderStores(data: ReorderStoresRequest): Promise<void> {
  return put<void>('/stores/reorder', data)
}
