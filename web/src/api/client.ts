import type { ApiError } from '@/types'

// --- Token shim (post cookie-auth cutover) -----------------------------------
//
// We migrated from Authorization: Bearer to an HttpOnly session cookie. These
// functions are kept as no-op shims so existing call sites (useAuth,
// useListLock, a handful of pages) keep compiling while we prune them. Cookie
// auth happens automatically via fetch `credentials: 'include'` below.
//
// getToken() always returns null — JavaScript cannot read HttpOnly cookies,
// which is the whole point. setToken()/clearToken() are no-ops; the server
// manages cookie lifecycle via Set-Cookie headers on /login, /setup, /join,
// and /logout.
export function getToken(): string | null {
  return null
}

export function setToken(_token: string): void {
  // no-op: the server sets the HttpOnly cookie
}

export function clearToken(): void {
  // no-op: the server clears the cookie on POST /logout
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
    // Cookie expired / missing. Send the user back to /login; the server has
    // already cleared (or never set) our cookie. Skip the redirect when the
    // user is already on a public route — otherwise useAuth's bootstrap
    // /profile probe hard-reloads /login on every mount, looping forever.
    const path = window.location.pathname
    const onPublic =
      path === '/login' || path === '/setup' || path.startsWith('/join/')
    if (!onPublic) {
      window.location.href = '/login'
    }
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

export async function get<T>(path: string): Promise<T> {
  const response = await fetch(`${getBaseUrl()}${path}`, {
    method: 'GET',
    credentials: 'include',
    headers: {
      Accept: 'application/json',
    },
  })
  return handleResponse<T>(response)
}

export async function post<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(`${getBaseUrl()}${path}`, {
    method: 'POST',
    credentials: 'include',
    headers: {
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
    credentials: 'include',
    headers: {
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
    credentials: 'include',
    headers: {
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
    credentials: 'include',
    headers: {
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
    credentials: 'include',
    headers: {
      // Do NOT set Content-Type — browser sets it with boundary for multipart
    },
    body: formData,
  })
  return handleResponse<T>(response)
}
