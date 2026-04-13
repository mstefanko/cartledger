import { useState, useCallback, type KeyboardEvent } from 'react'

export type CellCoord = [rowIndex: number, colIndex: number]

interface UseTableNavigationOptions {
  rowCount: number
  /** All column indices */
  columnCount: number
  /** Set of column indices that are editable */
  editableColumns: Set<number>
  /** Called when active cell changes */
  onActiveCellChange?: (cell: CellCoord | null) => void
}

interface UseTableNavigationReturn {
  activeCell: CellCoord | null
  setActiveCell: (cell: CellCoord | null) => void
  handleKeyDown: (e: KeyboardEvent) => void
  isEditing: boolean
  setIsEditing: (editing: boolean) => void
}

export function useTableNavigation({
  rowCount,
  columnCount,
  editableColumns,
  onActiveCellChange,
}: UseTableNavigationOptions): UseTableNavigationReturn {
  const [activeCell, setActiveCellState] = useState<CellCoord | null>(null)
  const [isEditing, setIsEditing] = useState(false)

  const setActiveCell = useCallback(
    (cell: CellCoord | null) => {
      setActiveCellState(cell)
      onActiveCellChange?.(cell)
    },
    [onActiveCellChange],
  )

  const findNextEditableCol = useCallback(
    (fromCol: number, direction: 1 | -1): number | null => {
      let col = fromCol + direction
      while (col >= 0 && col < columnCount) {
        if (editableColumns.has(col)) return col
        col += direction
      }
      return null
    },
    [columnCount, editableColumns],
  )

  const findFirstEditableCol = useCallback((): number | null => {
    for (let i = 0; i < columnCount; i++) {
      if (editableColumns.has(i)) return i
    }
    return null
  }, [columnCount, editableColumns])

  const findLastEditableCol = useCallback((): number | null => {
    for (let i = columnCount - 1; i >= 0; i--) {
      if (editableColumns.has(i)) return i
    }
    return null
  }, [columnCount, editableColumns])

  const handleKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (!activeCell) return

      const [row, col] = activeCell

      // Escape: exit edit mode or deactivate cell
      if (e.key === 'Escape') {
        e.preventDefault()
        if (isEditing) {
          setIsEditing(false)
        } else {
          setActiveCell(null)
        }
        return
      }

      // Tab / Shift+Tab: move horizontally, wrap rows
      if (e.key === 'Tab') {
        e.preventDefault()
        setIsEditing(false)
        const direction = e.shiftKey ? -1 : 1
        const nextCol = findNextEditableCol(col, direction as 1 | -1)
        if (nextCol !== null) {
          setActiveCell([row, nextCol])
        } else {
          // Wrap to next/prev row
          const nextRow = row + direction
          if (nextRow >= 0 && nextRow < rowCount) {
            const wrapCol = direction === 1 ? findFirstEditableCol() : findLastEditableCol()
            if (wrapCol !== null) {
              setActiveCell([nextRow, wrapCol])
            }
          }
        }
        return
      }

      // Enter: commit and move down
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        if (isEditing) {
          setIsEditing(false)
          // Move down
          const nextRow = row + 1
          if (nextRow < rowCount) {
            setActiveCell([nextRow, col])
          }
        } else {
          // Enter edit mode
          setIsEditing(true)
        }
        return
      }

      // Arrow keys: only when not editing
      if (!isEditing) {
        switch (e.key) {
          case 'ArrowUp':
            e.preventDefault()
            if (row > 0) setActiveCell([row - 1, col])
            break
          case 'ArrowDown':
            e.preventDefault()
            if (row + 1 < rowCount) setActiveCell([row + 1, col])
            break
          case 'ArrowLeft':
            e.preventDefault()
            {
              const prev = findNextEditableCol(col, -1)
              if (prev !== null) setActiveCell([row, prev])
            }
            break
          case 'ArrowRight':
            e.preventDefault()
            {
              const next = findNextEditableCol(col, 1)
              if (next !== null) setActiveCell([row, next])
            }
            break
        }
      }
    },
    [
      activeCell,
      isEditing,
      rowCount,
      findNextEditableCol,
      findFirstEditableCol,
      findLastEditableCol,
      setActiveCell,
    ],
  )

  return {
    activeCell,
    setActiveCell,
    handleKeyDown,
    isEditing,
    setIsEditing,
  }
}
