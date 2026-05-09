import { get, post, put, del } from './client'
import type {
  StatusResponse,
  SetupRequest,
  SetupResponse,
  LoginRequest,
  LoginResponse,
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
//
// setup() accepts an optional bootstrap token for compatibility with logged
// first-run URLs. Current servers allow setup without it while no users exist.
export async function setup(
  data: SetupRequest,
  bootstrapToken?: string,
): Promise<SetupResponse> {
  const path = bootstrapToken
    ? `/setup?bootstrap=${encodeURIComponent(bootstrapToken)}`
    : '/setup'
  return post<SetupResponse>(path, data)
}

export async function login(data: LoginRequest): Promise<LoginResponse> {
  return post<LoginResponse>('/login', data)
}

export async function logout(): Promise<{ status: string }> {
  return post<{ status: string }>('/logout')
}

export async function validateInvite(token: string): Promise<ValidateInviteResponse> {
  return get<ValidateInviteResponse>(`/invite/${encodeURIComponent(token)}/validate`)
}

export async function join(data: JoinRequest): Promise<JoinResponse> {
  return post<JoinResponse>('/join', data)
}

export async function requestPasswordReset(email: string): Promise<void> {
  return post<void>('/password/reset/request', { email })
}

export async function confirmPasswordReset(token: string, password: string): Promise<LoginResponse> {
  return post<LoginResponse>('/password/reset/confirm', {
    token,
    new_password: password,
  })
}

export async function changePassword(currentPassword: string, newPassword: string): Promise<{ status: string }> {
  return post('/password/change', {
    current_password: currentPassword,
    new_password: newPassword,
  })
}

export async function getProfile(): Promise<{ user: { id: string; household_id: string; email: string; name: string; is_admin: boolean }; household_name: string }> {
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
