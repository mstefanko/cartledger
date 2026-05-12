import { useState, useRef, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { scanReceipt, type ScanReceiptResponse } from '@/api/receipts'
import { Button } from '@/components/ui/Button'
import {
  isPdfFile,
  MAX_RECEIPT_PAGES,
  normalizeReceiptImageFile,
  RECEIPT_FILE_ACCEPT,
  RECEIPT_UPLOAD_JPEG_QUALITY,
  RECEIPT_UPLOAD_MAX_LONG_EDGE,
  type ReceiptPageSource,
  type ReceiptUploadPage,
  validatePageBudget,
  validateReceiptFile,
} from '@/lib/receiptUpload'

interface PageEntry {
  file: File
  previewUrl: string
  source: ReceiptPageSource
}

type ScannerPhase = 'capture' | 'preparing' | 'uploading' | 'error'

/**
 * Resize an image to fit within maxDim x maxDim while preserving aspect ratio.
 *
 * Uses createImageBitmap (preferred, handles EXIF orientation natively) with
 * canvas fallback. Step-down resizing for quality when scaling > 2x.
 * On any failure, returns the original file so the upload still works.
 */
async function resizeImage(file: File): Promise<File> {
  const normalizedFile = normalizeReceiptImageFile(file)
  try {
    // createImageBitmap handles EXIF orientation automatically
    const bitmap = await createImageBitmap(normalizedFile)
    const { width, height } = bitmap

    // Skip if already within bounds
    if (width <= RECEIPT_UPLOAD_MAX_LONG_EDGE && height <= RECEIPT_UPLOAD_MAX_LONG_EDGE) {
      bitmap.close()
      return normalizedFile
    }

    // Calculate target dimensions preserving aspect ratio
    const scale = Math.min(RECEIPT_UPLOAD_MAX_LONG_EDGE / width, RECEIPT_UPLOAD_MAX_LONG_EDGE / height)
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
      canvas.toBlob(resolve, 'image/jpeg', RECEIPT_UPLOAD_JPEG_QUALITY)
    )

    if (!blob) return normalizedFile // fallback to original

    return new File([blob], normalizedFile.name.replace(/\.\w+$/, '.jpg'), {
      type: 'image/jpeg',
      lastModified: normalizedFile.lastModified,
    })
  } catch {
    // Any failure (canvas OOM, unsupported format) — send original
    return normalizedFile
  }
}

function ReceiptScanner() {
  const navigate = useNavigate()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const pagesRef = useRef<PageEntry[]>([])
  const [pages, setPages] = useState<PageEntry[]>([])
  const [phase, setPhase] = useState<ScannerPhase>('capture')
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    pagesRef.current = pages
  }, [pages])

  // Clean up object URLs on unmount
  useEffect(() => {
    return () => {
      pagesRef.current.forEach((page) => URL.revokeObjectURL(page.previewUrl))
    }
  }, [])

  const uploadMutation = useMutation<ScanReceiptResponse, Error, ReceiptUploadPage[]>({
    mutationFn: scanReceipt,
    onSuccess: (receipt) => {
      navigate(`/receipts/${receipt.id}`)
    },
    onError: (err) => {
      setError(err.message || 'Upload failed. Please try again.')
      setPhase('error')
    },
  })

  const handleFileChange = useCallback(
    async (event: React.ChangeEvent<HTMLInputElement>) => {
      const files = Array.from(event.target.files ?? [])
      event.target.value = ''
      if (files.length === 0) return

      setError(null)
      setPhase('preparing')

      const newEntries: PageEntry[] = []
      let nextError: string | null = null

      try {
        for (const file of files) {
          const validationError = validateReceiptFile(file)
          if (validationError) {
            nextError ??= validationError
            continue
          }

          const budgetError = validatePageBudget(pages.length + newEntries.length, 1)
          if (budgetError) {
            nextError ??= budgetError
            break
          }

          if (isPdfFile(file)) {
            const remainingPageBudget = MAX_RECEIPT_PAGES - pages.length - newEntries.length
            try {
              const { renderPdfToJpegs } = await import('@/lib/pdfIngest')
              const renderedPages = await renderPdfToJpegs(file, { remainingPageBudget })
              newEntries.push(
                ...renderedPages.map((page) => ({
                  file: page.file,
                  previewUrl: URL.createObjectURL(page.file),
                  source: 'pdf_rendered' as const,
                })),
              )
            } catch (err) {
              nextError ??= err instanceof Error ? err.message : 'Failed to prepare the selected PDF.'
            }
            continue
          }

          newEntries.push({
            file,
            previewUrl: URL.createObjectURL(file),
            source: 'photo',
          })
        }

        if (newEntries.length > 0) {
          setPages((prev) => [...prev, ...newEntries])
        }

        if (nextError) {
          setError(nextError)
        }
      } catch (err) {
        newEntries.forEach((entry) => URL.revokeObjectURL(entry.previewUrl))
        setError(err instanceof Error ? err.message : 'Failed to prepare the selected receipt file.')
      } finally {
        setPhase('capture')
      }
    },
    [pages.length],
  )

  const removePage = useCallback((index: number) => {
    setPages((prev) => {
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
    if (pages.length === 0) return
    setError(null)
    setPhase('preparing')

    try {
      const resized = await Promise.all(
        pages.map(async (page) => ({
          file: await resizeImage(page.file),
          source: page.source,
        })),
      )
      setPhase('uploading')
      uploadMutation.mutate(resized)
    } catch {
      setError('Failed to process receipt pages. Please try again.')
      setPhase('error')
    }
  }, [pages, uploadMutation])

  const handleRetry = useCallback(() => {
    setError(null)
    setPhase('capture')
  }, [])

  // --- Preparing state (converting/resizing pages) ---
  if (phase === 'preparing') {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <div className="h-12 w-12 animate-spin rounded-full border-4 border-neutral-200 border-t-brand" />
        <p className="mt-6 font-display text-feature font-semibold text-neutral-900">
          Preparing receipt...
        </p>
        <p className="mt-2 text-body text-neutral-400">
          Converting and optimizing pages.
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
          Uploading receipt...
        </p>
        <p className="mt-2 text-body text-neutral-400">
          {pages.length} {pages.length === 1 ? 'page' : 'pages'} being sent.
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
        accept={RECEIPT_FILE_ACCEPT}
        multiple
        className="hidden"
        onChange={handleFileChange}
      />

      {pages.length === 0 && (
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
            Take Photo or Choose File
          </span>
          <span className="text-caption text-neutral-400">
            JPEG, PNG, or PDF, up to 10 pages and 10 MB per file
          </span>
        </button>
      )}

      {pages.length > 0 && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          {pages.map((page, index) => (
            <div key={page.previewUrl} className="group relative aspect-[3/4] overflow-hidden rounded-xl border border-neutral-200">
              <img
                src={page.previewUrl}
                alt={`Receipt page ${index + 1}`}
                className="h-full w-full object-cover"
              />
              <span className="absolute left-2 top-2 flex h-6 w-6 items-center justify-center rounded-md bg-neutral-900/70 text-small font-medium text-white">
                {index + 1}
              </span>
              <button
                type="button"
                onClick={() => removePage(index)}
                className="absolute right-2 top-2 flex h-7 w-7 items-center justify-center rounded-lg bg-neutral-900/70 text-white opacity-0 transition-opacity group-hover:opacity-100 hover:bg-expensive active:bg-expensive cursor-pointer"
                aria-label={`Remove page ${index + 1}`}
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

      {pages.length > 0 && (
        <div className="flex flex-col gap-3 sm:flex-row">
          {pages.length < MAX_RECEIPT_PAGES && (
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

      {pages.length > 0 && (
        <p className="text-center text-small text-neutral-400">
          {pages.length} of {MAX_RECEIPT_PAGES} pages
          {pages.length < MAX_RECEIPT_PAGES && ' — add more for long receipts'}
        </p>
      )}
    </div>
  )
}

export { ReceiptScanner }
