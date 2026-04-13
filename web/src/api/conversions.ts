import { get, post, del } from './client'
import type { UnitConversion, CreateConversionRequest } from '@/types'

export async function listConversions(): Promise<UnitConversion[]> {
  return get<UnitConversion[]>('/conversions')
}

export async function createConversion(data: CreateConversionRequest): Promise<UnitConversion> {
  return post<UnitConversion>('/conversions', data)
}

export async function deleteConversion(id: string): Promise<void> {
  return del<void>(`/conversions/${encodeURIComponent(id)}`)
}
