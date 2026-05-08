import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { useMutation } from '@tanstack/react-query'
import { requestPasswordReset } from '@/api/auth'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'

function ForgotPasswordPage() {
  const [email, setEmail] = useState('')

  const mutation = useMutation({
    mutationFn: () => requestPasswordReset(email),
  })

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    mutation.mutate()
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-white px-4">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="font-display text-section font-bold text-neutral-900 tracking-tight">
            Reset Password
          </h1>
          <p className="mt-2 text-body text-neutral-400">Enter the email for your household account.</p>
        </div>

        {mutation.isSuccess ? (
          <div className="bg-white rounded-2xl shadow-subtle p-6 text-center">
            <p className="text-body text-neutral-700">
              If an account exists for that email, a reset link has been sent.
            </p>
            <p className="mt-3 text-caption text-neutral-500">
              If email is not configured, contact your CartLedger administrator.
            </p>
            <Link className="mt-5 inline-flex text-caption font-medium text-brand hover:text-brand-dark" to="/login">
              Back to sign in
            </Link>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <Input
              label="Email"
              type="email"
              placeholder="jane@example.com"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              autoFocus
            />
            <Button type="submit" fullWidth disabled={mutation.isPending}>
              {mutation.isPending ? 'Sending...' : 'Send Reset Link'}
            </Button>
            <Link className="text-center text-caption font-medium text-brand hover:text-brand-dark" to="/login">
              Back to sign in
            </Link>
          </form>
        )}
      </div>
    </div>
  )
}

export default ForgotPasswordPage
