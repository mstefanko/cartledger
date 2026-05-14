import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import type { ManualLineItemInput } from '@/api/receipts'

export type ManualLineItemGridRow = ManualLineItemInput & { _key: string }

interface ManualLineItemGridProps {
  rows: ManualLineItemGridRow[]
  onRowsChange: (rows: ManualLineItemGridRow[]) => void
  disabled?: boolean
  addButtonLabel?: string
}

function createManualLineItemRow(
  defaults: Partial<ManualLineItemInput> = {},
): ManualLineItemGridRow {
  return {
    _key: crypto.randomUUID(),
    raw_name: '',
    total_price: '',
    ...defaults,
  }
}

function createManualLineItemRows(count: number): ManualLineItemGridRow[] {
  const safeCount = Math.max(1, Math.min(Math.floor(count), 100))
  return Array.from({ length: safeCount }, () => createManualLineItemRow())
}

function manualLineItemRowsAreComplete(rows: ManualLineItemGridRow[]): boolean {
  return (
    rows.length > 0 &&
    rows.every(
      (row) =>
        row.raw_name.trim().length > 0 &&
        row.total_price.trim().length > 0 &&
        ((row.pack_quantity_override?.trim() ?? '') === '' ? (row.pack_unit_override?.trim() ?? '') === '' : (row.pack_unit_override?.trim() ?? '') !== ''),
    )
  )
}

function optionalText(value: string | undefined): string | undefined {
  const trimmed = value?.trim() ?? ''
  return trimmed.length > 0 ? trimmed : undefined
}

function toManualLineItemInputs(
  rows: ManualLineItemGridRow[],
): ManualLineItemInput[] {
  return rows.map(({ _key, ...row }) => ({
    raw_name: row.raw_name.trim(),
    product_id: optionalText(row.product_id),
    quantity: optionalText(row.quantity),
    unit: optionalText(row.unit),
    unit_price: optionalText(row.unit_price),
    total_price: row.total_price.trim(),
    pack_quantity_override: optionalText(row.pack_quantity_override),
    pack_unit_override: optionalText(row.pack_unit_override),
    pack_override_source: optionalText(row.pack_quantity_override) && optionalText(row.pack_unit_override) ? 'user' : undefined,
  }))
}

function ManualLineItemGrid({
  rows,
  onRowsChange,
  disabled = false,
  addButtonLabel = 'Add item',
}: ManualLineItemGridProps) {
  const updateRow = (key: string, patch: Partial<ManualLineItemGridRow>) =>
    onRowsChange(rows.map((row) => (row._key === key ? { ...row, ...patch } : row)))

  const addRow = () => onRowsChange([...rows, createManualLineItemRow()])

  const removeRow = (key: string) => {
    if (rows.length <= 1) return
    onRowsChange(rows.filter((row) => row._key !== key))
  }

  return (
    <div>
      <div className="hidden sm:grid grid-cols-12 gap-2 px-1">
        <span className="col-span-4 text-small font-medium text-neutral-400 uppercase tracking-wide">
          Item
        </span>
        <span className="col-span-3 text-small font-medium text-neutral-400 uppercase tracking-wide">
          Purchased
        </span>
        <span className="col-span-2 text-small font-medium text-neutral-400 uppercase tracking-wide">
          Package Contents
        </span>
        <span className="col-span-2 text-small font-medium text-neutral-400 uppercase tracking-wide">
          Price
        </span>
        <span className="col-span-1" />
      </div>

      <div className="mt-1 space-y-2">
        {rows.map((row) => (
          <div
            key={row._key}
            className="grid grid-cols-12 gap-2 items-end p-3 rounded-xl bg-neutral-50 border border-neutral-200"
          >
            <div className="col-span-12 sm:col-span-4">
              <Input
                aria-label="Item name"
                placeholder="e.g. Whole Milk"
                value={row.raw_name}
                disabled={disabled}
                onChange={(event) => updateRow(row._key, { raw_name: event.target.value })}
              />
            </div>
            <div className="col-span-3 sm:col-span-1">
              <Input
                aria-label="Purchased quantity"
                inputMode="decimal"
                placeholder="1"
                value={row.quantity ?? ''}
                disabled={disabled}
                onChange={(event) => updateRow(row._key, { quantity: event.target.value })}
              />
            </div>
            <div className="col-span-3 sm:col-span-2">
              <Input
                aria-label="Purchased unit"
                placeholder="ea"
                value={row.unit ?? ''}
                disabled={disabled}
                onChange={(event) => updateRow(row._key, { unit: event.target.value })}
              />
            </div>
            <div className="col-span-4 sm:col-span-2 grid grid-cols-2 gap-1">
              <Input
                aria-label="Package contents quantity"
                inputMode="decimal"
                placeholder="12"
                value={row.pack_quantity_override ?? ''}
                disabled={disabled}
                onChange={(event) => updateRow(row._key, { pack_quantity_override: event.target.value })}
              />
              <Input
                aria-label="Package contents unit"
                placeholder="oz"
                value={row.pack_unit_override ?? ''}
                disabled={disabled}
                onChange={(event) => updateRow(row._key, { pack_unit_override: event.target.value })}
              />
            </div>
            <div className="col-span-3 sm:col-span-2">
              <Input
                aria-label="Total price"
                inputMode="decimal"
                placeholder="0.00"
                value={row.total_price}
                disabled={disabled}
                onChange={(event) => updateRow(row._key, { total_price: event.target.value })}
              />
            </div>
            <div className="col-span-1 flex items-end pb-0.5">
              <Button
                type="button"
                variant="subtle"
                size="sm"
                aria-label="Remove item"
                onClick={() => removeRow(row._key)}
                disabled={disabled || rows.length === 1}
                className="h-9 w-9 px-0"
              >
                <Trash2 className="h-4 w-4" aria-hidden="true" />
              </Button>
            </div>
          </div>
        ))}
      </div>

      <div className="mt-3">
        <Button
          type="button"
          variant="outlined"
          size="sm"
          onClick={addRow}
          disabled={disabled}
          className="gap-1.5"
        >
          <Plus className="h-4 w-4" aria-hidden="true" />
          {addButtonLabel}
        </Button>
      </div>
    </div>
  )
}

ManualLineItemGrid.displayName = 'ManualLineItemGrid'

export {
  ManualLineItemGrid,
  createManualLineItemRow,
  createManualLineItemRows,
  manualLineItemRowsAreComplete,
  toManualLineItemInputs,
}
