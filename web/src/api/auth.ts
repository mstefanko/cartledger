import { get, post, put, del } from './client'
import type {
  StatusResponse,
  SetupRequest,
  SetupResponse,
  LoginRequest,
  LoginResponse,
  InviteResponse,
  ValidateInviteResponse,
  JoinRequest,
  JoinResponse,
} from '@/types'

export async function getStatus(): Promise<StatusResponse> {
  return get<StatusResponse>('/status')
}

// setup / login / join: the server sets the session cookie via Set-Cookie.
// The response body carries the user object only (the `token` field is a
// legacy artefact that will be empty post-cutover; do NOT rely on it).
export async function setup(data: SetupRequest): Promise<SetupResponse> {
  return post<SetupResponse>('/setup', data)
}

export async function login(data: LoginRequest): Promise<LoginResponse> {
  return post<LoginResponse>('/login', data)
}

export async function logout(): Promise<{ status: string }> {
  return post<{ status: string }>('/logout')
}

export async function invite(): Promise<InviteResponse> {
  return post<InviteResponse>('/invite')
}

export async function validateInvite(token: string): Promise<ValidateInviteResponse> {
  return get<ValidateInviteResponse>(`/invite/${encodeURIComponent(token)}/validate`)
}

export async function join(data: JoinRequest): Promise<JoinResponse> {
  return post<JoinResponse>('/join', data)
}

export async function getProfile(): Promise<{ user: { id: string; household_id: string; email: string; name: string }; household_name: string }> {
  return get('/profile')
}

export async function updateProfile(data: { name?: string; email?: string }): Promise<{ status: string }> {
  return put('/profile', data)
}

export async function updateHousehold(data: { name: string }): Promise<{ status: string }> {
  return put('/household', data)
}

export async function deleteAllData(): Promise<{ status: string }> {
  return del('/household/data')
}
