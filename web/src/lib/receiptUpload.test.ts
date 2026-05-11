import { describe, expect, it } from 'vitest'
import {
  blobStartsWithPdfHeader,
  MAX_RECEIPT_PAGES,
  normalizeReceiptImageFile,
  receiptTypeForFile,
  validatePageBudget,
  validateReceiptFile,
} from './receiptUpload'

describe('receipt upload validation', () => {
  it('accepts JPEG, PNG, and PDF files', () => {
    expect(validateReceiptFile(fileLike('photo.jpg', 'image/jpeg'))).toBeNull()
    expect(validateReceiptFile(fileLike('photo.png', 'image/png'))).toBeNull()
    expect(validateReceiptFile(fileLike('scan.pdf', 'application/pdf'))).toBeNull()
    expect(validateReceiptFile(fileLike('camera.JPG', ''))).toBeNull()
    expect(validateReceiptFile(fileLike('scan.PNG', ''))).toBeNull()
    expect(validateReceiptFile(fileLike('scanner-export.pdf', ''))).toBeNull()
  })

  it('infers supported types for unlabeled browser files by extension', () => {
    expect(receiptTypeForFile(fileLike('camera.jpeg', ''))).toBe('image/jpeg')
    expect(receiptTypeForFile(fileLike('scan.png', ''))).toBe('image/png')
    expect(receiptTypeForFile(fileLike('document.pdf', ''))).toBe('application/pdf')
    expect(receiptTypeForFile(fileLike('notes.txt', ''))).toBeNull()
  })

  it('normalizes empty image MIME types before upload', async () => {
    const jpeg = new File(['image-bytes'], 'camera.jpg', { type: '' })
    const normalized = normalizeReceiptImageFile(jpeg)

    expect(normalized.type).toBe('image/jpeg')
    expect(normalized.name).toBe('camera.jpg')
    expect(await normalized.text()).toBe('image-bytes')
  })

  it('rejects unsupported file types with PDF-aware copy', () => {
    expect(validateReceiptFile(fileLike('notes.txt', 'text/plain'))).toContain('JPEG, PNG, or PDF')
  })

  it('rejects files over the source size limit', () => {
    expect(validateReceiptFile(fileLike('large.pdf', 'application/pdf', 10 * 1024 * 1024 + 1))).toContain(
      'Maximum is 10 MB',
    )
  })

  it('counts mixed receipt pages against one shared limit', () => {
    expect(validatePageBudget(4, 6)).toBeNull()
    expect(validatePageBudget(4, 7)).toContain('Only 6 more pages')
    expect(validatePageBudget(MAX_RECEIPT_PAGES, 1)).toBe(`Maximum ${MAX_RECEIPT_PAGES} pages per receipt.`)
  })

  it('checks the PDF header before PDF.js parsing', async () => {
    await expect(blobStartsWithPdfHeader(new Blob(['%PDF-1.7']))).resolves.toBe(true)
    await expect(blobStartsWithPdfHeader(new Blob(['not a pdf']))).resolves.toBe(false)
  })
})

function fileLike(name: string, type: string, size = 1024): Pick<File, 'name' | 'type' | 'size'> {
  return { name, type, size }
}
