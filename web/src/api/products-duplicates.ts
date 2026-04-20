import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { del, get, post } from './client'

// --- Response types ---

export interface DuplicatePairSide {
  id: string
  name: string
  brand: string | null
  category: string | null
  purchase_count: number
  last_purchased_at: string | null
  sample_aliases: string[]
}

export interface DuplicatePair {
  a: DuplicatePairSide
  b: DuplicatePairSide
  similarity: number
}

export interface DuplicateCandidatesResponse {
  pairs: DuplicatePair[]
  count: number
}

export interface DuplicateCandidatesParams {
  limit?: number
  minSimilarity?: number
  maxSimilarity?: number
}

/**
 * Fetches the duplicate-candidates feed. Each call re-runs the O(n^2)
 * pairwise comparison on the backend, so we cache aggressively in
 * react-query: the data changes only when the user merges or dismisses,
 * and both mutation hooks below invalidate this query on success.
 */
export async function getDuplicateCandidates(
  params: DuplicateCandidatesParams = {},
): Promise<DuplicateCandidatesResponse> {
  const qs = new URLSearchParams()
  if (params.limit !== undefined) qs.set('limit', String(params.limit))
  if (params.minSimilarity !== undefined) qs.set('min_similarity', String(params.minSimilarity))
  if (params.maxSimilarity !== undefined) qs.set('max_similarity', String(params.maxSimilarity))
  const suffix = qs.toString()
  return get<DuplicateCandidatesResponse>(
    `/products/duplicate-candidates${suffix ? `?${suffix}` : ''}`,
  )
}

/** Idempotent. Backend canonicalizes the pair so callers don't have to. */
export async function markNotDuplicate(productAID: string, productBID: string): Promise<void> {
  return post<void>('/products/not-duplicate-pairs', {
    product_a_id: productAID,
    product_b_id: productBID,
  })
}

/** Symmetric with markNotDuplicate — lets users un-dismiss a pair. */
export async function unmarkNotDuplicate(productAID: string, productBID: string): Promise<void> {
  return del<void>('/products/not-duplicate-pairs', {
    product_a_id: productAID,
    product_b_id: productBID,
  })
}

/**
 * Thin wrapper over the existing /products/merge endpoint; we keep it here
 * so the dupe UI can use a single named hook (useMergeProducts) without
 * reaching into the general products.ts module. The underlying handler is
 * exactly the merge used by ProductDetail — this avoids duplicating logic.
 */
async function mergeProductsRaw(keepID: string, mergeID: string): Promise<void> {
  return post<void>('/products/merge', { keep_id: keepID, merge_id: mergeID })
}

// --- React Query hooks ---

const duplicateCandidatesKey = (params?: DuplicateCandidatesParams) =>
  ['duplicate-candidates', params ?? {}] as const

export function useDuplicateCandidates(params: DuplicateCandidatesParams = {}, enabled = true) {
  return useQuery({
    queryKey: duplicateCandidatesKey(params),
    queryFn: () => getDuplicateCandidates(params),
    enabled,
    // Pairs are slow to recompute (O(n^2)) and change rarely; fetch on mount
    // and on mutation, not on every tab focus.
    refetchOnWindowFocus: false,
  })
}

/** Invalidates the candidates list so the dismissed pair drops away. */
export function useMarkNotDuplicate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ a, b }: { a: string; b: string }) => markNotDuplicate(a, b),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['duplicate-candidates'] })
    },
  })
}

/** Counterpart to useMarkNotDuplicate. */
export function useUnmarkNotDuplicate() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ a, b }: { a: string; b: string }) => unmarkNotDuplicate(a, b),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['duplicate-candidates'] })
    },
  })
}

/**
 * Merge mutation scoped to the dupe UI. On success we also invalidate the
 * general products query so the list page reflects the deletion without a
 * reload.
 */
export function useMergeProducts() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ keepId, mergeId }: { keepId: string; mergeId: string }) =>
      mergeProductsRaw(keepId, mergeId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['duplicate-candidates'] })
      void qc.invalidateQueries({ queryKey: ['products'] })
    },
  })
}
