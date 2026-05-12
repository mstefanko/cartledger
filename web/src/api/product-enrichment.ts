import { get, put } from './client'
import type { ProductEnrichmentSettings } from '@/types'

export type ProductEnrichmentSettingsUpdate = Partial<{
  manual_lookup_enabled: boolean
  auto_on_scan_enabled: boolean
  scheduled_sweep_enabled: boolean
  provider_openfoodfacts_enabled: boolean
  provider_usda_fdc_enabled: boolean
  provider_kroger_enabled: boolean
  first_run_backfill_limit: number
}>

export async function getProductEnrichmentSettings(): Promise<ProductEnrichmentSettings> {
  return get<ProductEnrichmentSettings>('/product-enrichment/settings')
}

export async function updateProductEnrichmentSettings(
  data: ProductEnrichmentSettingsUpdate,
): Promise<ProductEnrichmentSettings> {
  return put<ProductEnrichmentSettings>('/product-enrichment/settings', data)
}
