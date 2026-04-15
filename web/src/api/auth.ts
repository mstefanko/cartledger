import { get, post, put, del, setToken } from './client'
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

export async function setup(data: SetupRequest): Promise<SetupResponse> {
  const response = await post<SetupResponse>('/setup', data)
  setToken(response.token)
  return response
}

export async function login(data: LoginRequest): Promise<LoginResponse> {
  const response = await post<LoginResponse>('/login', data)
  setToken(response.token)
  return response
}

export async function invite(): Promise<InviteResponse> {
  return post<InviteResponse>('/invite')
}

export async function validateInvite(token: string): Promise<ValidateInviteResponse> {
  return get<ValidateInviteResponse>(`/invite/${encodeURIComponent(token)}/validate`)
}

export async function join(data: JoinRequest): Promise<JoinResponse> {
  const response = await post<JoinResponse>('/join', data)
  setToken(response.token)
  return response
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
