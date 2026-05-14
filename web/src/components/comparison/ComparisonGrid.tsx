import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Star } from 'lucide-react'
import { updateLineItem } from '@/api/receipts'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import type { CompareAppearance, CompareLineChoice, CompareProduct, CompareReceipt } from '@/api/comparison'

interface ComparisonGridProps {
  receipts: CompareReceipt[]
  products: CompareProduct[]
}

function formatCurrency(value: string | null | undefined): string {
  if (value == null) return '\u2014'
  const num = Number(value)
  if (!Number.isFinite(num)) return '\u2014'
  return `$${num.toFixed(2)}`
}

function formatUnit(unit: string): string {
  return unit.replace(/_/g, ' ')
}

function formatPurchased(appearance: CompareAppearance): string | null {
  if (appearance.quantity && appearance.unit) {
    return `${appearance.quantity} ${formatUnit(appearance.unit)}`
  }
  if (appearance.quantity) return appearance.quantity
  return null
}

function ReceiptColumnHeader({ receipt }: { receipt: CompareReceipt }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="truncate text-caption font-semibold text-neutral-900">
        {receipt.store_name ?? 'Unknown store'}
      </span>
      <span className="truncate text-small text-neutral-500">
        {receipt.receipt_date} · {formatCurrency(receipt.total)}
      </span>
    </div>
  )
}

function productDetailPath(product: CompareProduct): string {
  if (product.product_group_id) {
    return `/product-groups/${product.product_group_id}`
  }
  return `/products/${product.product_id}`
}

function ComparisonCell({
  appearance,
  isBest,
  onSetSize,
}: {
  appearance?: CompareAppearance
  isBest: boolean
  onSetSize: (appearance: CompareAppearance) => void
}) {
  if (!appearance) {
    return (
      <span className="text-body text-neutral-300" aria-label="No matching item">
        {'\u2014'}
      </span>
    )
  }

  const purchased = formatPurchased(appearance)
  const unitPrice =
    appearance.size_known && appearance.normalized_price && appearance.normalized_unit
      ? `${formatCurrency(appearance.normalized_price)}/${formatUnit(appearance.normalized_unit)}`
      : null

  return (
    <div className="relative min-h-[72px] pr-6">
      {isBest && (
        <Star
          aria-label="Best unit price"
          className="absolute right-0 top-0 h-4 w-4 fill-success text-success"
        />
      )}
      <div className="flex flex-wrap items-baseline gap-x-1 gap-y-0.5">
        <span className="font-medium tabular-nums text-neutral-900">
          {formatCurrency(appearance.total_price)}
        </span>
        {purchased && (
          <span className="max-w-full break-words text-small text-neutral-500">
            Purchased {purchased}
          </span>
        )}
      </div>
      {unitPrice ? (
        <div className="mt-1 break-words text-small font-medium tabular-nums text-success-dark">
          {unitPrice}
        </div>
      ) : (
        <button
          type="button"
          className="mt-2"
          onClick={() => onSetSize(appearance)}
          aria-label={`Set package contents for ${appearance.raw_name}`}
        >
          <Badge variant="warning">contents needed</Badge>
        </button>
      )}
      <div className="mt-1 line-clamp-2 break-words text-small text-neutral-400">
        {appearance.raw_name}
      </div>
    </div>
  )
}

export function ComparisonGrid({ receipts, products }: ComparisonGridProps) {
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<CompareAppearance | null>(null)
  const [linePicker, setLinePicker] = useState<CompareAppearance | null>(null)
  const [packQuantity, setPackQuantity] = useState('')
  const [packUnit, setPackUnit] = useState('')

  const saveSizeMutation = useMutation({
    mutationFn: () => {
      if (!editing) throw new Error('missing line item')
      return updateLineItem(editing.receipt_id, editing.line_item_id, {
        pack_quantity_override: packQuantity,
        pack_unit_override: packUnit,
        pack_override_source: 'user',
      })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['receipts', 'compare'] })
      queryClient.invalidateQueries({ queryKey: ['receipt'] })
      queryClient.invalidateQueries({ queryKey: ['product-detail'] })
      setEditing(null)
      setLinePicker(null)
      setPackQuantity('')
      setPackUnit('')
    },
  })

  const openSizeEditorForLine = (appearance: CompareAppearance, line?: CompareLineChoice) => {
    setEditing({
      ...appearance,
      line_item_id: line?.line_item_id ?? appearance.line_item_id,
      raw_name: line?.raw_name ?? appearance.raw_name,
      quantity: line?.quantity ?? appearance.quantity,
      unit: line?.unit ?? appearance.unit,
      total_price: line?.total_price ?? appearance.total_price,
      unit_price: line?.unit_price ?? appearance.unit_price,
    })
    setPackQuantity('')
    setPackUnit('')
  }

  const openSizeEditor = (appearance: CompareAppearance) => {
    if (appearance.lines && appearance.lines.length > 1) {
      setLinePicker(appearance)
      return
    }
    openSizeEditorForLine(appearance)
  }

  return (
    <>
      <div className="overflow-x-auto rounded-lg border border-neutral-200 bg-white">
        <table className="min-w-full border-collapse">
          <thead className="bg-neutral-50">
            <tr>
              <th className="sticky left-0 z-20 min-w-[190px] max-w-[220px] bg-neutral-50 px-3 py-2 text-left text-caption font-semibold text-neutral-600 border-b border-r border-neutral-200">
                Product
              </th>
              {receipts.map((receipt) => (
                <th
                  key={receipt.id}
                  className="min-w-[168px] px-3 py-2 text-left border-b border-neutral-200"
                >
                  <ReceiptColumnHeader receipt={receipt} />
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {products.map((product) => {
              const appearancesByReceipt = new Map(
                product.appearances.map((appearance) => [appearance.receipt_id, appearance]),
              )
              return (
                <tr key={product.comparison_key} className="align-top">
                  <th className="sticky left-0 z-10 min-w-[190px] max-w-[220px] bg-white px-3 py-3 text-left border-b border-r border-neutral-200">
                    <div className="flex min-w-0 flex-col gap-1">
                      <Link
                        to={productDetailPath(product)}
                        className="break-words text-body-medium font-semibold text-neutral-900 hover:text-brand"
                      >
                        {product.name}
                      </Link>
                      {product.category && (
                        <Badge variant="neutral" className="w-fit">
                          {product.category}
                        </Badge>
                      )}
                      <span className="text-small text-neutral-400">
                        {product.appearances.length} receipt
                        {product.appearances.length === 1 ? '' : 's'}
                      </span>
                    </div>
                  </th>
                  {receipts.map((receipt) => {
                    const appearance = appearancesByReceipt.get(receipt.id)
                    return (
                      <td
                        key={receipt.id}
                        className="min-w-[168px] px-3 py-3 text-left border-b border-neutral-200"
                      >
                        <ComparisonCell
                          appearance={appearance}
                          isBest={
                            !!appearance &&
                            product.best_appearance_id === appearance.line_item_id
                          }
                          onSetSize={openSizeEditor}
                        />
                      </td>
                    )
                  })}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      <Modal
        open={linePicker !== null}
        onClose={() => setLinePicker(null)}
        title="Choose Line"
      >
        <div className="space-y-3">
          {(linePicker?.lines ?? []).map((line) => {
            const purchased = line.quantity
              ? `${line.quantity}${line.unit ? ` ${formatUnit(line.unit)}` : ''}`
              : null
            return (
              <button
                key={line.line_item_id}
                type="button"
                className="w-full rounded-lg border border-neutral-200 px-3 py-2 text-left hover:border-brand hover:bg-neutral-50"
                onClick={() => {
                  if (!linePicker) return
                  openSizeEditorForLine(linePicker, line)
                  setLinePicker(null)
                }}
              >
                <span className="block text-body-medium text-neutral-900">{line.raw_name}</span>
                <span className="mt-1 block text-caption text-neutral-500">
                  {[purchased ? `Purchased ${purchased}` : null, formatCurrency(line.total_price)].filter(Boolean).join(' · ')}
                </span>
              </button>
            )
          })}
        </div>
      </Modal>

      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title="Package Contents"
        footer={(
          <>
            <Button variant="secondary" size="sm" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button
              size="sm"
              onClick={() => saveSizeMutation.mutate()}
              disabled={!packQuantity.trim() || !packUnit.trim() || saveSizeMutation.isPending}
            >
              {saveSizeMutation.isPending ? 'Saving...' : 'Save contents'}
            </Button>
          </>
        )}
      >
        <div className="space-y-4">
          <div>
            <p className="text-body-medium text-neutral-900">{editing?.raw_name}</p>
            <p className="text-caption text-neutral-400">
              {editing ? `${formatCurrency(editing.total_price)} on this receipt` : ''}
            </p>
          </div>
          {editing && (
            <div className="rounded-xl bg-neutral-50 px-3 py-2">
              <span className="block text-small font-medium text-neutral-400">Purchased on receipt</span>
              <span className="mt-0.5 block text-caption font-semibold text-neutral-900">
                {formatPurchased(editing) ?? '\u2014'}
              </span>
            </div>
          )}
          <div className="grid grid-cols-[minmax(0,7rem)_minmax(0,1fr)] gap-2">
            <label className="block">
              <span className="mb-1 block text-small font-medium text-neutral-400">Contents Qty</span>
              <input
                type="number"
                min="0"
                step="any"
                value={packQuantity}
                onChange={(e) => setPackQuantity(e.target.value)}
                className="w-full px-3 py-2 text-caption border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
                placeholder="32"
              />
            </label>
            <label className="block">
              <span className="mb-1 block text-small font-medium text-neutral-400">Contents Unit</span>
              <input
                type="text"
                value={packUnit}
                onChange={(e) => setPackUnit(e.target.value)}
                className="w-full px-3 py-2 text-caption border border-neutral-200 rounded-xl focus:outline-none focus:ring-2 focus:ring-brand focus:border-transparent"
                placeholder="oz"
              />
            </label>
          </div>
          <p className="text-caption text-neutral-400">
            Receipt-only override
          </p>
        </div>
      </Modal>
    </>
  )
}
