import type { ApiError } from '@/types'

const TOKEN_KEY = 'cartledger_token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
}

function getBaseUrl(): string {
  return `${window.location.origin}/api/v1`
}

export class ApiClientError extends Error {
  status: number
  details?: string

  constructor(status: number, message: string, details?: string) {
    super(message)
    this.name = 'ApiClientError'
    this.status = status
    this.details = details
  }
}

async function handleResponse<T>(response: Response): Promise<T> {
  if (response.status === 401) {
    clearToken()
    window.location.href = '/login'
    throw new ApiClientError(401, 'Unauthorized')
  }

  if (!response.ok) {
    let errorBody: ApiError | undefined
    try {
      errorBody = await response.json() as ApiError
    } catch {
      // Response body is not JSON
    }
    throw new ApiClientError(
      response.status,
      errorBody?.error ?? `HTTP ${response.status}`,
      errorBody?.details,
    )
  }

  // 204 No Content
  if (response.status === 204) {
    return undefined as T
  }

  const data = await response.json()
  // Go encodes nil slices as null; normalize to empty array for list endpoints.
  if (data === null) return [] as unknown as T
  return data as T
}

function authHeaders(): HeadersInit {
  const token = getToken()
  if (!token) return {}
  return { Authorization: `Bearer ${token}` }
}

export async function get<T>(path: string): Promise<T> {
  const response = await fetch(`${getBaseUrl()}${path}`, {
    method: 'GET',
    headers: {
      ...authHeaders(),
      Accept: 'application/json',
    },
  })
  return handleResponse<T>(response)
}

export async function post<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(`${getBaseUrl()}${path}`, {
    method: 'POST',
    headers: {
      ...authHeaders(),
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  return handleResponse<T>(response)
}

export async function put<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(`${getBaseUrl()}${path}`, {
    method: 'PUT',
    headers: {
      ...authHeaders(),
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  return handleResponse<T>(response)
}

export async function del<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(`${getBaseUrl()}${path}`, {
    method: 'DELETE',
    headers: {
      ...authHeaders(),
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      Accept: 'application/json',
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  return handleResponse<T>(response)
}

export async function patch<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(`${getBaseUrl()}${path}`, {
    method: 'PATCH',
    headers: {
      ...authHeaders(),
      'Content-Type': 'application/json',
      Accept: 'application/json',
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  return handleResponse<T>(response)
}

export async function postMultipart<T>(path: string, formData: FormData): Promise<T> {
  const response = await fetch(`${getBaseUrl()}${path}`, {
    method: 'POST',
    headers: {
      ...authHeaders(),
      // Do NOT set Content-Type — browser sets it with boundary for multipart
    },
    body: formData,
  })
  return handleResponse<T>(response)
}
