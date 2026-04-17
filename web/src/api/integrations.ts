import { get, put, del, post } from './client'

export type IntegrationType = 'mealie'

export interface Integration {
  type: IntegrationType
  enabled: boolean
  configured: boolean
  base_url?: string
}

export interface MealieConfigBody {
  base_url: string
  token: string
}

export interface TestResult {
  ok: boolean
  message?: string
}

export async function listIntegrations(): Promise<Integration[]> {
  return get<Integration[]>('/integrations')
}

export async function updateIntegration(
  type: IntegrationType,
  body: MealieConfigBody,
): Promise<Integration> {
  return put<Integration>(`/integrations/${encodeURIComponent(type)}`, body)
}

export async function deleteIntegration(type: IntegrationType): Promise<void> {
  return del<void>(`/integrations/${encodeURIComponent(type)}`)
}

export async function testIntegration(
  type: IntegrationType,
  body: MealieConfigBody,
): Promise<TestResult> {
  return post<TestResult>(`/integrations/${encodeURIComponent(type)}/test`, body)
}
