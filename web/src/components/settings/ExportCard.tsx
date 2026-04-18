import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { triggerExportDownload, type ExportParams } from '@/api/export'
import { listStores } from '@/api/stores'
import { Button } from '@/components/ui/Button'

function ExportCard() {
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [storeId, setStoreId] = useState('')
  const [format, setFormat] = useState<'csv' | 'markdown'>('csv')

  const { data: stores, isLoading: storesLoading } = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  function handleDownload() {
    const params: ExportParams = { format }
    if (from) params.from = from
    if (to) params.to = to
    if (storeId) params.store_id = storeId
    triggerExportDownload(params)
  }

  return (
    <div className="bg-white rounded-2xl shadow-subtle p-6">
      <h2 className="font-display text-feature font-semibold text-neutral-900">
        Export receipts
      </h2>
      <p className="text-body text-neutral-500 mt-1 mb-4">
        Download receipts as a spreadsheet or Obsidian-friendly markdown.
      </p>

      <div className="flex flex-col gap-4 max-w-md">
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <label htmlFor="export-from" className="text-caption font-medium text-neutral-900">
              From
            </label>
            <input
              id="export-from"
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 text-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <label htmlFor="export-to" className="text-caption font-medium text-neutral-900">
              To
            </label>
            <input
              id="export-to"
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 text-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
            />
          </div>
        </div>

        <div className="flex flex-col gap-1.5">
          <label htmlFor="export-store" className="text-caption font-medium text-neutral-900">
            Store
          </label>
          <select
            id="export-store"
            value={storeId}
            onChange={(e) => setStoreId(e.target.value)}
            disabled={storesLoading}
            className="w-full px-3 py-2.5 rounded-xl border border-neutral-200 text-body text-neutral-900 bg-white focus:outline-none focus:ring-2 focus:ring-brand focus:border-brand"
          >
            <option value="">All stores</option>
            {stores?.map((s) => (
              <option key={s.id} value={s.id}>
                {s.nickname || s.name}
              </option>
            ))}
          </select>
        </div>

        <fieldset className="flex flex-col gap-2">
          <legend className="text-caption font-medium text-neutral-900 mb-1">Format</legend>
          <label className="inline-flex items-center gap-2 text-body text-neutral-700">
            <input
              type="radio"
              name="export-format"
              value="csv"
              checked={format === 'csv'}
              onChange={() => setFormat('csv')}
              className="accent-brand"
            />
            CSV
          </label>
          <label className="inline-flex items-center gap-2 text-body text-neutral-700">
            <input
              type="radio"
              name="export-format"
              value="markdown"
              checked={format === 'markdown'}
              onChange={() => setFormat('markdown')}
              className="accent-brand"
            />
            Markdown zip
          </label>
        </fieldset>

        <p className="text-small text-neutral-400">
          CSV includes every line item. Markdown produces one file per receipt, zipped.
        </p>

        <div className="flex justify-end">
          <Button size="sm" onClick={handleDownload}>
            Download export
          </Button>
        </div>
      </div>
    </div>
  )
}

export default ExportCard
