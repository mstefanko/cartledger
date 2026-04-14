import { useState, useRef, useEffect, useCallback } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { scanReceipt } from '@/api/receipts'
import { getWebSocket } from '@/api/ws'
import { Button } from '@/components/ui/Button'
import type { Receipt, WSMessage } from '@/types'

const MAX_IMAGES = 5
const MAX_FILE_SIZE = 10 * 1024 * 1024 // 10MB
const ACCEPTED_TYPES = ['image/jpeg', 'image/png']
const RESIZE_MAX_DIM = 1600 // max width or height in px
const RESIZE_QUALITY = 0.85

interface ImageEntry {
  file: File
  previewUrl: string
}

type ScannerPhase = 'capture' | 'preparing' | 'uploading' | 'processing' | 'timeout' | 'error'

// Processing stage labels shown during the "processing" phase
const PROCESSING_STAGES = [
  { label: 'Starting AI engine...', duration: 8_000 },
  { label: 'Reading receipt image...', duration: 12_000 },
  { label: 'Extracting line items and prices...', duration: 30_000 },
  { label: 'Identifying store and date...', duration: 15_000 },
  { label: 'Matching products...', duration: 20_000 },
  { label: 'Almost done...', duration: 120_000 },
]

/**
 * Resize an image to fit within maxDim x maxDim while preserving aspect ratio.
 *
 * Uses createImageBitmap (preferred, handles EXIF orientation natively) with
 * canvas fallback. Step-down resizing for quality when scaling > 2x.
 * On any failure, returns the original file so the upload still works.
 */
async function resizeImage(file: File): Promise<File> {
  try {
    // createImageBitmap handles EXIF orientation automatically
    const bitmap = await createImageBitmap(file)
    const { width, height } = bitmap

    // Skip if already within bounds
    if (width <= RESIZE_MAX_DIM && height <= RESIZE_MAX_DIM) {
      bitmap.close()
      return file
    }

    // Calculate target dimensions preserving aspect ratio
    const scale = Math.min(RESIZE_MAX_DIM / width, RESIZE_MAX_DIM / height)
    let targetW = Math.round(width * scale)
    let targetH = Math.round(height * scale)

    // Step-down resize: halve dimensions until we're within 2x of target.
    // This produces much better quality than a single large downscale.
    let source: ImageBitmap | HTMLCanvasElement = bitmap
    let srcW = width
    let srcH = height

    while (srcW / 2 > targetW) {
      const halfW = Math.round(srcW / 2)
      const halfH = Math.round(srcH / 2)
      const step = document.createElement('canvas')
      step.width = halfW
      step.height = halfH
      const stepCtx = step.getContext('2d')!
      stepCtx.drawImage(source, 0, 0, halfW, halfH)
      if (source instanceof ImageBitmap) source.close()
      source = step
      srcW = halfW
      srcH = halfH
    }

    // Final draw to target size
    const canvas = document.createElement('canvas')
    canvas.width = targetW
    canvas.height = targetH
    const ctx = canvas.getContext('2d')!
    ctx.imageSmoothingQuality = 'high'
    ctx.drawImage(source, 0, 0, targetW, targetH)
    if (source instanceof ImageBitmap) source.close()

    const blob = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, 'image/jpeg', RESIZE_QUALITY)
    )

    if (!blob) return file // fallback to original

    return new File([blob], file.name.replace(/\.\w+$/, '.jpg'), {
      type: 'image/jpeg',
      lastModified: file.lastModified,
    })
  } catch {
    // Any failure (canvas OOM, unsupported format) — send original
    return file
  }
}

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

/** Indeterminate progress bar that fills over time. */
function ProgressBar({ stages }: { stages: typeof PROCESSING_STAGES }) {
  const [stageIndex, setStageIndex] = useState(0)
  const [barWidth, setBarWidth] = useState(0)

  useEffect(() => {
    let elapsed = 0
    let currentStage = 0
    const interval = setInterval(() => {
      elapsed += 500
      // Advance stage based on cumulative duration
      let cumulativeDuration = 0
      for (let i = 0; i < stages.length; i++) {
        cumulativeDuration += stages[i]!.duration
        if (elapsed < cumulativeDuration) {
          currentStage = i
          break
        }
        if (i === stages.length - 1) currentStage = i
      }
      setStageIndex(currentStage)

      // Calculate total progress (eased — slows down toward the end)
      const totalDuration = stages.reduce((sum, s) => sum + s.duration, 0)
      const linear = Math.min(elapsed / totalDuration, 0.95) // never hit 100% until real completion
      const eased = 1 - Math.pow(1 - linear, 2) // ease-out
      setBarWidth(Math.round(eased * 100))
    }, 500)
    return () => clearInterval(interval)
  }, [stages])

  return (
    <div className="w-full max-w-xs mx-auto mt-6">
      <div className="h-2 rounded-full bg-neutral-200 overflow-hidden">
        <div
          className="h-full rounded-full bg-brand transition-all duration-500 ease-out"
          style={{ width: `${barWidth}%` }}
        />
      </div>
      <p className="mt-3 text-small text-neutral-400 animate-pulse">
        {stages[stageIndex]?.label ?? 'Processing...'}
      </p>
    </div>
  )
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

      if (message.type === 'receipt.complete' && pendingReceiptId.current) {
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

  // 3-minute timeout for processing phase
  useEffect(() => {
    if (phase !== 'processing') return
    const timer = setTimeout(() => {
      setPhase('timeout')
    }, 180_000)
    return () => clearTimeout(timer)
  }, [phase])

  const uploadMutation = useMutation<Receipt, Error, File[]>({
    mutationFn: scanReceipt,
    onSuccess: (receipt) => {
      pendingReceiptId.current = receipt.id
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

  const handleUpload = useCallback(async () => {
    if (images.length === 0) return
    setError(null)
    setPhase('preparing')

    try {
      const resized = await Promise.all(images.map((img) => resizeImage(img.file)))
      setPhase('uploading')
      uploadMutation.mutate(resized)
    } catch {
      setError('Failed to process images. Please try again.')
      setPhase('error')
    }
  }, [images, uploadMutation])

  const handleRetry = useCallback(() => {
    setError(null)
    setPhase('capture')
  }, [])

  // --- Timeout state ---
  if (phase === 'timeout') {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <div className="flex h-16 w-16 items-center justify-center rounded-full bg-amber-100">
          <svg className="h-8 w-8 text-amber-800" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
        </div>
        <p className="mt-6 font-display text-feature font-semibold text-neutral-900">
          Processing is taking longer than expected
        </p>
        <p className="mt-2 text-body text-neutral-400">
          The receipt may still be processing in the background. You can check back on the Receipts page.
        </p>
        <Link
          to="/receipts"
          className="mt-6 inline-flex items-center gap-2 text-body font-medium text-brand hover:underline"
        >
          Go to Receipts
        </Link>
      </div>
    )
  }

  // --- Processing state (with progress bar) ---
  if (phase === 'processing') {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <div className="h-12 w-12 animate-spin rounded-full border-4 border-neutral-200 border-t-brand" />
        <p className="mt-6 font-display text-feature font-semibold text-neutral-900">
          Scanning receipt
        </p>
        <ProgressBar stages={PROCESSING_STAGES} />
      </div>
    )
  }

  // --- Preparing state (resizing images) ---
  if (phase === 'preparing') {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <div className="h-12 w-12 animate-spin rounded-full border-4 border-neutral-200 border-t-brand" />
        <p className="mt-6 font-display text-feature font-semibold text-neutral-900">
          Preparing images...
        </p>
        <p className="mt-2 text-body text-neutral-400">
          Optimizing for faster processing.
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
      <input
        ref={fileInputRef}
        type="file"
        accept="image/*"
        capture="environment"
        className="hidden"
        onChange={handleFileChange}
      />

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

      {images.length > 0 && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          {images.map((img, index) => (
            <div key={img.previewUrl} className="group relative aspect-[3/4] overflow-hidden rounded-xl border border-neutral-200">
              <img
                src={img.previewUrl}
                alt={`Receipt page ${index + 1}`}
                className="h-full w-full object-cover"
              />
              <span className="absolute left-2 top-2 flex h-6 w-6 items-center justify-center rounded-md bg-neutral-900/70 text-small font-medium text-white">
                {index + 1}
              </span>
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

      {error && (
        <p className="text-caption text-expensive">{error}</p>
      )}

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
