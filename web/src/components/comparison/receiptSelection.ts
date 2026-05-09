import type { Receipt } from '@/types'

export const RECEIPT_COMPARE_LIMIT = 12

export function isReceiptComparable(receipt: Pick<Receipt, 'status'>): boolean {
  return receipt.status === 'matched' || receipt.status === 'reviewed'
}

export function dedupeReceiptIds(ids: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const id of ids) {
    const trimmed = id.trim()
    if (!trimmed || seen.has(trimmed)) continue
    seen.add(trimmed)
    out.push(trimmed)
  }
  return out
}
