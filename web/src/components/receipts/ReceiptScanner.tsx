import { useState, useRef, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { scanReceipt } from '@/api/receipts'
import { getWebSocket } from '@/api/ws'
import { Button } from '@/components/ui/Button'
import type { Receipt, WSMessage } from '@/types'

const MAX_IMAGES = 5
const MAX_FILE_SIZE = 10 * 1024 * 1024 // 10MB
const ACCEPTED_TYPES = ['image/jpeg', 'image/png']

interface ImageEntry {
  file: File
  previewUrl: string
}

type ScannerPhase = 'capture' | 'uploading' | 'processing' | 'error'

function validateFile(file: File): string | null {
  if (!ACCEPTED_TYPES.includes(file.type)) {
    return `"${file.name}" is not a supported format. Use JPEG or PNG.`
  }
  if (file.size > MAX_FILE_SIZE) {
    const sizeMB = (file.size / (1024 * 1024)).toFixed(1)
    return `"${file.name}" is ${sizeMB} MB. Maximum is 10 MB.`
  }
  return null
}

function ReceiptScanner() {
  const navigate = useNavigate()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [images, setImages] = useState<ImageEntry[]>([])
  const [phase, setPhase] = useState<ScannerPhase>('capture')
  const [error, setError] = useState<string | null>(null)
  const pendingReceiptId = useRef<string | null>(null)

  // Clean up object URLs on unmount
  useEffect(() => {
    return () => {
      images.forEach((img) => URL.revokeObjectURL(img.previewUrl))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Listen for WebSocket receipt.processed event
  useEffect(() => {
    const ws = getWebSocket()
    if (!ws || phase !== 'processing') return

    function handleMessage(event: MessageEvent) {
      let message: WSMessage
      try {
        message = JSON.parse(event.data as string) as WSMessage
      } catch {
        return
      }

      if (message.type === 'receipt.processed' && pendingReceiptId.current) {
        const payload = message.payload as { receipt_id?: string }
        if (payload.receipt_id === pendingReceiptId.current) {
          navigate(`/receipts/${pendingReceiptId.current}`)
        }
      }
    }

    ws.addEventListener('message', handleMessage)
    return () => {
      ws.removeEventListener('message', handleMessage)
    }
  }, [phase, navigate])

  const uploadMutation = useMutation<Receipt, Error, File[]>({
    mutationFn: scanReceipt,
    onSuccess: (receipt) => {
      pendingReceiptId.current = receipt.id
      // If status is already matched/reviewed, navigate immediately
      if (receipt.status !== 'pending') {
        navigate(`/receipts/${receipt.id}`)
      } else {
        setPhase('processing')
      }
    },
    onError: (err) => {
      setError(err.message || 'Upload failed. Please try again.')
      setPhase('error')
    },
  })

  const handleFileChange = useCallback(
    (event: React.ChangeEvent<HTMLInputElement>) => {
      const files = event.target.files
      if (!files || files.length === 0) return

      setError(null)

      const newEntries: ImageEntry[] = []
      for (let i = 0; i < files.length; i++) {
        const file = files[i]
        if (!file) continue

        if (images.length + newEntries.length >= MAX_IMAGES) {
          setError(`Maximum ${MAX_IMAGES} images per receipt.`)
          break
        }

        const validationError = validateFile(file)
        if (validationError) {
          setError(validationError)
          continue
        }

        newEntries.push({
          file,
          previewUrl: URL.createObjectURL(file),
        })
      }

      if (newEntries.length > 0) {
        setImages((prev) => [...prev, ...newEntries])
      }

      // Reset input so the same file can be re-selected
      event.target.value = ''
    },
    [images.length],
  )

  const removeImage = useCallback((index: number) => {
    setImages((prev) => {
      const removed = prev[index]
      if (removed) {
        URL.revokeObjectURL(removed.previewUrl)
      }
      return prev.filter((_, i) => i !== index)
    })
  }, [])

  const openFilePicker = useCallback(() => {
    fileInputRef.current?.click()
  }, [])

  const handleUpload = useCallback(() => {
    if (images.length === 0) return
    setError(null)
    setPhase('uploading')
    uploadMutation.mutate(images.map((img) => img.file))
  }, [images, uploadMutation])

  const handleRetry = useCallback(() => {
    setError(null)
    setPhase('capture')
  }, [])

  // --- Processing state ---
  if (phase === 'processing') {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <div className="h-12 w-12 animate-spin rounded-full border-4 border-neutral-200 border-t-brand" />
        <p className="mt-6 font-display text-feature font-semibold text-neutral-900">
          Scanning receipt...
        </p>
        <p className="mt-2 text-body text-neutral-400">
          Extracting items, prices, and store information.
        </p>
      </div>
    )
  }

  // --- Uploading state ---
  if (phase === 'uploading') {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <div className="h-12 w-12 animate-spin rounded-full border-4 border-neutral-200 border-t-brand" />
        <p className="mt-6 font-display text-feature font-semibold text-neutral-900">
          Uploading images...
        </p>
        <p className="mt-2 text-body text-neutral-400">
          {images.length} {images.length === 1 ? 'image' : 'images'} being sent.
        </p>
      </div>
    )
  }

  // --- Error state ---
  if (phase === 'error') {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <div className="flex h-16 w-16 items-center justify-center rounded-full bg-expensive-subtle">
          <svg className="h-8 w-8 text-expensive" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
          </svg>
        </div>
        <p className="mt-6 font-display text-feature font-semibold text-neutral-900">
          Something went wrong
        </p>
        <p className="mt-2 text-body text-neutral-400">
          {error ?? 'Upload failed. Please try again.'}
        </p>
        <Button className="mt-6" onClick={handleRetry}>
          Try Again
        </Button>
      </div>
    )
  }

  // --- Capture state (default) ---
  return (
    <div className="flex flex-col gap-6">
      {/* Hidden file input */}
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        capture="environment"
        className="hidden"
        onChange={handleFileChange}
      />

      {/* Empty state — large capture button */}
      {images.length === 0 && (
        <button
          type="button"
          onClick={openFilePicker}
          className="flex flex-col items-center justify-center gap-4 rounded-2xl border-2 border-dashed border-neutral-200 bg-neutral-50 px-6 py-16 transition-colors hover:border-brand hover:bg-brand-subtle active:bg-brand-subtle cursor-pointer"
        >
          <svg className="h-12 w-12 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M6.827 6.175A2.31 2.31 0 015.186 7.23c-.38.054-.757.112-1.134.175C2.999 7.58 2.25 8.507 2.25 9.574V18a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9.574c0-1.067-.75-1.994-1.802-2.169a47.865 47.865 0 00-1.134-.175 2.31 2.31 0 01-1.64-1.055l-.822-1.316a2.192 2.192 0 00-1.736-1.039 48.774 48.774 0 00-5.232 0 2.192 2.192 0 00-1.736 1.039l-.821 1.316z" />
            <path strokeLinecap="round" strokeLinejoin="round" d="M16.5 12.75a4.5 4.5 0 11-9 0 4.5 4.5 0 019 0z" />
          </svg>
          <span className="text-body-medium text-neutral-900">
            Take Photo or Choose Image
          </span>
          <span className="text-caption text-neutral-400">
            JPEG or PNG, up to 10 MB
          </span>
        </button>
      )}

      {/* Thumbnail grid */}
      {images.length > 0 && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          {images.map((img, index) => (
            <div key={img.previewUrl} className="group relative aspect-[3/4] overflow-hidden rounded-xl border border-neutral-200">
              <img
                src={img.previewUrl}
                alt={`Receipt page ${index + 1}`}
                className="h-full w-full object-cover"
              />
              {/* Page number badge */}
              <span className="absolute left-2 top-2 flex h-6 w-6 items-center justify-center rounded-md bg-neutral-900/70 text-small font-medium text-white">
                {index + 1}
              </span>
              {/* Remove button */}
              <button
                type="button"
                onClick={() => removeImage(index)}
                className="absolute right-2 top-2 flex h-7 w-7 items-center justify-center rounded-lg bg-neutral-900/70 text-white opacity-0 transition-opacity group-hover:opacity-100 hover:bg-expensive active:bg-expensive cursor-pointer"
                aria-label={`Remove image ${index + 1}`}
              >
                <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>
          ))}
        </div>
      )}

      {/* Validation error */}
      {error && (
        <p className="text-caption text-expensive">{error}</p>
      )}

      {/* Action buttons */}
      {images.length > 0 && (
        <div className="flex flex-col gap-3 sm:flex-row">
          {images.length < MAX_IMAGES && (
            <Button variant="outlined" onClick={openFilePicker} className="flex-1">
              <svg className="mr-2 h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
              </svg>
              Add More Pages
            </Button>
          )}
          <Button onClick={handleUpload} className="flex-1">
            <svg className="mr-2 h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5m-13.5-9L12 3m0 0l4.5 4.5M12 3v13.5" />
            </svg>
            Upload &amp; Scan
          </Button>
        </div>
      )}

      {/* Image count hint */}
      {images.length > 0 && (
        <p className="text-center text-small text-neutral-400">
          {images.length} of {MAX_IMAGES} images
          {images.length < MAX_IMAGES && ' — add more for long receipts'}
        </p>
      )}
    </div>
  )
}

export { ReceiptScanner }
