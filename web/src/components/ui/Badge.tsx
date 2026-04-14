import type { ReactNode } from 'react'

type BadgeVariant = 'success' | 'warning' | 'error' | 'neutral'

interface BadgeProps {
  variant?: BadgeVariant
  children: ReactNode
  className?: string
}

const variantClasses: Record<BadgeVariant, string> = {
  success: 'bg-success-subtle text-success-dark',
  warning: 'bg-amber-100 text-amber-800',
  error: 'bg-expensive-subtle text-expensive',
  neutral: 'bg-neutral-50 text-neutral-600',
}

function Badge({ variant = 'neutral', children, className = '' }: BadgeProps) {
  return (
    <span
      className={[
        'inline-flex items-center px-2 py-0.5 rounded-md text-small font-medium',
        variantClasses[variant],
        className,
      ]
        .filter(Boolean)
        .join(' ')}
    >
      {children}
    </span>
  )
}

export { Badge }
export type { BadgeProps, BadgeVariant }
