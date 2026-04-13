import { ReceiptScanner } from '@/components/receipts/ReceiptScanner'

function ScanPage() {
  return (
    <div className="mx-auto max-w-lg py-8">
      <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight">
        Scan Receipt
      </h1>
      <p className="mt-2 text-body text-neutral-400">
        Take a photo of your receipt or choose an image from your device.
      </p>
      <div className="mt-6">
        <ReceiptScanner />
      </div>
    </div>
  )
}

export default ScanPage
