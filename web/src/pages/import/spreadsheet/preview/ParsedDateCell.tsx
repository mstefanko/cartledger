interface ParsedDateCellProps {
  raw: string
  parsed: string
  error?: string
}

function ParsedDateCell({ raw, parsed, error }: ParsedDateCellProps) {
  const valid = !error && !!parsed
  return (
    <span className="inline-flex items-center gap-1" title={error}>
      <span className="text-neutral-500">{raw}</span>
      <span className="text-neutral-400">→</span>
      {valid ? (
        <span className="text-success-dark font-medium">{parsed}</span>
      ) : (
        <span className="text-expensive font-medium">Invalid</span>
      )}
    </span>
  )
}

export default ParsedDateCell
