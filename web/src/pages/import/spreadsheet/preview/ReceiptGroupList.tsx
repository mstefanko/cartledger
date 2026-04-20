import { useMemo } from 'react'
import type { PreviewResponse, PreviewRow } from '@/api/import-spreadsheet'
import type { ImportConfig } from '../SpreadsheetImportTab'
import ReceiptGroupCard from './ReceiptGroupCard'

interface ReceiptGroupListProps {
  preview: PreviewResponse | null
  config: ImportConfig
  onToggleInclude: (groupId: string, include: boolean) => void
  onMarkDuplicate: (groupId: string, isDuplicate: boolean) => void
  onSkipRow: (rowIdx: number) => void
}

function ReceiptGroupList({
  preview,
  config,
  onToggleInclude,
  onMarkDuplicate,
  onSkipRow,
}: ReceiptGroupListProps) {
  const rowsByGroup = useMemo(() => {
    const m = new Map<string, PreviewRow[]>()
    if (!preview) return m
    for (const r of preview.rows) {
      const arr = m.get(r.receipt_group_id) ?? []
      arr.push(r)
      m.set(r.receipt_group_id, arr)
    }
    return m
  }, [preview])

  if (!preview) {
    return (
      <div className="bg-white rounded-2xl border border-neutral-200 p-8 text-center text-caption text-neutral-400">
        Adjust the mapping above to generate a preview.
      </div>
    )
  }

  if (preview.groups.length === 0) {
    return (
      <div className="bg-white rounded-2xl border border-neutral-200 p-8 text-center text-caption text-neutral-500">
        No receipts formed with the current mapping. Try a different grouping strategy or check the Date and Store columns.
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {preview.groups.map((g) => (
        <ReceiptGroupCard
          key={g.group_id}
          group={g}
          rows={rowsByGroup.get(g.group_id) ?? []}
          config={config}
          onToggleInclude={onToggleInclude}
          onMarkDuplicate={onMarkDuplicate}
          onSkipRow={onSkipRow}
        />
      ))}
      {preview.rows.length < preview.summary.items && (
        <p className="text-small text-neutral-400 text-center pt-2">
          Showing first {preview.rows.length} rows of {preview.summary.items}. Commit processes all rows.
        </p>
      )}
    </div>
  )
}

export default ReceiptGroupList
