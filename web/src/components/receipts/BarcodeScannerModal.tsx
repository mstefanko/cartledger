import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Barcode, Camera, Check, Loader2, X } from 'lucide-react'
import { ApiClientError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import {
  applyLineItemBarcode,
  previewLineItemBarcode,
  type LineItemBarcodeApplyResponse,
  type LineItemBarcodePreview,
} from '@/api/receipts'
import type { LineItem } from '@/types'

type BarcodeScannerModalProps =
  | {
      open: boolean
      mode: 'fill'
      title?: string
      initialValue?: string
      onClose: () => void
      onFill: (upc: string) => void
    }
  | {
      open: boolean
      mode: 'apply-to-line'
      receiptId: string
      lineItem: LineItem
      onClose: () => void
      onApplied: (response: LineItemBarcodeApplyResponse) => void
    }

type ScannerState = 'idle' | 'starting' | 'scanning' | 'blocked' | 'unsupported' | 'error'

function normalizeGTIN(value: string): string {
  return value.replace(/[\s._-]+/g, '')
}

function validGTIN(value: string): boolean {
  const digits = normalizeGTIN(value)
  if (!/^\d{8}$|^\d{12}$|^\d{13}$|^\d{14}$/.test(digits)) return false
  let sum = 0
  let weight = 3
  for (let i = digits.length - 2; i >= 0; i--) {
    sum += Number(digits[i]) * weight
    weight = weight === 3 ? 1 : 3
  }
  const check = (10 - (sum % 10)) % 10
  return check === Number(digits[digits.length - 1])
}

function lookupSkipLabel(reason: string | undefined): string {
  switch (reason) {
    case 'env_disabled':
      return 'Lookup skipped: enrichment is disabled by the server.'
    case 'household_manual_lookup_disabled':
      return 'Lookup skipped: manual lookups are disabled in Settings.'
    case 'no_provider_configured':
      return 'Lookup skipped: no barcode provider is available.'
    case 'queue_failed':
      return 'Lookup skipped: enrichment queue failed after the row was saved.'
    default:
      return 'Lookup skipped.'
  }
}

function BarcodeScannerModal(props: BarcodeScannerModalProps) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const controlsRef = useRef<{ stop: () => void } | null>(null)
  const cameraSessionRef = useRef(0)
  const lastDecodedRef = useRef<{ value: string; at: number } | null>(null)
  const [scannerState, setScannerState] = useState<ScannerState>('idle')
  const [upc, setUpc] = useState(props.mode === 'fill' ? props.initialValue ?? '' : '')
  const [error, setError] = useState<string | null>(null)
  const [preview, setPreview] = useState<LineItemBarcodePreview | null>(null)

  const lineItem = props.mode === 'apply-to-line' ? props.lineItem : null
  const normalizedUPC = useMemo(() => normalizeGTIN(upc), [upc])
  const resetKey = props.mode === 'apply-to-line' ? props.lineItem.id : props.initialValue ?? ''

  const stopCamera = useCallback(() => {
    cameraSessionRef.current += 1
    try {
      controlsRef.current?.stop()
    } catch {
      // best effort
    }
    controlsRef.current = null
    const video = videoRef.current
    const stream = video?.srcObject
    if (stream instanceof MediaStream) {
      for (const track of stream.getTracks()) {
        track.stop()
      }
    }
    if (video) {
      video.srcObject = null
    }
    setScannerState((state) => (state === 'scanning' || state === 'starting' ? 'idle' : state))
  }, [])

  const handleDecoded = useCallback((value: string) => {
    const normalized = normalizeGTIN(value)
    if (!validGTIN(normalized)) return
    const now = Date.now()
    const last = lastDecodedRef.current
    if (last && last.value === normalized && now - last.at < 1200) return
    lastDecodedRef.current = { value: normalized, at: now }
    setUpc(normalized)
    setError(null)
    stopCamera()
  }, [stopCamera])

  const startCamera = useCallback(async () => {
    setError(null)
    if (!navigator.mediaDevices?.getUserMedia) {
      setScannerState(window.location.protocol === 'http:' && window.location.hostname !== 'localhost' ? 'unsupported' : 'blocked')
      return
    }
    setScannerState('starting')
    const session = cameraSessionRef.current + 1
    cameraSessionRef.current = session
    const isCurrentSession = () => cameraSessionRef.current === session && videoRef.current != null
    try {
      const permission = await navigator.permissions?.query?.({ name: 'camera' as PermissionName })
      if (permission?.state === 'denied') {
        setScannerState('blocked')
        return
      }
    } catch {
      // Safari may not support camera permission query; getUserMedia will report.
    }
    try {
      const { BrowserMultiFormatOneDReader } = await import('@zxing/browser')
      if (!isCurrentSession()) return
      const reader = new BrowserMultiFormatOneDReader()
      const controls = await reader.decodeFromConstraints(
        { video: { facingMode: { ideal: 'environment' } } },
        videoRef.current!,
        (result) => {
          const text = result?.getText()
          if (text) handleDecoded(text)
        },
      )
      if (!isCurrentSession()) {
        controls.stop()
        return
      }
      controlsRef.current = controls
      setScannerState('scanning')
    } catch (err) {
      if (!isCurrentSession()) return
      setScannerState('error')
      setError(err instanceof Error ? err.message : 'Camera scanner failed to start.')
      stopCamera()
    }
  }, [handleDecoded, stopCamera])

  useEffect(() => {
    if (!props.open) {
      stopCamera()
      return
    }
    const onVisibility = () => {
      if (document.visibilityState === 'hidden') stopCamera()
    }
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      document.removeEventListener('visibilitychange', onVisibility)
      stopCamera()
    }
  }, [props.open, stopCamera])

  useEffect(() => {
    if (!props.open) return
    setUpc(props.mode === 'fill' ? props.initialValue ?? '' : '')
    setPreview(null)
    setError(null)
    setScannerState('idle')
    lastDecodedRef.current = null
  }, [props.open, props.mode, resetKey])

  const previewMutation = useMutation({
    mutationFn: (value: string) => {
      if (props.mode !== 'apply-to-line') throw new Error('preview unavailable')
      return previewLineItemBarcode(props.receiptId, props.lineItem.id, value)
    },
    onSuccess: setPreview,
    onError: (err) => {
      setPreview(null)
      setError(err instanceof Error ? err.message : 'Barcode preview failed.')
    },
  })

  const applyMutation = useMutation({
    mutationFn: (createProduct: boolean) => {
      if (props.mode !== 'apply-to-line') throw new Error('apply unavailable')
      return applyLineItemBarcode(props.receiptId, props.lineItem.id, {
        upc: normalizedUPC,
        create_product: createProduct,
      })
    },
    onSuccess: (response) => {
      stopCamera()
      if (props.mode === 'apply-to-line') {
        props.onApplied(response)
      } else {
        props.onClose()
      }
    },
    onError: (err) => {
      if (props.mode === 'apply-to-line' && err instanceof ApiClientError && err.status === 409 && validGTIN(normalizedUPC)) {
        previewLineItemBarcode(props.receiptId, props.lineItem.id, normalizedUPC, true)
          .then((nextPreview) => {
            setPreview(nextPreview)
            setError(null)
          })
          .catch((previewErr) => {
            setError(previewErr instanceof Error ? previewErr.message : 'Barcode apply failed.')
          })
        return
      }
      setError(err instanceof Error ? err.message : 'Barcode apply failed.')
    },
  })

  const submitManual = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setError(null)
    setPreview(null)
    if (!validGTIN(upc)) {
      setError('Enter a valid 8, 12, 13, or 14 digit barcode.')
      return
    }
    if (props.mode === 'fill') {
      props.onFill(normalizedUPC)
      props.onClose()
      return
    }
    previewMutation.mutate(normalizedUPC)
  }

  const title = props.mode === 'fill' ? props.title ?? 'Scan UPC' : 'Scan Item Barcode'
  const canSubmit = validGTIN(upc) && !previewMutation.isPending && !applyMutation.isPending

  return (
    <Modal
      open={props.open}
      onClose={() => {
        stopCamera()
        props.onClose()
      }}
      title={title}
      footer={
        <>
          <Button type="button" variant="secondary" size="sm" onClick={props.onClose}>
            <X className="mr-1 h-4 w-4" aria-hidden="true" />
            Cancel
          </Button>
          {props.mode === 'fill' ? (
            <Button type="submit" form="barcode-form" size="sm" disabled={!canSubmit}>
              <Check className="mr-1 h-4 w-4" aria-hidden="true" />
              Use UPC
            </Button>
          ) : (
            <Button
              type="submit"
              form="barcode-form"
              size="sm"
              disabled={!canSubmit}
            >
              {previewMutation.isPending ? <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" /> : <Barcode className="mr-1 h-4 w-4" aria-hidden="true" />}
              Preview
            </Button>
          )}
        </>
      }
    >
      <div className="space-y-4">
        {lineItem && (
          <div className="rounded-lg bg-neutral-50 px-3 py-2">
            <p className="truncate text-body-medium font-semibold text-neutral-900">{lineItem.raw_name}</p>
            <p className="text-caption text-neutral-400">${Number(lineItem.total_price).toFixed(2)}</p>
          </div>
        )}

        <div className="overflow-hidden rounded-lg bg-neutral-900">
          <video ref={videoRef} className="aspect-video w-full object-cover" muted playsInline />
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant={scannerState === 'scanning' ? 'secondary' : 'subtle'}
            onClick={scannerState === 'scanning' ? stopCamera : startCamera}
            disabled={scannerState === 'starting'}
          >
            {scannerState === 'starting' ? <Loader2 className="mr-1 h-4 w-4 animate-spin" aria-hidden="true" /> : <Camera className="mr-1 h-4 w-4" aria-hidden="true" />}
            {scannerState === 'scanning' ? 'Stop camera' : 'Start camera'}
          </Button>
          {scannerState === 'blocked' && (
            <span className="text-small text-amber-700">Camera is blocked. Manual entry is still available.</span>
          )}
          {scannerState === 'unsupported' && (
            <span className="text-small text-amber-700">Use HTTPS or localhost for camera access.</span>
          )}
        </div>

        <form id="barcode-form" onSubmit={submitManual} className="space-y-2">
          <label className="block text-caption font-medium text-neutral-900">
            UPC
            <input
              type="text"
              inputMode="numeric"
              value={upc}
              onChange={(event) => {
                setUpc(event.target.value)
                setPreview(null)
                setError(null)
              }}
              className="mt-1 w-full rounded-lg border border-neutral-200 px-3 py-2 font-mono text-body focus:border-brand focus:outline-none focus:ring-2 focus:ring-brand"
              autoFocus
            />
          </label>
          {error && <p className="text-small text-expensive">{error}</p>}
        </form>

        {preview && props.mode === 'apply-to-line' && (
          <div className="rounded-lg border border-neutral-200 px-3 py-3">
            {preview.matched_product ? (
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p className="text-caption font-semibold text-neutral-900">
                    {preview.conflict ? 'UPC belongs to an existing product' : preview.matched_product.name}
                  </p>
                  {preview.conflict && (
                    <p className="mt-1 text-small text-neutral-500">
                      Switch this row to {preview.matched_product.name}?
                    </p>
                  )}
                  <p className="font-mono text-small text-neutral-400">{preview.upc}</p>
                </div>
                <div className="flex items-center gap-2">
                  {preview.conflict && (
                    <Button type="button" variant="secondary" size="sm" onClick={props.onClose} disabled={applyMutation.isPending}>
                      Cancel
                    </Button>
                  )}
                  <Button size="sm" onClick={() => applyMutation.mutate(false)} disabled={applyMutation.isPending}>
                    {preview.conflict ? 'Switch match' : 'Match row'}
                  </Button>
                </div>
              </div>
            ) : (
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p className="text-caption font-semibold text-neutral-900">No household product found</p>
                  <p className="font-mono text-small text-neutral-400">{preview.upc}</p>
                  {preview.lookup_skipped_reason && (
                    <p className="mt-1 text-small text-amber-700">{lookupSkipLabel(preview.lookup_skipped_reason)}</p>
                  )}
                </div>
                <Button size="sm" onClick={() => applyMutation.mutate(true)} disabled={applyMutation.isPending || !preview.create_product_allowed}>
                  Create product
                </Button>
              </div>
            )}
          </div>
        )}
      </div>
    </Modal>
  )
}

export { BarcodeScannerModal }
