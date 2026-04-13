import { get, post, put, del } from './client'
import type {
  MatchingRule,
  MatchLineItemRequest,
  MatchLineItemResponse,
  CreateRuleRequest,
  UpdateRuleRequest,
} from '@/types'

export async function matchLineItem(
  lineItemId: string,
  data: MatchLineItemRequest,
): Promise<MatchLineItemResponse> {
  return put<MatchLineItemResponse>(
    `/line-items/${encodeURIComponent(lineItemId)}/match`,
    data,
  )
}

export async function listRules(): Promise<MatchingRule[]> {
  return get<MatchingRule[]>('/rules')
}

export async function createRule(data: CreateRuleRequest): Promise<MatchingRule> {
  return post<MatchingRule>('/rules', data)
}

export async function updateRule(id: string, data: UpdateRuleRequest): Promise<MatchingRule> {
  return put<MatchingRule>(`/rules/${encodeURIComponent(id)}`, data)
}

export async function deleteRule(id: string): Promise<void> {
  return del<void>(`/rules/${encodeURIComponent(id)}`)
}
