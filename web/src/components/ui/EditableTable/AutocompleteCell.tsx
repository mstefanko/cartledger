import {
  useState,
  useRef,
  useEffect,
  useCallback,
  useMemo,
  type KeyboardEvent,
} from 'react'
import { createPortal } from 'react-dom'

export interface AutocompleteOption {
  id: string
  label: string
}

interface AutocompleteCellProps {
  value: string
  displayValue: string
  suggestedValue?: { name: string; type: string } | null
  rowIndex: number
  columnId: string
  isActive: boolean
  isEditing: boolean
  options: AutocompleteOption[]
  onSearch: (query: string) => void
  onChange: (rowIndex: number, columnId: string, optionId: string) => void
  onCreate?: (rowIndex: number, columnId: string, label: string) => void
  onStartEdit: () => void
  onKeyDown: (e: KeyboardEvent) => void
  onCancelEdit: () => void
}

function highlightMatch(text: string, query: string): React.ReactNode {
  if (!query) return text
  const idx = text.toLowerCase().indexOf(query.toLowerCase())
  if (idx === -1) return text
  return (
    <>
      {text.slice(0, idx)}
      <span className="font-semibold text-brand">{text.slice(idx, idx + query.length)}</span>
      {text.slice(idx + query.length)}
    </>
  )
}

function AutocompleteCell({
  value,
  displayValue,
  suggestedValue,
  rowIndex,
  columnId,
  isActive,
  isEditing,
  options,
  onSearch,
  onChange,
  onCreate,
  onStartEdit,
  onKeyDown,
  onCancelEdit,
}: AutocompleteCellProps) {
  // Pre-fill input with suggestion name when no matched product
  const initialInput = displayValue || suggestedValue?.name || ''
  const [inputValue, setInputValue] = useState(initialInput)
  const [highlightedIndex, setHighlightedIndex] = useState(0)
  const [dropdownOpen, setDropdownOpen] = useState(false)
  const [dropdownPos, setDropdownPos] = useState<{ top: number; left: number; width: number }>({
    top: 0,
    left: 0,
    width: 0,
  })

  const inputRef = useRef<HTMLInputElement>(null)
  const cellRef = useRef<HTMLDivElement>(null)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)

  // Include "Create new..." option
  const allOptions = useMemo(() => {
    const items = [...options]
    if (onCreate && inputValue.trim()) {
      items.push({ id: '__create__', label: `Create new "${inputValue.trim()}"` })
    }
    return items
  }, [options, onCreate, inputValue])

  // Sync display value when not editing
  useEffect(() => {
    if (!isEditing) {
      setInputValue(displayValue || suggestedValue?.name || '')
    }
  }, [displayValue, suggestedValue, isEditing])

  // Position dropdown and open when editing
  useEffect(() => {
    if (isEditing && inputRef.current) {
      inputRef.current.focus()
      inputRef.current.select()
      const rect = inputRef.current.getBoundingClientRect()
      setDropdownPos({
        top: rect.bottom + 2,
        left: rect.left,
        width: Math.max(rect.width, 240),
      })
      setDropdownOpen(true)
      setHighlightedIndex(0)
    } else {
      setDropdownOpen(false)
    }
  }, [isEditing])

  // Debounced search
  const handleInputChange = useCallback(
    (val: string) => {
      setInputValue(val)
      setHighlightedIndex(0)
      if (debounceRef.current) clearTimeout(debounceRef.current)
      debounceRef.current = setTimeout(() => {
        onSearch(val)
      }, 200)
    },
    [onSearch],
  )

  // Cleanup debounce timer
  useEffect(() => {
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [])

  const selectOption = useCallback(
    (option: AutocompleteOption) => {
      if (option.id === '__create__' && onCreate) {
        onCreate(rowIndex, columnId, inputValue.trim())
      } else {
        onChange(rowIndex, columnId, option.id)
      }
      setDropdownOpen(false)
      onCancelEdit()
    },
    [onChange, onCreate, rowIndex, columnId, inputValue, onCancelEdit],
  )

  const handleInputKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setHighlightedIndex((prev) => Math.min(prev + 1, allOptions.length - 1))
      } else if (e.key === 'ArrowUp') {
        e.preventDefault()
        setHighlightedIndex((prev) => Math.max(prev - 1, 0))
      } else if (e.key === 'Enter') {
        e.preventDefault()
        const selected = allOptions[highlightedIndex]
        if (selected) {
          selectOption(selected)
        }
      } else if (e.key === 'Escape') {
        e.preventDefault()
        setInputValue(displayValue || suggestedValue?.name || '')
        setDropdownOpen(false)
        onCancelEdit()
      } else if (e.key === 'Tab') {
        // Select current highlighted and let navigation handle tab
        const selected = allOptions[highlightedIndex]
        if (selected) {
          selectOption(selected)
        }
        onKeyDown(e as unknown as KeyboardEvent)
      }
    },
    [allOptions, highlightedIndex, selectOption, displayValue, onCancelEdit, onKeyDown],
  )

  const handleBlur = useCallback(() => {
    // Small delay to allow click on dropdown item
    setTimeout(() => {
      if (isEditing) {
        setDropdownOpen(false)
        setInputValue(displayValue || suggestedValue?.name || '')
        onCancelEdit()
      }
    }, 150)
  }, [isEditing, displayValue, suggestedValue, onCancelEdit])

  // Scroll highlighted option into view
  useEffect(() => {
    if (dropdownOpen && dropdownRef.current) {
      const el = dropdownRef.current.children[highlightedIndex] as HTMLElement | undefined
      el?.scrollIntoView({ block: 'nearest' })
    }
  }, [highlightedIndex, dropdownOpen])

  const handleCellKeyDown = useCallback(
    (e: KeyboardEvent) => {
      onKeyDown(e)
    },
    [onKeyDown],
  )

  // Focus cell div when active but not editing
  useEffect(() => {
    if (isActive && !isEditing && cellRef.current) {
      cellRef.current.focus()
    }
  }, [isActive, isEditing])

  const dropdown =
    dropdownOpen && isEditing
      ? createPortal(
          <div
            ref={dropdownRef}
            style={{
              position: 'fixed',
              top: dropdownPos.top,
              left: dropdownPos.left,
              width: dropdownPos.width,
              zIndex: 9999,
            }}
            className="bg-white border border-neutral-200 rounded-lg shadow-subtle max-h-60 overflow-y-auto"
          >
            {allOptions.length === 0 ? (
              <div className="px-3 py-2 text-caption text-neutral-400">No matches</div>
            ) : (
              allOptions.map((option, idx) => (
                <div
                  key={option.id}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    selectOption(option)
                  }}
                  onMouseEnter={() => setHighlightedIndex(idx)}
                  className={[
                    'px-3 py-2 text-caption font-body cursor-pointer',
                    idx === highlightedIndex ? 'bg-brand-subtle text-neutral-900' : 'text-neutral-900',
                    option.id === '__create__' ? 'border-t border-neutral-200 text-brand font-medium' : '',
                  ].join(' ')}
                >
                  {option.id === '__create__'
                    ? option.label
                    : highlightMatch(option.label, inputValue)}
                </div>
              ))
            )}
          </div>,
          document.body,
        )
      : null

  if (isEditing) {
    return (
      <>
        <input
          ref={inputRef}
          type="text"
          value={inputValue}
          onChange={(e) => handleInputChange(e.target.value)}
          onBlur={handleBlur}
          onKeyDown={handleInputKeyDown}
          className="w-full h-full px-2 py-1 text-caption font-body text-neutral-900 bg-white border border-brand rounded-sm focus:outline-none focus:ring-1 focus:ring-brand"
        />
        {dropdown}
      </>
    )
  }

  // Determine view mode display based on whether a product is matched (value = product_id)
  const hasMatch = !!value
  const viewText = displayValue || suggestedValue?.name || 'Unmatched'
  const isSuggested = !hasMatch && !!suggestedValue
  const isUnmatched = !hasMatch && !suggestedValue

  return (
    <>
      <div
        ref={cellRef}
        tabIndex={isActive ? 0 : -1}
        onClick={onStartEdit}
        onKeyDown={handleCellKeyDown}
        className={[
          'w-full h-full px-2 py-1 text-caption font-body cursor-pointer truncate',
          'leading-[36px]',
          isActive ? 'ring-2 ring-brand ring-inset rounded-sm' : '',
          isSuggested
            ? suggestedValue.type === 'new_product'
              ? 'text-blue-600 italic'
              : 'text-amber-600 italic'
            : isUnmatched
              ? 'text-neutral-400 italic'
              : 'text-neutral-900',
        ].join(' ')}
      >
        {viewText}
      </div>
      {dropdown}
    </>
  )
}

AutocompleteCell.displayName = 'AutocompleteCell'

export { AutocompleteCell }
export type { AutocompleteCellProps }
