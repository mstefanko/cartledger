import { useRef, useCallback } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { ReceiptReview } from '@/components/receipts/ReceiptReview'
import { getReceipt, type ReceiptDetail } from '@/api/receipts'

function ReceiptReviewPage() {
  const { id } = useParams<{ id: string }>()
  const imageRef = useRef<HTMLDivElement>(null)

  const { data: receipt } = useQuery<ReceiptDetail>({
    queryKey: ['receipt', id],
    queryFn: () => getReceipt(id!),
    enabled: !!id,
  })

  const handleScrollToImage = useCallback(() => {
    imageRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [])

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
      <div className="mb-4">
        <Link to="/receipts" className="text-caption text-brand hover:underline">
          &larr; Back to Receipts
        </Link>
      </div>
      <h1 className="font-display text-subhead font-bold text-neutral-900 mb-4">
        Review Receipt
      </h1>

      {receipt && (receipt.receipt_time || receipt.card_type) && (
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
          {receipt.card_type && (
            <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-neutral-100 text-neutral-700">
              {receipt.card_type}
              {receipt.card_last4 ? ` \u00b7\u00b7\u00b7\u00b7${receipt.card_last4}` : ''}
            </span>
          )}
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* LEFT: Receipt images */}
        <div ref={imageRef} className="flex flex-col gap-4">
          <h2 className="font-display text-feature font-semibold text-neutral-900">
            Receipt Image
          </h2>
          {imagePaths.length > 0 ? (
            <div className="flex flex-col gap-4 overflow-y-auto max-h-[80vh] rounded-lg border border-neutral-200 p-2 bg-neutral-50">
              {imagePaths.map((path, idx) => (
                <img
                  key={idx}
                  src={`/api/v1/files/${encodeURIComponent(path)}`}
                  alt={`Receipt page ${idx + 1}`}
                  className="w-full rounded-lg shadow-micro"
                  loading="lazy"
                />
              ))}
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
          <ReceiptReview
            receiptId={id}
            onScrollToImage={handleScrollToImage}
          />
        </div>
      </div>
    </div>
  )
}

export default ReceiptReviewPage
