import {
  getDocument,
  GlobalWorkerOptions,
  PasswordResponses,
  type PDFDocumentLoadingTask,
  type PDFDocumentProxy,
  type PDFPageProxy,
} from 'pdfjs-dist'
// Vite turns this PDF.js worker import into a served asset URL in dev and build.
import workerURL from 'pdfjs-dist/build/pdf.worker.mjs?url'
import {
  blobStartsWithPdfHeader,
  RECEIPT_UPLOAD_JPEG_QUALITY,
  RECEIPT_UPLOAD_MAX_LONG_EDGE,
  validatePageBudget,
} from '@/lib/receiptUpload'

GlobalWorkerOptions.workerSrc = workerURL

export class PdfIngestError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'PdfIngestError'
  }
}

export interface RenderPdfOptions {
  remainingPageBudget: number
  maxLongEdge?: number
  jpegQuality?: number
}

export interface RenderedPdfPage {
  file: File
  pageNumber: number
}

export async function renderPdfToJpegs(
  file: File,
  options: RenderPdfOptions,
): Promise<RenderedPdfPage[]> {
  if (!(await blobStartsWithPdfHeader(file))) {
    throw new PdfIngestError(
      `"${file.name}" does not look like a valid PDF file.`,
    )
  }

  const bytes = new Uint8Array(await file.arrayBuffer())
  const loadingTask = getDocument({
    data: bytes,
    stopAtErrors: true,
    isEvalSupported: false,
  })
  rejectPasswordRequests(loadingTask, file.name)

  let pdf: PDFDocumentProxy | null = null
  try {
    pdf = await loadingTask.promise
    const budgetError = validatePageBudget(0, pdf.numPages, options.remainingPageBudget)
    if (budgetError) {
      throw new PdfIngestError(
        `"${file.name}" has ${pdf.numPages} pages. ${budgetError}`,
      )
    }

    const pages: RenderedPdfPage[] = []
    for (let pageNumber = 1; pageNumber <= pdf.numPages; pageNumber += 1) {
      const page = await pdf.getPage(pageNumber)
      try {
        const rendered = await renderPdfPage(file, page, pageNumber, options)
        pages.push(rendered)
      } finally {
        page.cleanup()
      }
    }
    return pages
  } catch (err) {
    if (err instanceof PdfIngestError) throw err
    throw mapPdfError(err, file.name)
  } finally {
    await pdf?.destroy()
    if (!pdf) {
      await loadingTask.destroy().catch(() => undefined)
    }
  }
}

function rejectPasswordRequests(loadingTask: PDFDocumentLoadingTask, filename: string) {
  loadingTask.onPassword = (updatePassword: (password: string | Error) => void, reason: number) => {
    const message =
      reason === PasswordResponses.INCORRECT_PASSWORD
        ? `"${filename}" could not be opened with the provided password.`
        : `"${filename}" is password protected. Remove the password and try again.`
    // PDF.js accepts an Error here and rejects the loading task with it.
    updatePassword(new PdfIngestError(message))
  }
}

async function renderPdfPage(
  file: File,
  page: PDFPageProxy,
  pageNumber: number,
  options: RenderPdfOptions,
): Promise<RenderedPdfPage> {
  const maxLongEdge = options.maxLongEdge ?? RECEIPT_UPLOAD_MAX_LONG_EDGE
  const quality = options.jpegQuality ?? RECEIPT_UPLOAD_JPEG_QUALITY
  const viewport = page.getViewport({ scale: 1 })
  const longEdge = Math.max(viewport.width, viewport.height)
  const scale = longEdge > 0 ? maxLongEdge / longEdge : 1
  const renderViewport = page.getViewport({ scale })

  const canvas = document.createElement('canvas')
  canvas.width = Math.max(1, Math.round(renderViewport.width))
  canvas.height = Math.max(1, Math.round(renderViewport.height))
  const context = canvas.getContext('2d')
  if (!context) {
    throw new PdfIngestError(`Could not render page ${pageNumber} of "${file.name}".`)
  }

  try {
    await page.render({
      canvasContext: context,
      viewport: renderViewport,
      background: 'white',
    }).promise

    const blob = await new Promise<Blob | null>((resolve) => {
      canvas.toBlob(resolve, 'image/jpeg', quality)
    })
    if (!blob) {
      throw new PdfIngestError(`Could not encode page ${pageNumber} of "${file.name}".`)
    }

    return {
      file: new File([blob], renderedPdfPageName(file.name, pageNumber), {
        type: 'image/jpeg',
        lastModified: file.lastModified,
      }),
      pageNumber,
    }
  } finally {
    canvas.width = 0
    canvas.height = 0
  }
}

export function renderedPdfPageName(filename: string, pageNumber: number): string {
  const stem = filename.replace(/\.pdf$/i, '')
  return `${stem}.page-${pageNumber}.jpg`
}

function mapPdfError(err: unknown, filename: string): PdfIngestError {
  if (isPasswordError(err)) {
    return new PdfIngestError(
      `"${filename}" is password protected. Remove the password and try again.`,
    )
  }

  const message = err instanceof Error ? err.message : String(err)
  return new PdfIngestError(
    `"${filename}" could not be read as a PDF.${message ? ` ${message}` : ''}`,
  )
}

function isPasswordError(err: unknown): boolean {
  return (
    err instanceof PdfIngestError ||
    (err instanceof Error && err.name === 'PasswordException')
  )
}
