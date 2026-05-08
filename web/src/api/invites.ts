import { get, post, del } from './client'

export interface InviteListItem {
  id: string
  email: string | null
  expires_at: string
  created_at: string
  consumed_at: string | null
  revoked_at: string | null
  status: 'pending' | 'consumed' | 'revoked' | 'expired'
}

export interface CreateInviteRequest {
  email?: string
  ttl_days?: number
}

export interface InviteCreateResponse {
  id: string
  token: string
  url: string
  link: string
  expires_at: string
  expires_in: string
}

export async function listInvites(): Promise<InviteListItem[]> {
  return get<InviteListItem[]>('/invites')
}

export async function createInvite(data: CreateInviteRequest): Promise<InviteCreateResponse> {
  return post<InviteCreateResponse>('/invite', data)
}

export async function revokeInvite(id: string): Promise<void> {
  return del<void>(`/invites/${encodeURIComponent(id)}`)
}

export async function resendInvite(id: string): Promise<InviteCreateResponse> {
  return post<InviteCreateResponse>(`/invites/${encodeURIComponent(id)}/send`)
}
