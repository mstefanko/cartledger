export const MAX_RECEIPT_PAGES = 10
export const MAX_RECEIPT_SOURCE_FILE_SIZE = 10 * 1024 * 1024 // 10MB
export const RECEIPT_UPLOAD_MAX_LONG_EDGE = 1600
export const RECEIPT_UPLOAD_JPEG_QUALITY = 0.85
export const RECEIPT_FILE_ACCEPT = 'image/jpeg,image/png,application/pdf'

export const ACCEPTED_RECEIPT_TYPES = [
  'image/jpeg',
  'image/png',
  'application/pdf',
] as const

export type ReceiptPageSource = 'photo' | 'pdf_rendered'

export interface ReceiptUploadPage {
  file: File
  source: ReceiptPageSource
}

type AcceptedReceiptType = (typeof ACCEPTED_RECEIPT_TYPES)[number]

export function isPdfFile(file: Pick<File, 'type' | 'name'>): boolean {
  return receiptTypeForFile(file) === 'application/pdf'
}

export function receiptTypeForFile(file: Pick<File, 'type' | 'name'>): AcceptedReceiptType | null {
  if (ACCEPTED_RECEIPT_TYPES.includes(file.type as AcceptedReceiptType)) {
    return file.type as AcceptedReceiptType
  }
  if (file.type !== '') return null

  const lowerName = file.name.toLowerCase()
  if (lowerName.endsWith('.jpg') || lowerName.endsWith('.jpeg')) return 'image/jpeg'
  if (lowerName.endsWith('.png')) return 'image/png'
  if (lowerName.endsWith('.pdf')) return 'application/pdf'
  return null
}

export function normalizeReceiptImageFile(file: File): File {
  const type = receiptTypeForFile(file)
  if ((type !== 'image/jpeg' && type !== 'image/png') || file.type === type) {
    return file
  }
  return new File([file], file.name, {
    type,
    lastModified: file.lastModified,
  })
}

export function validateReceiptFile(file: Pick<File, 'name' | 'type' | 'size'>): string | null {
  if (receiptTypeForFile(file) === null) {
    return `"${file.name}" is not a supported format. Use JPEG, PNG, or PDF.`
  }
  if (file.size > MAX_RECEIPT_SOURCE_FILE_SIZE) {
    const sizeMB = (file.size / (1024 * 1024)).toFixed(1)
    return `"${file.name}" is ${sizeMB} MB. Maximum is 10 MB.`
  }
  return null
}

export function validatePageBudget(
  currentPages: number,
  incomingPages: number,
  maxPages = MAX_RECEIPT_PAGES,
): string | null {
  if (incomingPages <= 0) return null
  if (currentPages + incomingPages > maxPages) {
    const remaining = Math.max(maxPages - currentPages, 0)
    if (remaining === 0) {
      return `Maximum ${maxPages} pages per receipt.`
    }
    return `Only ${remaining} more ${remaining === 1 ? 'page' : 'pages'} can be added to this receipt.`
  }
  return null
}

export async function blobStartsWithPdfHeader(blob: Blob): Promise<boolean> {
  const header = await blob.slice(0, 5).arrayBuffer()
  const bytes = new Uint8Array(header)
  return (
    bytes.length === 5 &&
    bytes[0] === 0x25 && // %
    bytes[1] === 0x50 && // P
    bytes[2] === 0x44 && // D
    bytes[3] === 0x46 && // F
    bytes[4] === 0x2d // -
  )
}
