import { useMemo, useState } from 'react'
import { Badge } from '@/components/ui/Badge'
import type { PreviewGroup, PreviewRow } from '@/api/import-spreadsheet'
import type { ImportConfig } from '../SpreadsheetImportTab'
import ParsedDateCell from './ParsedDateCell'

interface ReceiptGroupCardProps {
  group: PreviewGroup
  rows: PreviewRow[]
  config: ImportConfig
  onToggleInclude: (groupId: string, include: boolean) => void
  onMarkDuplicate: (groupId: string, isDuplicate: boolean) => void
  onSkipRow: (rowIdx: number) => void
}

function formatMoney(cents: number): string {
  const sign = cents < 0 ? '-' : ''
  const abs = Math.abs(cents)
  return `${sign}$${(abs / 100).toFixed(2)}`
}

function ReceiptGroupCard({
  group,
  rows,
  config,
  onToggleInclude,
  onMarkDuplicate,
  onSkipRow,
}: ReceiptGroupCardProps) {
  const [expanded, setExpanded] = useState(true)
  const override = config.groupOverrides[group.group_id] ?? {}
  const included = override.include !== false
  const isDuplicate = override.isDuplicate === true

  const hasErrors = useMemo(
    () => rows.some((r) => r.cell_errors && Object.keys(r.cell_errors).length > 0),
    [rows],
  )

  return (
    <div className="bg-white rounded-2xl border border-neutral-200 overflow-hidden">
      {/* Header */}
      <div className="px-4 py-3 flex items-center gap-3 flex-wrap">
        <button
          type="button"
          onClick={() => setExpanded((x) => !x)}
          aria-expanded={expanded}
          aria-label={expanded ? 'Collapse group' : 'Expand group'}
          className="text-neutral-400 hover:text-neutral-900 cursor-pointer w-5 h-5 inline-flex items-center justify-center"
        >
          {expanded ? '−' : '+'}
        </button>
        <div className="font-display text-body-medium font-semibold text-neutral-900 truncate">
          {group.store || <span className="text-neutral-400 italic">unspecified store</span>}
        </div>
        <span className="text-caption text-neutral-400">{group.date || '—'}</span>
        <span className="text-caption font-medium text-neutral-900 ml-auto">
          {formatMoney(group.total_cents)}
        </span>
        {group.split_suggested && <Badge variant="warning">Split suggested</Badge>}
        {group.duplicate_of_receipt_id && (
          <Badge variant="warning">Duplicate of #{group.duplicate_of_receipt_id.slice(0, 6)}</Badge>
        )}
        {hasErrors && <Badge variant="error">Errors</Badge>}
        <label className="inline-flex items-center gap-2 text-caption text-neutral-600 cursor-pointer ml-2">
          <input
            type="checkbox"
            checked={included}
            onChange={(e) => onToggleInclude(group.group_id, e.target.checked)}
            className="accent-brand"
          />
          Include
        </label>
      </div>

      {/* Duplicate reconciliation row */}
      {group.duplicate_of_receipt_id && (
        <div className="px-4 py-2 bg-amber-50 border-t border-amber-100 flex items-center gap-2 flex-wrap text-caption">
          <span className="text-amber-800 font-medium">Matches an existing receipt.</span>
          <button
            type="button"
            onClick={() => {
              onMarkDuplicate(group.group_id, true)
              onToggleInclude(group.group_id, true)
            }}
            className={[
              'px-2.5 py-1 rounded-md border cursor-pointer',
              isDuplicate
                ? 'border-brand bg-brand text-white'
                : 'border-neutral-200 bg-white text-neutral-700 hover:bg-neutral-50',
            ].join(' ')}
          >
            Keep both
          </button>
          <button
            type="button"
            onClick={() => {
              onMarkDuplicate(group.group_id, false)
              onToggleInclude(group.group_id, false)
            }}
            className={[
              'px-2.5 py-1 rounded-md border cursor-pointer',
              !included
                ? 'border-brand bg-brand text-white'
                : 'border-neutral-200 bg-white text-neutral-700 hover:bg-neutral-50',
            ].join(' ')}
          >
            Skip
          </button>
          <button
            type="button"
            disabled
            title="Coming soon"
            className="px-2.5 py-1 rounded-md border border-neutral-200 bg-neutral-50 text-neutral-400 cursor-not-allowed"
          >
            Replace existing
          </button>
        </div>
      )}

      {/* Body */}
      {expanded && (
        <div className="px-4 py-3 border-t border-neutral-100">
          <table className="w-full text-caption">
            <thead>
              <tr className="text-small text-neutral-400 uppercase tracking-wide text-left">
                <th className="pb-1.5 font-medium w-6"></th>
                <th className="pb-1.5 font-medium">Item</th>
                <th className="pb-1.5 font-medium">Date</th>
                <th className="pb-1.5 font-medium">Qty</th>
                <th className="pb-1.5 font-medium text-right">Total</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => {
                const errs = r.cell_errors ?? {}
                const dateErr = errs['date']
                const itemErr = errs['item']
                const qtyErr = errs['qty']
                const totErr = errs['total_price']
                const skipped = config.skipRowIndices.includes(r.row_index)
                return (
                  <tr
                    key={r.row_index}
                    className={[
                      'border-t border-neutral-100',
                      skipped ? 'opacity-40' : '',
                    ].join(' ')}
                  >
                    <td className="py-1.5 align-top">
                      <input
                        type="checkbox"
                        aria-label={`Skip row ${r.row_index}`}
                        checked={skipped}
                        onChange={() => onSkipRow(r.row_index)}
                        className="accent-expensive"
                      />
                    </td>
                    <td className={['py-1.5 align-top', itemErr ? 'text-expensive' : 'text-neutral-900'].join(' ')} title={itemErr}>
                      {r.parsed.item || <span className="text-neutral-400 italic">—</span>}
                    </td>
                    <td className="py-1.5 align-top">
                      <ParsedDateCell
                        raw={rawDate(r)}
                        parsed={r.parsed.date}
                        error={dateErr}
                      />
                    </td>
                    <td className={['py-1.5 align-top', qtyErr ? 'text-expensive' : 'text-neutral-600'].join(' ')} title={qtyErr}>
                      {r.parsed.qty || 1} {r.parsed.unit}
                    </td>
                    <td
                      className={[
                        'py-1.5 align-top text-right font-medium',
                        totErr ? 'text-expensive' : 'text-neutral-900',
                      ].join(' ')}
                      title={totErr}
                    >
                      {formatMoney(r.parsed.total_cents)}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function rawDate(r: PreviewRow): string {
  // The row's raw[] lines up with columns, but the frontend doesn't know which
  // column maps to date here — we surface the parsed.date backstop when the
  // raw shape is ambiguous. In practice the raw string shows through in the
  // ParsedDateCell via parsed.date != '' success path.
  return r.raw.find(Boolean) ?? ''
}

export default ReceiptGroupCard
