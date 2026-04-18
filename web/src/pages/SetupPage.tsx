import { useState, type FormEvent } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { setup } from '@/api/auth'
import { useAuth } from '@/hooks/useAuth'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import type { SetupRequest } from '@/types'
import { ApiClientError } from '@/api/client'

function SetupPage() {
  const navigate = useNavigate()
  const { setAuth } = useAuth()

  // The one-time bootstrap token is printed to the server's stderr on first
  // boot as part of a "setup URL". Without it, POST /setup returns 401 — so
  // we require it to be present in the query string.
  const [searchParams] = useSearchParams()
  const bootstrapToken = searchParams.get('bootstrap') ?? ''

  const [householdName, setHouseholdName] = useState('')
  const [userName, setUserName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  const mutation = useMutation({
    mutationFn: (data: SetupRequest) => setup(data, bootstrapToken),
    onSuccess: (response) => {
      // Cookie is set by the server on POST /setup. We only record the user
      // into React state here.
      setAuth(response.user)
      navigate('/', { replace: true })
    },
  })

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!bootstrapToken) {
      // Mutation would fail with 401 anyway; short-circuit with a clearer
      // message.
      return
    }
    mutation.mutate({
      household_name: householdName,
      user_name: userName,
      email,
      password,
    })
  }

  const missingTokenMessage = !bootstrapToken
    ? 'Setup requires the bootstrap URL printed at server startup — open it from the server logs.'
    : null

  const errorMessage =
    missingTokenMessage ??
    (mutation.error instanceof ApiClientError
      ? mutation.error.message
      : mutation.error
        ? 'Something went wrong. Please try again.'
        : null)

  return (
    <div className="min-h-screen flex items-center justify-center bg-white px-4">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="font-display text-section font-bold text-neutral-900 tracking-tight">
            Welcome to CartLedger
          </h1>
          <p className="mt-2 text-body text-neutral-400">
            Set up your household to get started.
          </p>
        </div>

        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <Input
            label="Household Name"
            placeholder="e.g. The Smiths"
            value={householdName}
            onChange={(e) => setHouseholdName(e.target.value)}
            required
            autoFocus
          />
          <Input
            label="Your Name"
            placeholder="e.g. Jane"
            value={userName}
            onChange={(e) => setUserName(e.target.value)}
            required
          />
          <Input
            label="Email"
            type="email"
            placeholder="jane@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
          />
          <Input
            label="Password"
            type="password"
            placeholder="Choose a password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            minLength={8}
          />

          {errorMessage && (
            <p className="text-small text-expensive" role="alert">
              {errorMessage}
            </p>
          )}

          <Button
            type="submit"
            fullWidth
            disabled={mutation.isPending || !bootstrapToken}
          >
            {mutation.isPending ? 'Setting up...' : 'Create Household'}
          </Button>
        </form>
      </div>
    </div>
  )
}

export default SetupPage
