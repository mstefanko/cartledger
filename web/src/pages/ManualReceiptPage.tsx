import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { createManualReceipt, type ManualLineItemInput } from '@/api/receipts'
import { listStores } from '@/api/stores'

type Row = ManualLineItemInput & { _key: string }

function emptyRow(): Row {
  return { _key: crypto.randomUUID(), raw_name: '', total_price: '' }
}

export default function ManualReceiptPage() {
  const navigate = useNavigate()
  const qc = useQueryClient()

  const { data: stores = [] } = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  const [storeId, setStoreId] = useState<string>('')
  const [receiptDate, setReceiptDate] = useState<string>(
    new Date().toISOString().slice(0, 10),
  )
  const [subtotal, setSubtotal] = useState('')
  const [tax, setTax] = useState('')
  const [total, setTotal] = useState('')
  const [rows, setRows] = useState<Row[]>([emptyRow()])
  const [submitError, setSubmitError] = useState<string | null>(null)

  const mutation = useMutation({
    mutationFn: createManualReceipt,
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ['receipts'] })
      navigate(`/receipts/${res.id}`)
    },
    onError: (err: Error) => setSubmitError(err.message),
  })

  const isValid =
    rows.length > 0 &&
    rows.every(
      (r) => r.raw_name.trim().length > 0 && r.total_price.trim().length > 0,
    ) &&
    receiptDate.length > 0

  const handleSubmit = () => {
    setSubmitError(null)
    mutation.mutate({
      store_id: storeId || undefined,
      receipt_date: receiptDate,
      subtotal: subtotal || undefined,
      tax: tax || undefined,
      total: total || undefined,
      items: rows.map(({ _key, ...rest }) => rest),
    })
  }

  const updateRow = (key: string, patch: Partial<Row>) =>
    setRows((rs) => rs.map((r) => (r._key === key ? { ...r, ...patch } : r)))

  const addRow = () => setRows((rs) => [...rs, emptyRow()])

  const removeRow = (key: string) =>
    setRows((rs) => (rs.length > 1 ? rs.filter((r) => r._key !== key) : rs))

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

      {/* Column headers */}
      <div className="mt-3 hidden sm:grid grid-cols-12 gap-2 px-1">
        <span className="col-span-5 text-small font-medium text-neutral-400 uppercase tracking-wide">Item</span>
        <span className="col-span-2 text-small font-medium text-neutral-400 uppercase tracking-wide">Qty</span>
        <span className="col-span-2 text-small font-medium text-neutral-400 uppercase tracking-wide">Unit</span>
        <span className="col-span-2 text-small font-medium text-neutral-400 uppercase tracking-wide">Price</span>
        <span className="col-span-1" />
      </div>

      {/* Item rows */}
      <div className="mt-1 space-y-2">
        {rows.map((r) => (
          <div
            key={r._key}
            className="grid grid-cols-12 gap-2 items-end p-3 rounded-xl bg-neutral-50 border border-neutral-200"
          >
            <div className="col-span-12 sm:col-span-5">
              <Input
                aria-label="Item name"
                placeholder="e.g. Whole Milk"
                value={r.raw_name}
                onChange={(e) => updateRow(r._key, { raw_name: e.target.value })}
              />
            </div>
            <div className="col-span-4 sm:col-span-2">
              <Input
                aria-label="Quantity"
                inputMode="decimal"
                placeholder="1"
                value={r.quantity ?? ''}
                onChange={(e) => updateRow(r._key, { quantity: e.target.value })}
              />
            </div>
            <div className="col-span-4 sm:col-span-2">
              <Input
                aria-label="Unit"
                placeholder="ea"
                value={r.unit ?? ''}
                onChange={(e) => updateRow(r._key, { unit: e.target.value })}
              />
            </div>
            <div className="col-span-3 sm:col-span-2">
              <Input
                aria-label="Total price"
                inputMode="decimal"
                placeholder="0.00"
                value={r.total_price}
                onChange={(e) => updateRow(r._key, { total_price: e.target.value })}
              />
            </div>
            <div className="col-span-1 flex items-end pb-0.5">
              <Button
                variant="subtle"
                size="sm"
                aria-label="Remove item"
                onClick={() => removeRow(r._key)}
                disabled={rows.length === 1}
              >
                ×
              </Button>
            </div>
          </div>
        ))}
      </div>

      {/* Add item */}
      <div className="mt-3">
        <Button variant="outlined" size="sm" onClick={addRow}>
          + Add item
        </Button>
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
