import { get, post, put, del, patch } from './client'
import { getToken } from './client'
import type {
  ShoppingListWithCounts,
  ShoppingListDetail,
  ListItemWithPrice,
  CreateListRequest,
  UpdateListRequest,
  CreateListItemRequest,
  UpdateListItemRequest,
  ReorderListItemsRequest,
} from '@/types'

export async function listLists(): Promise<ShoppingListWithCounts[]> {
  return get<ShoppingListWithCounts[]>('/lists')
}

export async function createList(data: CreateListRequest): Promise<ShoppingListWithCounts> {
  return post<ShoppingListWithCounts>('/lists', data)
}

export async function getList(id: string): Promise<ShoppingListDetail> {
  return get<ShoppingListDetail>(`/lists/${encodeURIComponent(id)}`)
}

export async function updateList(
  id: string,
  data: UpdateListRequest,
): Promise<ShoppingListWithCounts> {
  return put<ShoppingListWithCounts>(`/lists/${encodeURIComponent(id)}`, data)
}

export async function deleteList(id: string): Promise<void> {
  return del<void>(`/lists/${encodeURIComponent(id)}`)
}

export async function addItem(
  listId: string,
  data: CreateListItemRequest,
): Promise<ListItemWithPrice> {
  return post<ListItemWithPrice>(`/lists/${encodeURIComponent(listId)}/items`, data)
}

export interface BulkAddItemsResponse {
  items: ListItemWithPrice[]
  list: ShoppingListDetail
}

export async function bulkAddItems(
  listId: string,
  items: CreateListItemRequest[],
): Promise<BulkAddItemsResponse> {
  return post<BulkAddItemsResponse>(
    `/lists/${encodeURIComponent(listId)}/items/bulk`,
    { items },
  )
}

export interface BulkUpdateItemsPatch {
  assigned_store_id?: string | null
  checked?: boolean
}

export interface BulkUpdateItemsResponse {
  list_id: string
  item_ids: string[]
}

export async function bulkUpdateItems(
  listId: string,
  params: { item_ids: string[]; patch: BulkUpdateItemsPatch },
): Promise<BulkUpdateItemsResponse> {
  return patch<BulkUpdateItemsResponse>(
    `/lists/${encodeURIComponent(listId)}/items/bulk`,
    params,
  )
}

export interface BulkDeleteItemsResponse {
  list_id: string
  item_ids: string[]
}

export async function bulkDeleteItems(
  listId: string,
  itemIds: string[],
): Promise<BulkDeleteItemsResponse> {
  return del<BulkDeleteItemsResponse>(
    `/lists/${encodeURIComponent(listId)}/items/bulk`,
    { item_ids: itemIds },
  )
}

export async function updateItem(
  listId: string,
  itemId: string,
  data: UpdateListItemRequest,
): Promise<ListItemWithPrice> {
  return put<ListItemWithPrice>(
    `/lists/${encodeURIComponent(listId)}/items/${encodeURIComponent(itemId)}`,
    data,
  )
}

export async function deleteItem(listId: string, itemId: string): Promise<void> {
  return del<void>(
    `/lists/${encodeURIComponent(listId)}/items/${encodeURIComponent(itemId)}`,
  )
}

export async function reorderItems(
  listId: string,
  data: ReorderListItemsRequest,
): Promise<void> {
  return put<void>(`/lists/${encodeURIComponent(listId)}/reorder`, data)
}

export async function getShareText(id: string): Promise<string> {
  const token = getToken()
  const response = await fetch(
    `${window.location.origin}/api/v1/lists/${encodeURIComponent(id)}/share`,
    {
      method: 'GET',
      headers: {
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        Accept: 'text/plain',
      },
    },
  )
  if (!response.ok) {
    throw new Error(`Failed to get share text: ${response.status}`)
  }
  return response.text()
}
