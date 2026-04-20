import { Link, useLocation, Navigate } from 'react-router-dom'
import { Button } from '@/components/ui/Button'
import type { CommitResponse } from '@/api/import-spreadsheet'

interface LocationState {
  result: CommitResponse
  importId: string
}

function SpreadsheetCommitResult() {
  const location = useLocation()
  const state = location.state as LocationState | null

  if (!state?.result) {
    return <Navigate to="/import?tab=spreadsheet" replace />
  }

  const { result } = state
  const hasErrors = (result.errors?.length ?? 0) > 0

  return (
    <div className="py-8 max-w-3xl">
      <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight mb-2">
        Import complete
      </h1>
      <p className="text-body text-neutral-500 mb-6">
        {result.receipts_created} receipt{result.receipts_created === 1 ? '' : 's'} and{' '}
        {result.line_items_created} line item{result.line_items_created === 1 ? '' : 's'} added.
      </p>

      <div className="bg-white rounded-2xl shadow-subtle p-6 space-y-3">
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <Stat label="Receipts" value={result.receipts_created} />
          <Stat label="Line items" value={result.line_items_created} />
          <Stat label="Need review" value={result.unmatched} />
          <Stat label="Duplicates skipped" value={result.duplicates_skipped} />
        </div>

        <div className="flex flex-wrap gap-2 pt-3">
          <Link to="/analytics">
            <Button variant="outlined" size="sm">View analytics</Button>
          </Link>
          {result.unmatched > 0 && (
            <Link to={`/review?batch=${encodeURIComponent(result.batch_id)}`}>
              <Button variant="subtle" size="sm">Review {result.unmatched} item{result.unmatched === 1 ? '' : 's'}</Button>
            </Link>
          )}
          <Link to="/import?tab=spreadsheet">
            <Button variant="secondary" size="sm">Import another file</Button>
          </Link>
        </div>
      </div>

      {hasErrors && (
        <div className="mt-6 bg-expensive-subtle border border-expensive/30 rounded-2xl p-5">
          <div className="font-display text-feature font-semibold text-expensive mb-2">
            {result.errors!.length} group{result.errors!.length === 1 ? '' : 's'} failed
          </div>
          <ul className="space-y-1.5">
            {result.errors!.map((e, i) => (
              <li key={i} className="text-caption text-neutral-900">
                <span className="text-small font-mono text-neutral-500 mr-2">
                  {e.fatal ? 'FATAL' : 'warn'}
                </span>
                <span className="text-small font-mono text-neutral-500 mr-2">
                  {e.group_id.slice(0, 8)}
                </span>
                {e.message}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <div className="text-small text-neutral-400 uppercase tracking-wide">{label}</div>
      <div className="font-display text-feature font-semibold text-neutral-900">{value}</div>
    </div>
  )
}

export default SpreadsheetCommitResult
