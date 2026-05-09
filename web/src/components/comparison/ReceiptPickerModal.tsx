import { useEffect, useMemo, useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { Badge } from '@/components/ui/Badge'
import { ReceiptRowCheckbox } from '@/components/comparison/ReceiptRowCheckbox'
import {
  RECEIPT_COMPARE_LIMIT,
  dedupeReceiptIds,
  isReceiptComparable,
} from '@/components/comparison/receiptSelection'
import type { Receipt } from '@/types'

interface ReceiptPickerModalProps {
  open: boolean
  receipts: Receipt[]
  selectedIds: string[]
  onClose: () => void
  onConfirm: (ids: string[]) => void
}

function formatCurrency(value: string | null): string {
  if (!value) return '\u2014'
  const num = Number(value)
  if (!Number.isFinite(num)) return '\u2014'
  return `$${num.toFixed(2)}`
}

function statusVariant(status: Receipt['status']) {
  if (status === 'reviewed') return 'success'
  if (status === 'matched') return 'neutral'
  if (status === 'error') return 'error'
  return 'warning'
}

export function ReceiptPickerModal({
  open,
  receipts,
  selectedIds,
  onClose,
  onConfirm,
}: ReceiptPickerModalProps) {
  const [draftIds, setDraftIds] = useState<Set<string>>(new Set(selectedIds))

  useEffect(() => {
    if (open) {
      setDraftIds(new Set(dedupeReceiptIds(selectedIds).slice(0, RECEIPT_COMPARE_LIMIT)))
    }
  }, [open, selectedIds])

  const sortedReceipts = useMemo(
    () =>
      [...receipts].sort(
        (a, b) =>
          new Date(b.receipt_date).getTime() - new Date(a.receipt_date).getTime(),
      ),
    [receipts],
  )

  const eligibleIds = useMemo(
    () =>
      sortedReceipts
        .filter(isReceiptComparable)
        .slice(0, RECEIPT_COMPARE_LIMIT)
        .map((receipt) => receipt.id),
    [sortedReceipts],
  )

  const allEligibleSelected =
    eligibleIds.length > 0 && eligibleIds.every((id) => draftIds.has(id))
  const selectedCount = draftIds.size
  const cappedNotice = sortedReceipts.filter(isReceiptComparable).length > RECEIPT_COMPARE_LIMIT

  function setReceiptChecked(id: string, checked: boolean) {
    setDraftIds((current) => {
      const next = new Set(current)
      if (checked) {
        if (next.size < RECEIPT_COMPARE_LIMIT) next.add(id)
      } else {
        next.delete(id)
      }
      return next
    })
  }

  function toggleAll(checked: boolean) {
    setDraftIds((current) => {
      const next = new Set(current)
      for (const id of eligibleIds) {
        if (checked) next.add(id)
        else next.delete(id)
      }
      return next
    })
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Change Receipts"
      footer={
        <>
          <Button variant="secondary" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="sm"
            disabled={selectedCount < 2}
            onClick={() => onConfirm(Array.from(draftIds).slice(0, RECEIPT_COMPARE_LIMIT))}
          >
            Compare {Math.min(selectedCount, RECEIPT_COMPARE_LIMIT)} receipts
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        {cappedNotice && (
          <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-caption text-amber-900">
            Select all uses the first {RECEIPT_COMPARE_LIMIT} comparable receipts.
          </div>
        )}

        <label className="flex items-center gap-3 rounded-lg bg-neutral-50 px-3 py-2 text-caption font-medium text-neutral-900">
          <input
            type="checkbox"
            checked={allEligibleSelected}
            onChange={(event) => toggleAll(event.target.checked)}
            className="h-4 w-4 accent-brand"
          />
          Select first {Math.min(eligibleIds.length, RECEIPT_COMPARE_LIMIT)} comparable receipts
        </label>

        <div className="max-h-[52vh] overflow-y-auto rounded-lg border border-neutral-200">
          {sortedReceipts.map((receipt) => {
            const comparable = isReceiptComparable(receipt)
            const checked = draftIds.has(receipt.id)
            const atLimit = !checked && selectedCount >= RECEIPT_COMPARE_LIMIT
            const disabled = !comparable || atLimit
            const title = !comparable
              ? 'Only matched or reviewed receipts can be compared'
              : atLimit
                ? `Comparison is limited to ${RECEIPT_COMPARE_LIMIT} receipts`
                : undefined
            return (
              <div
                key={receipt.id}
                className="grid grid-cols-[24px_1fr_auto] items-center gap-3 border-b border-neutral-200 px-3 py-2 last:border-b-0"
              >
                <ReceiptRowCheckbox
                  checked={checked}
                  disabled={disabled}
                  title={title}
                  label={`Select receipt from ${receipt.store_name ?? 'Unknown store'} on ${receipt.receipt_date}`}
                  onChange={(nextChecked) => setReceiptChecked(receipt.id, nextChecked)}
                />
                <div className="min-w-0">
                  <p className="truncate text-caption font-medium text-neutral-900">
                    {receipt.store_name ?? 'Unknown store'}
                  </p>
                  <p className="truncate text-small text-neutral-500">
                    {receipt.receipt_date} · {formatCurrency(receipt.total)}
                  </p>
                </div>
                <Badge variant={statusVariant(receipt.status)}>
                  {receipt.status}
                </Badge>
              </div>
            )
          })}
        </div>
      </div>
    </Modal>
  )
}
