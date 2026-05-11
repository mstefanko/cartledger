import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { createManualReceipt } from '@/api/receipts'
import { listStores } from '@/api/stores'
import {
  ManualLineItemGrid,
  createManualLineItemRow,
  manualLineItemRowsAreComplete,
  toManualLineItemInputs,
  type ManualLineItemGridRow,
} from '@/components/receipts/ManualLineItemGrid'

export default function ManualReceiptPage() {
  const navigate = useNavigate()
  const qc = useQueryClient()

  const { data: stores = [] } = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  const [storeId, setStoreId] = useState<string>('')
  const [receiptDate, setReceiptDate] = useState<string>(
    new Date().toLocaleDateString('en-CA'),
  )
  const [subtotal, setSubtotal] = useState('')
  const [tax, setTax] = useState('')
  const [total, setTotal] = useState('')
  const [rows, setRows] = useState<ManualLineItemGridRow[]>([
    createManualLineItemRow(),
  ])
  const [submitError, setSubmitError] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: createManualReceipt,
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ['receipts'] })
      navigate(`/receipts/${res.id}`)
    },
    onError: (err: Error) => setSubmitError(err.message),
  })

  const isValid = manualLineItemRowsAreComplete(rows) && receiptDate.length > 0

  const handleSubmit = () => {
    setSubmitError(null)
    mutation.mutate({
      store_id: storeId || undefined,
      receipt_date: receiptDate,
      subtotal: subtotal || undefined,
      tax: tax || undefined,
      total: total || undefined,
      items: toManualLineItemInputs(rows),
    })
  }

  return (
    <div className="mx-auto max-w-3xl py-8">
      {/* Page header */}
      <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight">
        New Receipt
      </h1>
      <p className="mt-2 text-body text-neutral-400">
        Enter items from a receipt manually — no photo needed.
      </p>

      {/* Receipt metadata */}
      <div className="mt-6 grid grid-cols-1 sm:grid-cols-2 gap-4">
        {/* Store selector */}
        <div className="flex flex-col gap-1.5">
          <label className="text-caption font-medium text-neutral-900">
            Store
          </label>
          <select
            value={storeId}
            onChange={(e) => setStoreId(e.target.value)}
            className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 bg-white text-body text-neutral-900 focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand transition-colors"
          >
            <option value="">— none —</option>
            {stores.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </div>

        <Input
          type="date"
          label="Receipt date"
          value={receiptDate}
          onChange={(e) => setReceiptDate(e.target.value)}
        />

        <Input
          label="Subtotal"
          inputMode="decimal"
          placeholder="0.00"
          value={subtotal}
          onChange={(e) => setSubtotal(e.target.value)}
        />

        <Input
          label="Tax"
          inputMode="decimal"
          placeholder="0.00"
          value={tax}
          onChange={(e) => setTax(e.target.value)}
        />

        <Input
          label="Total"
          inputMode="decimal"
          placeholder="0.00"
          value={total}
          onChange={(e) => setTotal(e.target.value)}
        />
      </div>

      {/* Items section */}
      <div className="mt-8 flex items-baseline justify-between">
        <h2 className="font-display text-feature font-semibold text-neutral-900">
          Items
        </h2>
        <span className="text-caption text-neutral-400">
          {rows.length} {rows.length === 1 ? 'item' : 'items'}
        </span>
      </div>

      <div className="mt-3">
        <ManualLineItemGrid rows={rows} onRowsChange={setRows} />
      </div>

      {/* Submit error */}
      {submitError && (
        <p className="mt-4 text-body text-expensive" role="alert">
          {submitError}
        </p>
      )}

      {/* Actions */}
      <div className="mt-8 flex gap-3 justify-end">
        <Button variant="subtle" onClick={() => navigate('/receipts')}>
          Cancel
        </Button>
        <Button
          variant="primary"
          onClick={handleSubmit}
          disabled={!isValid || mutation.isPending}
        >
          {mutation.isPending ? 'Saving…' : 'Save receipt'}
        </Button>
      </div>
    </div>
  )
}
