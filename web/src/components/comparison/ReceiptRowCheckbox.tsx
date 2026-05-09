interface ReceiptRowCheckboxProps {
  checked: boolean
  disabled?: boolean
  label: string
  title?: string
  onChange: (checked: boolean) => void
}

export function ReceiptRowCheckbox({
  checked,
  disabled = false,
  label,
  title,
  onChange,
}: ReceiptRowCheckboxProps) {
  return (
    <span className="inline-flex items-center justify-center" title={title}>
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        aria-label={label}
        onChange={(event) => onChange(event.target.checked)}
        onClick={(event) => event.stopPropagation()}
        className="h-4 w-4 cursor-pointer accent-brand disabled:cursor-not-allowed disabled:opacity-40"
      />
    </span>
  )
}
