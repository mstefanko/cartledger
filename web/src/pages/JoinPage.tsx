import { useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useQuery, useMutation } from '@tanstack/react-query'
import { validateInvite, join } from '@/api/auth'
import { useAuth } from '@/hooks/useAuth'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import type { JoinRequest } from '@/types'
import { ApiClientError } from '@/api/client'

function JoinPage() {
  const { token } = useParams<{ token: string }>()
  const navigate = useNavigate()
  const { setAuth } = useAuth()

  const [userName, setUserName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')

  const inviteQuery = useQuery({
    queryKey: ['invite', token],
    queryFn: () => validateInvite(token!),
    enabled: !!token,
    retry: false,
  })

  const mutation = useMutation({
    mutationFn: (data: JoinRequest) => join(data),
    onSuccess: (response) => {
      // Cookie is set by the server on POST /join. We only record the user
      // into React state here.
      setAuth(response.user)
      navigate('/', { replace: true })
    },
  })

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!token) return
    mutation.mutate({
      token,
      user_name: userName,
      email,
      password,
    })
  }

  const mutationError =
    mutation.error instanceof ApiClientError
      ? mutation.error.message
      : mutation.error
        ? 'Something went wrong. Please try again.'
        : null

  if (!token) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-white px-4">
        <p className="text-body text-neutral-400">Invalid invite link.</p>
      </div>
    )
  }

  if (inviteQuery.isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-white px-4">
        <p className="text-body text-neutral-400">Validating invite...</p>
      </div>
    )
  }

  if (inviteQuery.isError) {
    const msg =
      inviteQuery.error instanceof ApiClientError
        ? inviteQuery.error.message
        : 'This invite link is invalid or has expired.'
    return (
      <div className="min-h-screen flex items-center justify-center bg-white px-4">
        <div className="text-center">
          <h1 className="font-display text-subhead font-bold text-neutral-900">Invalid Invite</h1>
          <p className="mt-2 text-body text-neutral-400">{msg}</p>
        </div>
      </div>
    )
  }

  const invite = inviteQuery.data

  return (
    <div className="min-h-screen flex items-center justify-center bg-white px-4">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="font-display text-section font-bold text-neutral-900 tracking-tight">
            Join {invite?.household_name}
          </h1>
          <p className="mt-2 text-body text-neutral-400">
            {invite?.invited_by} invited you to their household.
          </p>
        </div>

        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <Input
            label="Your Name"
            placeholder="e.g. Jane"
            value={userName}
            onChange={(e) => setUserName(e.target.value)}
            required
            autoFocus
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

          {mutationError && (
            <p className="text-small text-expensive" role="alert">
              {mutationError}
            </p>
          )}

          <Button type="submit" fullWidth disabled={mutation.isPending}>
            {mutation.isPending ? 'Joining...' : 'Join Household'}
          </Button>
        </form>
      </div>
    </div>
  )
}

export default JoinPage
