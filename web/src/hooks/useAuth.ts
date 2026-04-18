import {
  createContext,
  useContext,
  useState,
  useEffect,
  useCallback,
  createElement,
  type ReactNode,
} from 'react'
import {
  getStatus,
  getProfile,
  login as apiLogin,
  logout as apiLogout,
} from '@/api/auth'
import type { User, LoginRequest } from '@/types'

interface AuthContextValue {
  user: User | null
  // Retained for call-site back-compat; always null in cookie mode.
  token: string | null
  needsSetup: boolean
  isAuthenticated: boolean
  isLoading: boolean
  login: (data: LoginRequest) => Promise<void>
  logout: () => Promise<void>
  // setAuth was previously called after login/setup/join to stash the token
  // into localStorage and flip isAuthenticated. With cookie auth the server
  // owns cookie lifecycle, so this just records the user server-side-returned
  // profile into React state and clears needsSetup.
  setAuth: (user: User) => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

interface AuthProviderProps {
  children: ReactNode
}

function AuthProvider({ children }: AuthProviderProps) {
  const [user, setUser] = useState<User | null>(null)
  const [needsSetup, setNeedsSetup] = useState(false)
  const [isLoading, setIsLoading] = useState(true)

  useEffect(() => {
    let cancelled = false

    async function bootstrap() {
      try {
        // /status is public (no auth); tells us whether to route to /setup.
        const status = await getStatus()
        if (cancelled) return
        setNeedsSetup(status.needs_setup)
      } catch {
        // Status endpoint unavailable — assume no setup needed
      }

      // Probe /profile with the cookie (if any). 200 => authenticated;
      // anything else leaves user=null which signals not-authenticated.
      try {
        const resp = await getProfile()
        if (cancelled) return
        setUser({
          id: resp.user.id,
          household_id: resp.user.household_id,
          email: resp.user.email,
          name: resp.user.name,
          is_admin: resp.user.is_admin,
          created_at: '',
        })
      } catch {
        // Unauthenticated or profile fetch failed — user stays null.
      } finally {
        if (!cancelled) setIsLoading(false)
      }
    }

    void bootstrap()
    return () => {
      cancelled = true
    }
  }, [])

  const setAuth = useCallback((newUser: User) => {
    setUser(newUser)
    setNeedsSetup(false)
  }, [])

  const login = useCallback(
    async (data: LoginRequest) => {
      const response = await apiLogin(data)
      setAuth(response.user)
    },
    [setAuth],
  )

  const logout = useCallback(async () => {
    try {
      await apiLogout()
    } catch {
      // Best effort — even if the server call fails, drop local user state.
    }
    setUser(null)
  }, [])

  const value: AuthContextValue = {
    user,
    token: null,
    needsSetup,
    isAuthenticated: user !== null,
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
