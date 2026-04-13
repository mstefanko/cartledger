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
import { getStatus, login as apiLogin } from '@/api/auth'
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

    void checkStatus()
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
