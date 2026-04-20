import { useMemo, useState } from 'react'

import {
  useDuplicateCandidates,
  useMarkNotDuplicate,
  useMergeProducts,
  type DuplicatePair,
  type DuplicatePairSide,
} from '@/api/products-duplicates'
import { Button } from '@/components/ui/Button'

/**
 * Duplicate-products lane for /review?tab=dupes.
 *
 * The backend returns pairs canonicalized A.id < B.id; the UI keeps that
 * ordering for determinism but lets the user pick either side as the
 * "keep" side via the two buttons. "Mark as not duplicates" is the escape
 * hatch for false positives — the pair lands in not_duplicate_pairs and
 * never resurfaces.
 *
 * Pairs are driven entirely off the react-query cache; successful
 * merge/mark invalidates the query which triggers a refetch. We keep a
 * local "dismissed" set so the card animates away instantly without
 * waiting for the roundtrip — the next refetch reconciles.
 */
export default function DuplicatePairsList() {
  const { data, isLoading, isError } = useDuplicateCandidates()
  const [locallyDismissed, setLocallyDismissed] = useState<Set<string>>(new Set())

  const visiblePairs = useMemo(() => {
    if (!data?.pairs) return []
    return data.pairs.filter((p) => !locallyDismissed.has(pairKey(p)))
  }, [data?.pairs, locallyDismissed])

  if (isLoading) {
    return <p className="text-body text-neutral-400 py-4">Loading duplicate candidates…</p>
  }

  if (isError) {
    return (
      <p className="text-body text-neutral-500 py-4">
        Couldn't load duplicate candidates. Try refreshing.
      </p>
    )
  }

  if (!data || data.count === 0 || visiblePairs.length === 0) {
    return (
      <div className="rounded-2xl bg-white shadow-subtle p-5 max-w-2xl">
        <p className="font-display text-feature font-semibold text-neutral-900 mb-1">
          No duplicates detected
        </p>
        <p className="text-body text-neutral-500">
          We'll surface pairs here when two products look like they might be the same.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-4 max-w-3xl">
      <p className="text-caption text-neutral-500">
        {visiblePairs.length} pair{visiblePairs.length === 1 ? '' : 's'} to review
        {data.count > visiblePairs.length ? ` (of ${data.count} total)` : ''}
      </p>
      {visiblePairs.map((pair) => (
        <DuplicatePairCard
          key={pairKey(pair)}
          pair={pair}
          onDone={() =>
            setLocallyDismissed((prev) => {
              const next = new Set(prev)
              next.add(pairKey(pair))
              return next
            })
          }
        />
      ))}
    </div>
  )
}

function pairKey(pair: DuplicatePair): string {
  return `${pair.a.id}|${pair.b.id}`
}

interface DuplicatePairCardProps {
  pair: DuplicatePair
  onDone: () => void
}

function DuplicatePairCard({ pair, onDone }: DuplicatePairCardProps) {
  const merge = useMergeProducts()
  const markNotDup = useMarkNotDuplicate()

  const anyPending = merge.isPending || markNotDup.isPending

  const handleKeep = (keepId: string, mergeId: string) => {
    merge.mutate({ keepId, mergeId }, { onSuccess: onDone })
  }
  const handleNotDup = () => {
    markNotDup.mutate({ a: pair.a.id, b: pair.b.id }, { onSuccess: onDone })
  }

  // Similarity is already 0..1. Band colors: 0.6–0.7 is "worth looking at",
  // 0.7–0.85 is "probably a dupe". Both tones are reused from elsewhere in
  // the app (brand-subtle + amber-50) — no new tokens invented.
  const similarityPct = Math.round(pair.similarity * 100)
  const badgeClass =
    pair.similarity >= 0.7
      ? 'bg-amber-50 text-amber-800'
      : 'bg-brand-subtle text-brand'

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-6">
      <div className="mb-4 flex items-start justify-between gap-4">
        <h3 className="font-display text-feature font-semibold text-neutral-900 truncate">
          <span className="truncate">{pair.a.name}</span>
          <span className="mx-2 text-neutral-400">↔</span>
          <span className="truncate">{pair.b.name}</span>
        </h3>
        <span
          className={`shrink-0 rounded-full px-3 py-1 text-caption font-medium ${badgeClass}`}
        >
          {similarityPct}% similar
        </span>
      </div>

      <div className="grid grid-cols-1 gap-6 md:grid-cols-2">
        <DuplicateSide side={pair.a} label="A" />
        <DuplicateSide side={pair.b} label="B" />
      </div>

      <div className="mt-6 flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="primary"
          onClick={() => handleKeep(pair.a.id, pair.b.id)}
          disabled={anyPending}
          title={`Merge B into A — keep "${pair.a.name}"`}
        >
          Keep A
        </Button>
        <Button
          size="sm"
          variant="primary"
          onClick={() => handleKeep(pair.b.id, pair.a.id)}
          disabled={anyPending}
          title={`Merge A into B — keep "${pair.b.name}"`}
        >
          Keep B
        </Button>
        <Button
          size="sm"
          variant="subtle"
          disabled
          title="Coming soon: merge into a custom name"
        >
          Merge into…
        </Button>
        <Button
          size="sm"
          variant="outlined"
          onClick={handleNotDup}
          disabled={anyPending}
        >
          Mark as not duplicates
        </Button>
      </div>

      {(merge.isError || markNotDup.isError) && (
        <p className="mt-3 text-caption text-red-600">
          That didn't work — try again.
        </p>
      )}
    </div>
  )
}

function DuplicateSide({ side, label }: { side: DuplicatePairSide; label: string }) {
  const lastPurchasedDisplay = formatLastPurchased(side.last_purchased_at)
  const aliases = side.sample_aliases ?? []
  return (
    <div>
      <p className="text-caption uppercase tracking-wide text-neutral-400 mb-1">{label}</p>
      <p className="font-display text-body-medium font-semibold text-neutral-900 truncate">
        {side.name}
      </p>
      <dl className="mt-2 space-y-1 text-caption text-neutral-600">
        <div className="flex gap-2">
          <dt className="w-20 text-neutral-400">Brand</dt>
          <dd>{side.brand ?? '—'}</dd>
        </div>
        <div className="flex gap-2">
          <dt className="w-20 text-neutral-400">Category</dt>
          <dd>{side.category ?? '—'}</dd>
        </div>
        <div className="flex gap-2">
          <dt className="w-20 text-neutral-400">Purchases</dt>
          <dd>
            {side.purchase_count}
            {lastPurchasedDisplay ? ` · last ${lastPurchasedDisplay}` : ''}
          </dd>
        </div>
        {aliases.length > 0 && (
          <div className="flex gap-2">
            <dt className="w-20 text-neutral-400">Aliases</dt>
            <dd className="truncate">{aliases.join(', ')}</dd>
          </div>
        )}
      </dl>
    </div>
  )
}

function formatLastPurchased(iso: string | null): string | null {
  if (!iso) return null
  try {
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return iso
    return d.toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    })
  } catch {
    return iso
  }
}
