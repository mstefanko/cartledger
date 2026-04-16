import { useState, useRef, useCallback } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { ReceiptReview } from '@/components/receipts/ReceiptReview'
import { getReceipt, deleteReceipt, type ReceiptDetail } from '@/api/receipts'
import { getToken } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'

const LENS_SIZE = 240
const ZOOM = 1.10

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

  const { data: receipt } = useQuery<ReceiptDetail>({
    queryKey: ['receipt', id],
    queryFn: () => getReceipt(id!),
    enabled: !!id,
  })

  const deleteMutation = useMutation({
    mutationFn: () => deleteReceipt(id!),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['receipts'] })
      navigate('/receipts')
    },
  })

  if (!id) {
    return (
      <div className="py-8">
        <p className="text-body text-expensive">No receipt ID provided.</p>
      </div>
    )
  }

  // Parse image paths from the receipt (comma-separated or JSON array)
  const imagePaths: string[] = (() => {
    if (!receipt?.image_paths) return []
    try {
      const parsed = JSON.parse(receipt.image_paths)
      if (Array.isArray(parsed)) return parsed as string[]
    } catch {
      // Not JSON — treat as comma-separated
    }
    return receipt.image_paths.split(',').map((p) => p.trim()).filter(Boolean)
  })()

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
        <div className="flex items-center gap-3 text-sm text-neutral-500 mb-4">
          {receipt.receipt_date && (
            <span>
              {new Date(receipt.receipt_date).toLocaleDateString(undefined, {
                year: 'numeric',
                month: 'short',
                day: 'numeric',
              })}
              {receipt.receipt_time && ` at ${receipt.receipt_time}`}
            </span>
          )}
          {receipt.created_at && receipt.created_at !== receipt.receipt_date && (
            <span className="text-neutral-400">
              Scanned {new Date(receipt.created_at).toLocaleDateString(undefined, {
                year: 'numeric',
                month: 'short',
                day: 'numeric',
              })}
            </span>
          )}
          {receipt.card_type && (
            <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-neutral-100 text-neutral-700">
              {receipt.card_type}
              {receipt.card_last4 ? ` \u00b7\u00b7\u00b7\u00b7${receipt.card_last4}` : ''}
            </span>
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
          {imagePaths.length > 0 ? (
            <div className="flex flex-col gap-4 overflow-y-auto max-h-[80vh] rounded-lg border border-neutral-200 p-2 bg-neutral-50">
              {imagePaths.map((path, idx) => (
                <ReceiptMagnifier
                  key={idx}
                  src={`/api/v1/files/${path}?token=${encodeURIComponent(getToken() ?? '')}`}
                  alt={`Receipt page ${idx + 1}`}
                />
              ))}
              <p className="text-xs text-neutral-400 text-center pb-1">Hover to magnify</p>
            </div>
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
