import { useRef, useMemo, useCallback, type KeyboardEvent } from 'react'
import {
  useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  flexRender,
  type ColumnDef,
  type SortingState,
} from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useState } from 'react'
import { useTableNavigation } from './useTableNavigation'
import { EditableCell } from './EditableCell'
import { AutocompleteCell, type AutocompleteOption } from './AutocompleteCell'

const ROW_HEIGHT = 36

declare module '@tanstack/react-table' {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  interface ColumnMeta<TData, TValue> {
    editable?: boolean
    cellType?: 'text' | 'number' | 'autocomplete'
    autocompleteOptions?: AutocompleteOption[]
    onAutocompleteSearch?: (query: string) => void
    onAutocompleteCreate?: (rowIndex: number, columnId: string, label: string) => void
    /** For autocomplete: a function to get display text from the raw cell value */
    getDisplayValue?: (value: TValue) => string
    /** For autocomplete: a function to get a pending suggestion for this row */
    getSuggestedValue?: (rowIndex: number) => { name: string; type: string } | null
  }
}

interface EditableTableProps<TData> {
  columns: ColumnDef<TData, unknown>[]
  data: TData[]
  onCellUpdate: (rowIndex: number, columnId: string, value: string) => void
  getRowClassName?: (row: TData, index: number) => string
  virtualizeRows?: boolean
}

function EditableTable<TData>({
  columns,
  data,
  onCellUpdate,
  getRowClassName,
  virtualizeRows = true,
}: EditableTableProps<TData>) {
  const [sorting, setSorting] = useState<SortingState>([])
  const tableContainerRef = useRef<HTMLDivElement>(null)

  const table = useReactTable({
    data,
    columns,
    state: { sorting },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  })

  const { rows } = table.getRowModel()

  // Build set of editable column indices
  const editableColumns = useMemo(() => {
    const set = new Set<number>()
    table.getAllLeafColumns().forEach((col, idx) => {
      if (col.columnDef.meta?.editable) {
        set.add(idx)
      }
    })
    return set
  }, [table])

  const leafColumns = table.getAllLeafColumns()

  const {
    activeCell,
    setActiveCell,
    handleKeyDown: navHandleKeyDown,
    isEditing,
    setIsEditing,
  } = useTableNavigation({
    rowCount: rows.length,
    columnCount: leafColumns.length,
    editableColumns,
  })

  const handleCellClick = useCallback(
    (rowIndex: number, colIndex: number) => {
      if (editableColumns.has(colIndex)) {
        setActiveCell([rowIndex, colIndex])
        setIsEditing(true)
      }
    },
    [editableColumns, setActiveCell, setIsEditing],
  )

  const handleStartEdit = useCallback(() => {
    setIsEditing(true)
  }, [setIsEditing])

  const handleCancelEdit = useCallback(() => {
    setIsEditing(false)
  }, [setIsEditing])

  const handleTableKeyDown = useCallback(
    (e: KeyboardEvent) => {
      navHandleKeyDown(e)
    },
    [navHandleKeyDown],
  )

  // Virtualization
  const rowVirtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => tableContainerRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 10,
    enabled: virtualizeRows,
  })

  const virtualRows = virtualizeRows ? rowVirtualizer.getVirtualItems() : null
  const totalSize = virtualizeRows ? rowVirtualizer.getTotalSize() : 0

  const renderRow = useCallback(
    (rowIndex: number, row: (typeof rows)[number]) => {
      const rowData = row.original
      const rowClassName = getRowClassName ? getRowClassName(rowData, rowIndex) : ''
      return row.getVisibleCells().map((cell, colIndex) => {
        const meta = cell.column.columnDef.meta
        const isActive = activeCell !== null && activeCell[0] === rowIndex && activeCell[1] === colIndex
        const cellEditing = isActive && isEditing
        const cellValue = String(cell.getValue() ?? '')

        if (meta?.editable && meta.cellType === 'autocomplete') {
          const displayValue = meta.getDisplayValue
            ? meta.getDisplayValue(cell.getValue())
            : cellValue
          const suggestedValue = meta.getSuggestedValue
            ? meta.getSuggestedValue(rowIndex)
            : null
          return (
            <td
              key={cell.id}
              className={['h-[36px] p-0 border-b border-neutral-200', rowClassName].join(' ')}
            >
              <AutocompleteCell
                value={cellValue}
                displayValue={displayValue}
                suggestedValue={suggestedValue}
                rowIndex={rowIndex}
                columnId={cell.column.id}
                isActive={isActive}
                isEditing={cellEditing}
                options={meta.autocompleteOptions ?? []}
                onSearch={meta.onAutocompleteSearch ?? (() => {})}
                onChange={onCellUpdate}
                onCreate={meta.onAutocompleteCreate}
                onStartEdit={handleStartEdit}
                onKeyDown={handleTableKeyDown}
                onCancelEdit={handleCancelEdit}
              />
            </td>
          )
        }

        if (meta?.editable) {
          return (
            <td
              key={cell.id}
              className={['h-[36px] p-0 border-b border-neutral-200', rowClassName].join(' ')}
            >
              <EditableCell
                value={cellValue}
                rowIndex={rowIndex}
                columnId={cell.column.id}
                cellType={(meta.cellType === 'number' ? 'number' : 'text')}
                isActive={isActive}
                isEditing={cellEditing}
                onStartEdit={handleStartEdit}
                onCellUpdate={onCellUpdate}
                onKeyDown={handleTableKeyDown}
                onCancelEdit={handleCancelEdit}
              />
            </td>
          )
        }

        // Non-editable cell
        return (
          <td
            key={cell.id}
            onClick={() => handleCellClick(rowIndex, colIndex)}
            className={[
              'h-[36px] px-2 py-1 text-caption font-body text-neutral-900 border-b border-neutral-200 truncate',
              rowClassName,
            ].join(' ')}
          >
            {flexRender(cell.column.columnDef.cell, cell.getContext())}
          </td>
        )
      })
    },
    [
      activeCell,
      isEditing,
      getRowClassName,
      onCellUpdate,
      handleStartEdit,
      handleCancelEdit,
      handleTableKeyDown,
      handleCellClick,
    ],
  )

  return (
    <div
      ref={tableContainerRef}
      className="overflow-auto border border-neutral-200 rounded-lg"
      style={virtualizeRows ? { maxHeight: '80vh' } : undefined}
    >
      <table className="w-full border-collapse table-fixed">
        <thead className="sticky top-0 z-10 bg-white">
          {table.getHeaderGroups().map((headerGroup) => (
            <tr key={headerGroup.id}>
              {headerGroup.headers.map((header) => (
                <th
                  key={header.id}
                  onClick={header.column.getToggleSortingHandler()}
                  className={[
                    'h-[36px] px-2 py-1 text-caption font-semibold text-neutral-600 text-left border-b border-neutral-200 bg-neutral-50 select-none',
                    header.column.getCanSort() ? 'cursor-pointer hover:text-neutral-900' : '',
                  ].join(' ')}
                  style={{ width: header.getSize() }}
                >
                  <div className="flex items-center gap-1">
                    {header.isPlaceholder
                      ? null
                      : flexRender(header.column.columnDef.header, header.getContext())}
                    {{
                      asc: ' \u2191',
                      desc: ' \u2193',
                    }[header.column.getIsSorted() as string] ?? null}
                  </div>
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {virtualizeRows && virtualRows ? (
            <>
              {virtualRows.length > 0 && (
                <tr style={{ height: `${virtualRows[0]!.start}px` }}>
                  <td colSpan={leafColumns.length} />
                </tr>
              )}
              {virtualRows.map((virtualRow) => {
                const row = rows[virtualRow.index]!
                return (
                  <tr
                    key={row.id}
                    className="hover:bg-neutral-50 transition-colors"
                    style={{ height: ROW_HEIGHT }}
                  >
                    {renderRow(virtualRow.index, row)}
                  </tr>
                )
              })}
              {virtualRows.length > 0 && (
                <tr
                  style={{
                    height: `${totalSize - (virtualRows[virtualRows.length - 1]!.end)}px`,
                  }}
                >
                  <td colSpan={leafColumns.length} />
                </tr>
              )}
            </>
          ) : (
            rows.map((row, rowIndex) => (
              <tr
                key={row.id}
                className="hover:bg-neutral-50 transition-colors"
                style={{ height: ROW_HEIGHT }}
              >
                {renderRow(rowIndex, row)}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  )
}

EditableTable.displayName = 'EditableTable'

export { EditableTable, ROW_HEIGHT }
export type { EditableTableProps }
export type { AutocompleteOption } from './AutocompleteCell'
