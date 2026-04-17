import {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  createElement,
  type ReactNode,
} from 'react'
import { getToken, setToken, clearToken } from '@/api/client'
import { getStatus, getProfile, login as apiLogin } from '@/api/auth'
import type { User, LoginRequest } from '@/types'

interface AuthContextValue {
  user: User | null
  token: string | null
  needsSetup: boolean
  isAuthenticated: boolean
  isLoading: boolean
  login: (data: LoginRequest) => Promise<void>
  logout: () => void
  setAuth: (token: string, user: User) => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

interface AuthProviderProps {
  children: ReactNode
}

function AuthProvider({ children }: AuthProviderProps) {
  const [user, setUser] = useState<User | null>(null)
  const [token, setTokenState] = useState<string | null>(getToken)
  const [needsSetup, setNeedsSetup] = useState(false)
  const [isLoading, setIsLoading] = useState(true)

  useEffect(() => {
    let cancelled = false

    async function checkStatus() {
      try {
        const status = await getStatus()
        if (cancelled) return
        setNeedsSetup(status.needs_setup)
      } catch {
        // Status endpoint unavailable — assume no setup needed
      } finally {
        if (!cancelled) {
          setIsLoading(false)
        }
      }
    }

    // Hydrate user from /profile on mount when we have a token but no user in
    // state (e.g. after a page refresh). Without this, consumers like
    // useListLock get `currentUserId=''` and can't recognize self-owned locks.
    async function hydrateUser() {
      const existing = getToken()
      if (!existing) return
      try {
        const resp = await getProfile()
        if (cancelled) return
        const u: User = {
          id: resp.user.id,
          household_id: resp.user.household_id,
          email: resp.user.email,
          name: resp.user.name,
          created_at: '',
        }
        setUser(u)
      } catch {
        // Token expired or profile fetch failed — clear token so
        // ProtectedRoute redirects to login.
        if (cancelled) return
        clearToken()
        setTokenState(null)
      }
    }

    void checkStatus()
    void hydrateUser()
    return () => {
      cancelled = true
    }
  }, [])

  const setAuth = useCallback((newToken: string, newUser: User) => {
    setToken(newToken)
    setTokenState(newToken)
    setUser(newUser)
    setNeedsSetup(false)
  }, [])

  const login = useCallback(
    async (data: LoginRequest) => {
      const response = await apiLogin(data)
      setAuth(response.token, response.user)
    },
    [setAuth],
  )

  const logout = useCallback(() => {
    clearToken()
    setTokenState(null)
    setUser(null)
  }, [])

  const value: AuthContextValue = {
    user,
    token,
    needsSetup,
    isAuthenticated: token !== null,
    isLoading,
    login,
    logout,
    setAuth,
  }

  return createElement(AuthContext.Provider, { value }, children)
}

function useAuth(): AuthContextValue {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return context
}

export { AuthProvider, useAuth }
export type { AuthContextValue }
