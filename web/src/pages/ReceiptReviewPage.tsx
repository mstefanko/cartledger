import { useState, useRef, useCallback, useEffect } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ReceiptReview } from '@/components/receipts/ReceiptReview'
import { getReceipt, deleteReceipt, reprocessReceipt, updateReceipt, type ReceiptDetail } from '@/api/receipts'
import { listStores } from '@/api/stores'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { ApiClientError } from '@/api/client'
import { formatDateOnly } from '@/lib/dates'
import type { UpdateReceiptRequest } from '@/types'

const LENS_SIZE = 280
const ZOOM = 0.75

type ReceiptMetaForm = {
  store_id: string
  receipt_date: string
  receipt_time: string
  subtotal: string
  tax: string
  total: string
}

const emptyReceiptMeta: ReceiptMetaForm = {
  store_id: '',
  receipt_date: '',
  receipt_time: '',
  subtotal: '',
  tax: '',
  total: '',
}

function timeInputValue(value: string | null | undefined): string {
  if (!value || value.toLowerCase() === '<unknown>') return ''
  const match = value.match(/^(\d{2}:\d{2})/)
  return match?.[1] ?? ''
}

function ReceiptImagePending({ message }: { message: string }) {
  return (
    <div
      role="status"
      className="flex h-64 flex-col items-center justify-center rounded-lg border border-neutral-200 bg-neutral-50 px-4 text-center"
    >
      <div className="h-10 w-10 animate-spin rounded-full border-4 border-neutral-200 border-t-brand" />
      <p className="mt-4 text-body text-neutral-500">{message}</p>
    </div>
  )
}

function ReceiptMagnifier({ src, alt }: { src: string; alt: string }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [lens, setLens] = useState<{ x: number; y: number; bgX: number; bgY: number; show: boolean; imgW: number; imgH: number }>({
    x: 0, y: 0, bgX: 0, bgY: 0, show: false, imgW: 0, imgH: 0,
  })

  const handleMove = useCallback((e: React.MouseEvent<HTMLDivElement>) => {
    const container = containerRef.current
    const img = container?.querySelector('img')
    if (!container || !img) return
    const rect = img.getBoundingClientRect()
    const x = e.clientX - rect.left
    const y = e.clientY - rect.top
    // clamp
    if (x < 0 || y < 0 || x > rect.width || y > rect.height) {
      setLens((p) => ({ ...p, show: false }))
      return
    }
    // background position: scale mouse coords to natural image size
    const ratioX = img.naturalWidth / rect.width
    const ratioY = img.naturalHeight / rect.height
    setLens({
      x: e.clientX - container.getBoundingClientRect().left,
      y: e.clientY - container.getBoundingClientRect().top,
      bgX: x * ratioX * ZOOM - LENS_SIZE / 2,
      bgY: y * ratioY * ZOOM - LENS_SIZE / 2,
      show: true,
      imgW: img.naturalWidth * ZOOM,
      imgH: img.naturalHeight * ZOOM,
    })
  }, [])

  return (
    <div
      ref={containerRef}
      className="relative cursor-crosshair"
      onMouseMove={handleMove}
      onMouseLeave={() => setLens((p) => ({ ...p, show: false }))}
    >
      <img src={src} alt={alt} className="w-full rounded-lg shadow-micro" loading="lazy" />
      {lens.show && (
        <div
          className="pointer-events-none absolute border-2 border-white rounded-full shadow-lg z-10"
          style={{
            width: LENS_SIZE,
            height: LENS_SIZE,
            left: lens.x - LENS_SIZE / 2,
            top: lens.y - LENS_SIZE / 2,
            backgroundImage: `url(${src})`,
            backgroundSize: `${lens.imgW}px ${lens.imgH}px`,
            backgroundPosition: `-${lens.bgX}px -${lens.bgY}px`,
            backgroundRepeat: 'no-repeat',
          }}
        />
      )}
    </div>
  )
}

function ReceiptReviewPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [showDelete, setShowDelete] = useState(false)
  const [showImage, setShowImage] = useState(false)

  const { data: receipt, isLoading: isReceiptLoading } = useQuery<ReceiptDetail>({
    queryKey: ['receipt', id],
    queryFn: () => getReceipt(id!),
    enabled: !!id,
  })
  const { data: stores = [] } = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })
  const [receiptMeta, setReceiptMeta] = useState<ReceiptMetaForm>(emptyReceiptMeta)

  useEffect(() => {
    if (!receipt) return
    setReceiptMeta({
      store_id: receipt.store_id ?? '',
      receipt_date: receipt.receipt_date ?? '',
      receipt_time: timeInputValue(receipt.receipt_time),
      subtotal: receipt.subtotal ?? '',
      tax: receipt.tax ?? '',
      total: receipt.total ?? '',
    })
  }, [receipt])

  const isExtracting = receipt?.status === 'pending' || receipt?.status === 'processing'
  const canEditReceiptMeta = !!receipt && receipt.status !== 'reviewed'

  useEffect(() => {
    if (!id || !isExtracting) return
    const interval = window.setInterval(() => {
      void queryClient.invalidateQueries({ queryKey: ['receipt', id] })
      void queryClient.invalidateQueries({ queryKey: ['receipts'] })
    }, 3_000)
    return () => window.clearInterval(interval)
  }, [id, isExtracting, queryClient])

  const deleteMutation = useMutation({
    mutationFn: () => deleteReceipt(id!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['receipts'] })
      navigate('/receipts')
    },
  })

  const updateReceiptMutation = useMutation({
    mutationFn: (patch: UpdateReceiptRequest) => updateReceipt(id!, patch),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['receipt', id] })
      void queryClient.invalidateQueries({ queryKey: ['receipts'] })
      void queryClient.invalidateQueries({ queryKey: ['products'] })
    },
  })

  const commitReceiptMeta = useCallback(
    (field: keyof ReceiptMetaForm) => {
      if (!receipt || !canEditReceiptMeta) return
      const value = receiptMeta[field].trim()
      const current: ReceiptMetaForm = {
        store_id: receipt.store_id ?? '',
        receipt_date: receipt.receipt_date ?? '',
        receipt_time: timeInputValue(receipt.receipt_time),
        subtotal: receipt.subtotal ?? '',
        tax: receipt.tax ?? '',
        total: receipt.total ?? '',
      }
      if (value === current[field]) return
      updateReceiptMutation.mutate({ [field]: value } as UpdateReceiptRequest)
    },
    [canEditReceiptMeta, receipt, receiptMeta, updateReceiptMutation],
  )

  const [retryError, setRetryError] = useState<string | null>(null)
  const retryMutation = useMutation({
    mutationFn: () => reprocessReceipt(id!),
    onMutate: async () => {
      // Flip the detail cache to "processing" optimistically; the ws
      // 'receipt.complete' event will invalidate ['receipt', id] when
      // the worker finishes (success or failure).
      await queryClient.cancelQueries({ queryKey: ['receipt', id] })
      const previous = queryClient.getQueryData<ReceiptDetail>(['receipt', id])
      if (previous) {
        queryClient.setQueryData<ReceiptDetail>(['receipt', id], {
          ...previous,
          status: 'processing',
          error_message: null,
        })
      }
      return { previous }
    },
    onError: (err, _vars, ctx) => {
      if (ctx?.previous) queryClient.setQueryData(['receipt', id], ctx.previous)
      setRetryError(err instanceof ApiClientError ? err.message : 'Retry failed')
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ['receipt', id] })
      void queryClient.invalidateQueries({ queryKey: ['receipts'] })
    },
  })

  if (!id) {
    return (
      <div className="py-8">
        <p className="text-body text-expensive">No receipt ID provided.</p>
      </div>
    )
  }

  const displayImages = (() => {
    const images = receipt?.images ?? []
    const processed = images.filter((image) => image.kind === 'processed')
    return processed.length > 0 ? processed : images.filter((image) => image.kind === 'original')
  })()
  const metaControlClass = [
    'h-9 w-full rounded-lg border border-neutral-200 bg-white px-2 text-caption text-neutral-900',
    'focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand',
    !canEditReceiptMeta ? 'cursor-not-allowed bg-neutral-50 text-neutral-400' : '',
  ].join(' ')

  return (
    <div className="py-6">
      <div className="mb-4 flex items-center justify-between">
        <Link to="/receipts" className="text-caption text-brand hover:underline">
          &larr; Back to Receipts
        </Link>
        <Button
          variant="secondary"
          size="sm"
          className="text-red-500 hover:text-red-700 hover:bg-red-50"
          onClick={() => setShowDelete(true)}
        >
          Delete Receipt
        </Button>
      </div>
      <h1 className="font-display text-subhead font-bold text-neutral-900 mb-4">
        Review Receipt
      </h1>

      {receipt && (
        <div className="mb-4 rounded-lg border border-neutral-200 bg-neutral-50/60 p-3">
          <div className="grid grid-cols-2 gap-3 md:grid-cols-[minmax(9rem,1fr)_minmax(8rem,0.9fr)_minmax(7rem,0.7fr)_repeat(3,minmax(5.5rem,0.6fr))]">
            <label className="flex flex-col gap-1 text-small font-medium text-neutral-500">
              Store
              <select
                value={receiptMeta.store_id}
                disabled={!canEditReceiptMeta}
                onChange={(event) => {
                  const value = event.target.value
                  setReceiptMeta((prev) => ({ ...prev, store_id: value }))
                  updateReceiptMutation.mutate({ store_id: value })
                }}
                className={metaControlClass}
              >
                <option value="">Unknown</option>
                {stores.map((store) => (
                  <option key={store.id} value={store.id}>
                    {store.nickname ?? store.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-small font-medium text-neutral-500">
              Date
              <input
                type="date"
                value={receiptMeta.receipt_date}
                disabled={!canEditReceiptMeta}
                onChange={(event) => setReceiptMeta((prev) => ({ ...prev, receipt_date: event.target.value }))}
                onBlur={() => commitReceiptMeta('receipt_date')}
                className={metaControlClass}
              />
            </label>
            <label className="flex flex-col gap-1 text-small font-medium text-neutral-500">
              Time
              <input
                type="time"
                value={receiptMeta.receipt_time}
                disabled={!canEditReceiptMeta}
                placeholder="Time missing"
                onChange={(event) => setReceiptMeta((prev) => ({ ...prev, receipt_time: event.target.value }))}
                onBlur={() => commitReceiptMeta('receipt_time')}
                className={metaControlClass}
              />
            </label>
            <label className="flex flex-col gap-1 text-small font-medium text-neutral-500">
              Subtotal
              <input
                inputMode="decimal"
                value={receiptMeta.subtotal}
                disabled={!canEditReceiptMeta}
                onChange={(event) => setReceiptMeta((prev) => ({ ...prev, subtotal: event.target.value }))}
                onBlur={() => commitReceiptMeta('subtotal')}
                className={metaControlClass}
              />
            </label>
            <label className="flex flex-col gap-1 text-small font-medium text-neutral-500">
              Tax
              <input
                inputMode="decimal"
                value={receiptMeta.tax}
                disabled={!canEditReceiptMeta}
                onChange={(event) => setReceiptMeta((prev) => ({ ...prev, tax: event.target.value }))}
                onBlur={() => commitReceiptMeta('tax')}
                className={metaControlClass}
              />
            </label>
            <label className="flex flex-col gap-1 text-small font-medium text-neutral-500">
              Total
              <input
                inputMode="decimal"
                value={receiptMeta.total}
                disabled={!canEditReceiptMeta}
                onChange={(event) => setReceiptMeta((prev) => ({ ...prev, total: event.target.value }))}
                onBlur={() => commitReceiptMeta('total')}
                className={metaControlClass}
              />
            </label>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-3 text-small text-neutral-400">
            {receipt.receipt_date && (
              <span>
                {formatDateOnly(receipt.receipt_date, {
                  year: 'numeric',
                  month: 'short',
                  day: 'numeric',
                })}
                {receipt.receipt_time && !receipt.receipt_time.toLowerCase().includes('unknown') && ` at ${receipt.receipt_time}`}
              </span>
            )}
            {receipt.created_at && receipt.created_at !== receipt.receipt_date && (
              <span>
                Scanned {new Date(receipt.created_at).toLocaleDateString(undefined, {
                  year: 'numeric',
                  month: 'short',
                  day: 'numeric',
                })}
              </span>
            )}
            {receipt.card_type && (
              <span className="inline-flex items-center rounded bg-neutral-100 px-2 py-0.5 text-small font-medium text-neutral-700">
                {receipt.card_type}
                {receipt.card_last4 ? ` \u00b7\u00b7\u00b7\u00b7${receipt.card_last4}` : ''}
              </span>
            )}
            {updateReceiptMutation.isError && (
              <span className="text-expensive">Receipt details failed to save.</span>
            )}
          </div>
        </div>
      )}

      {receipt?.status === 'error' && (
        <div
          role="alert"
          className="mb-4 rounded-lg border border-red-200 bg-red-50 p-4"
        >
          <div className="flex items-start justify-between gap-3">
            <div>
              <p className="text-sm font-medium text-red-800">
                Extraction failed
              </p>
              <p className="mt-1 text-sm text-red-700">
                {receipt.error_message || 'Processing failed (no details)'}
              </p>
            </div>
            <Button
              size="sm"
              variant="secondary"
              onClick={() => { setRetryError(null); retryMutation.mutate() }}
              disabled={retryMutation.isPending}
            >
              {retryMutation.isPending ? 'Retrying...' : 'Retry extraction'}
            </Button>
          </div>
          {retryError && (
            <p className="mt-2 text-xs text-red-600">{retryError}</p>
          )}
        </div>
      )}

      {/* Mobile: toggle to show/hide receipt image */}
      <div className="lg:hidden mb-3">
        <button
          type="button"
          onClick={() => setShowImage((v) => !v)}
          className="text-caption text-brand hover:underline flex items-center gap-1"
        >
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
          </svg>
          {showImage ? 'Hide Receipt Image' : 'View Receipt Image'}
        </button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[1fr_2fr] gap-6">
        {/* LEFT: Receipt images */}
        <div className={`flex flex-col gap-4 ${showImage ? '' : 'hidden lg:flex'}`}>
          <h2 className="font-display text-feature font-semibold text-neutral-900">
            Receipt Image
          </h2>
          {isReceiptLoading ? (
            <ReceiptImagePending message="Loading receipt image..." />
          ) : displayImages.length > 0 ? (
            <div className="flex flex-col gap-4 overflow-y-auto max-h-[80vh] rounded-lg border border-neutral-200 p-2 bg-neutral-50">
              {displayImages.map((image, idx) => (
                <ReceiptMagnifier
                  key={`${image.kind}-${image.page}`}
                  src={image.url}
                  alt={`Receipt page ${idx + 1}`}
                />
              ))}
              <p className="text-xs text-neutral-400 text-center pb-1">Hover to magnify</p>
            </div>
          ) : isExtracting ? (
            <ReceiptImagePending message="Preparing receipt image..." />
          ) : (
            <div className="flex items-center justify-center h-64 rounded-lg border border-neutral-200 bg-neutral-50">
              <p className="text-body text-neutral-400">
                No receipt images available
              </p>
            </div>
          )}
        </div>

        {/* RIGHT: Editable line items table */}
        <div className="min-w-0">
          <ReceiptReview receiptId={id} />
        </div>
      </div>

      <Modal
        open={showDelete}
        onClose={() => setShowDelete(false)}
        title="Delete Receipt"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setShowDelete(false)}>
              Cancel
            </Button>
            <Button
              className="bg-red-600 text-white hover:bg-red-700"
              size="sm"
              onClick={() => deleteMutation.mutate()}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-600">
          Delete this receipt and all its line items? This cannot be undone.
        </p>
      </Modal>
    </div>
  )
}

export default ReceiptReviewPage
