import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { PasswordResponses } from 'pdfjs-dist'
import { renderedPdfPageName, renderPdfToJpegs } from './pdfIngest'

const pdfjsMock = vi.hoisted(() => ({
  getDocument: vi.fn(),
  globalWorkerOptions: {} as { workerSrc?: string },
  passwordResponses: {
    NEED_PASSWORD: 1,
    INCORRECT_PASSWORD: 2,
  },
}))

vi.mock('pdfjs-dist', () => ({
  getDocument: pdfjsMock.getDocument,
  GlobalWorkerOptions: pdfjsMock.globalWorkerOptions,
  PasswordResponses: pdfjsMock.passwordResponses,
}))

vi.mock('pdfjs-dist/build/pdf.worker.mjs?url', () => ({
  default: '/mock-pdf-worker.mjs',
}))

describe('pdf ingest', () => {
  beforeEach(() => {
    pdfjsMock.getDocument.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('creates stable rendered page names', () => {
    expect(renderedPdfPageName('receipt.pdf', 3)).toBe('receipt.page-3.jpg')
    expect(renderedPdfPageName('receipt.PDF', 1)).toBe('receipt.page-1.jpg')
    expect(renderedPdfPageName('receipt-export', 2)).toBe('receipt-export.page-2.jpg')
  })

  it('rejects files that do not start with a PDF header before calling PDF.js', async () => {
    const file = new File(['not a pdf'], 'fake.pdf', { type: 'application/pdf' })

    await expect(renderPdfToJpegs(file, { remainingPageBudget: 10 })).rejects.toThrow(
      'does not look like a valid PDF',
    )
    expect(pdfjsMock.getDocument).not.toHaveBeenCalled()
  })

  it('rejects PDFs that exceed the remaining page budget and cleans up the document', async () => {
    const destroy = vi.fn().mockResolvedValue(undefined)
    pdfjsMock.getDocument.mockReturnValue({
      promise: Promise.resolve({
        numPages: 11,
        destroy,
      }),
      destroy: vi.fn(),
    })

    const file = new File(['%PDF-1.7'], 'long.pdf', { type: 'application/pdf' })

    await expect(renderPdfToJpegs(file, { remainingPageBudget: 10 })).rejects.toThrow('has 11 pages')
    expect(destroy).toHaveBeenCalledOnce()
  })

  it('turns PDF.js password prompts into user-facing password errors', async () => {
    const destroy = vi.fn().mockResolvedValue(undefined)
    pdfjsMock.getDocument.mockImplementation(() => {
      const loadingTask: {
        onPassword?: (updatePassword: (password: string | Error) => void, reason: number) => void
        promise?: Promise<never>
        destroy: typeof destroy
      } = { destroy }
      loadingTask.promise = new Promise((_, reject) => {
        queueMicrotask(() => {
          loadingTask.onPassword?.((password) => {
            reject(password)
          }, PasswordResponses.NEED_PASSWORD)
        })
      })
      return loadingTask
    })

    const file = new File(['%PDF-1.7'], 'locked.pdf', { type: 'application/pdf' })

    await expect(renderPdfToJpegs(file, { remainingPageBudget: 10 })).rejects.toThrow(
      '"locked.pdf" is password protected',
    )
    expect(destroy).toHaveBeenCalledOnce()
  })

  it('renders PDF pages sequentially to JPEG files', async () => {
    const toBlob = vi.fn((callback: BlobCallback, type?: string, quality?: number) => {
      callback(new Blob(['jpeg-page'], { type }))
      expect(type).toBe('image/jpeg')
      expect(quality).toBe(0.85)
    })
    const canvas = {
      width: 0,
      height: 0,
      getContext: vi.fn(() => ({ drawImage: vi.fn() })),
      toBlob,
    }
    vi.stubGlobal('document', {
      createElement: vi.fn(() => canvas),
    })

    const cleanup = vi.fn()
    const render = vi.fn(() => ({ promise: Promise.resolve() }))
    const getViewport = vi
      .fn()
      .mockReturnValueOnce({ width: 800, height: 2000 })
      .mockReturnValueOnce({ width: 640, height: 1600 })
    const destroy = vi.fn().mockResolvedValue(undefined)
    pdfjsMock.getDocument.mockReturnValue({
      promise: Promise.resolve({
        numPages: 1,
        getPage: vi.fn().mockResolvedValue({
          cleanup,
          getViewport,
          render,
        }),
        destroy,
      }),
      destroy: vi.fn(),
    })

    const file = new File(['%PDF-1.7'], 'receipt.pdf', {
      type: 'application/pdf',
      lastModified: 123,
    })

    const pages = await renderPdfToJpegs(file, { remainingPageBudget: 10 })

    expect(pages).toHaveLength(1)
    expect(pages[0]?.pageNumber).toBe(1)
    expect(pages[0]?.file.name).toBe('receipt.page-1.jpg')
    expect(pages[0]?.file.type).toBe('image/jpeg')
    expect(pages[0]?.file.lastModified).toBe(123)
    expect(getViewport).toHaveBeenNthCalledWith(1, { scale: 1 })
    expect(getViewport).toHaveBeenNthCalledWith(2, { scale: 0.8 })
    expect(render).toHaveBeenCalledOnce()
    expect(cleanup).toHaveBeenCalledOnce()
    expect(destroy).toHaveBeenCalledOnce()
    expect(canvas.width).toBe(0)
    expect(canvas.height).toBe(0)
  })
})
