import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { confirmPasswordReset } from '@/api/auth'
import { ApiClientError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'

function ResetPasswordPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const token = searchParams.get('token') ?? ''
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')

  const mutation = useMutation({
    mutationFn: () => confirmPasswordReset(token, password),
    onSuccess: () => {
      navigate('/login', { replace: true })
    },
  })

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (password !== confirm) return
    mutation.mutate()
  }

  const errorMessage =
    password && confirm && password !== confirm
      ? 'Passwords do not match.'
      : mutation.error instanceof ApiClientError
        ? mutation.error.message
        : mutation.error
          ? 'Unable to reset password.'
          : null

  return (
    <div className="min-h-screen flex items-center justify-center bg-white px-4">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="font-display text-section font-bold text-neutral-900 tracking-tight">
            Choose New Password
          </h1>
          <p className="mt-2 text-body text-neutral-400">
            This logs you in here. Other devices stay signed in until their session expires.
          </p>
        </div>

        {!token ? (
          <div className="bg-white rounded-2xl shadow-subtle p-6 text-center">
            <p className="text-body text-neutral-700">This reset link is missing a token.</p>
            <Link className="mt-5 inline-flex text-caption font-medium text-brand hover:text-brand-dark" to="/forgot-password">
              Request a new link
            </Link>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <Input
              label="New Password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              minLength={8}
              autoFocus
            />
            <Input
              label="Confirm Password"
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
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
              disabled={mutation.isPending || password !== confirm || password.length < 8}
            >
              {mutation.isPending ? 'Updating...' : 'Update Password'}
            </Button>
          </form>
        )}
      </div>
    </div>
  )
}

export default ResetPasswordPage
