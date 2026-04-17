import { useState, useRef, useEffect, useCallback, type KeyboardEvent, type ReactNode } from 'react'

interface EditableCellProps {
  value: string
  rowIndex: number
  columnId: string
  cellType: 'text' | 'number'
  isActive: boolean
  isEditing: boolean
  onStartEdit: () => void
  onCellUpdate: (rowIndex: number, columnId: string, value: string) => void
  onKeyDown: (e: KeyboardEvent) => void
  onCancelEdit: () => void
  /** Optional custom display content rendered when not editing. Falls back to `value`. */
  displayNode?: ReactNode
}

function EditableCell({
  value,
  rowIndex,
  columnId,
  cellType,
  isActive,
  isEditing,
  onStartEdit,
  onCellUpdate,
  onKeyDown,
  onCancelEdit,
  displayNode,
}: EditableCellProps) {
  const [editValue, setEditValue] = useState(value)
  const inputRef = useRef<HTMLInputElement>(null)
  const cellRef = useRef<HTMLDivElement>(null)

  // Sync edit value when value prop changes while not editing
  useEffect(() => {
    if (!isEditing) {
      setEditValue(value)
    }
  }, [value, isEditing])

  // Auto-focus and select input when entering edit mode
  useEffect(() => {
    if (isEditing && inputRef.current) {
      inputRef.current.focus()
      inputRef.current.select()
    }
  }, [isEditing])

  // Focus the cell div when active but not editing (for keyboard nav)
  useEffect(() => {
    if (isActive && !isEditing && cellRef.current) {
      cellRef.current.focus()
    }
  }, [isActive, isEditing])

  const commitEdit = useCallback(() => {
    if (editValue !== value) {
      onCellUpdate(rowIndex, columnId, editValue)
    }
  }, [editValue, value, rowIndex, columnId, onCellUpdate])

  const handleBlur = useCallback(() => {
    if (isEditing) {
      commitEdit()
      onCancelEdit()
    }
  }, [isEditing, commitEdit, onCancelEdit])

  const handleInputKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'Enter') {
        e.preventDefault()
        commitEdit()
        onKeyDown(e as unknown as KeyboardEvent)
      } else if (e.key === 'Escape') {
        e.preventDefault()
        setEditValue(value)
        onCancelEdit()
      } else if (e.key === 'Tab') {
        commitEdit()
        onKeyDown(e as unknown as KeyboardEvent)
      }
    },
    [commitEdit, onKeyDown, onCancelEdit, value],
  )

  const handleCellKeyDown = useCallback(
    (e: KeyboardEvent) => {
      // If not editing, pass key events to navigation handler
      onKeyDown(e)
    },
    [onKeyDown],
  )

  if (isEditing) {
    return (
      <input
        ref={inputRef}
        type={cellType === 'number' ? 'number' : 'text'}
        value={editValue}
        onChange={(e) => setEditValue(e.target.value)}
        onBlur={handleBlur}
        onKeyDown={handleInputKeyDown}
        className="w-full h-full px-2 py-1 text-caption font-body text-neutral-900 bg-white border border-brand rounded-sm focus:outline-none focus:ring-1 focus:ring-brand"
        step={cellType === 'number' ? 'any' : undefined}
      />
    )
  }

  return (
    <div
      ref={cellRef}
      tabIndex={isActive ? 0 : -1}
      onClick={onStartEdit}
      onKeyDown={handleCellKeyDown}
      className={[
        'w-full h-full px-2 py-1 text-caption font-body text-neutral-900 cursor-pointer truncate',
        'leading-[36px] border-b border-dashed border-neutral-200 hover:border-neutral-400 hover:bg-neutral-50/50 transition-colors',
        isActive ? 'ring-2 ring-brand ring-inset rounded-sm' : '',
      ].join(' ')}
      title="Click to edit"
    >
      {displayNode ?? value}
    </div>
  )
}

EditableCell.displayName = 'EditableCell'

export { EditableCell }
export type { EditableCellProps }
