import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { listLists, createList, deleteList } from '@/api/lists'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { Modal } from '@/components/ui/Modal'
import { Input } from '@/components/ui/Input'
import type { ShoppingListWithCounts } from '@/types'

function ListsIndexPage() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [newListName, setNewListName] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<ShoppingListWithCounts | null>(null)

  const listsQuery = useQuery({
    queryKey: ['shopping-lists'],
    queryFn: listLists,
  })

  const createMutation = useMutation({
    mutationFn: createList,
    onSuccess: (newList) => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
      setShowCreate(false)
      setNewListName('')
      navigate(`/lists/${newList.id}`)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: deleteList,
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
      setDeleteTarget(null)
    },
  })

  function handleCreate() {
    const name = newListName.trim()
    if (!name) return
    createMutation.mutate({ name })
  }

  const lists = listsQuery.data ?? []
  const activeLists = lists.filter((l) => l.status === 'active')
  const completedLists = lists.filter((l) => l.status === 'completed')
  const archivedLists = lists.filter((l) => l.status === 'archived')

  function statusBadge(status: string) {
    switch (status) {
      case 'active':
        return <Badge variant="success">Active</Badge>
      case 'completed':
        return <Badge variant="neutral">Completed</Badge>
      case 'archived':
        return <Badge variant="neutral">Archived</Badge>
      default:
        return null
    }
  }

  function renderListCard(list: ShoppingListWithCounts) {
    return (
      <div
        key={list.id}
        className="flex items-center gap-3 px-4 py-3 bg-white border border-neutral-200 rounded-xl cursor-pointer hover:border-brand transition-colors"
        onClick={() => navigate(`/lists/${list.id}`)}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            navigate(`/lists/${list.id}`)
          }
        }}
      >
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <p className="text-body-medium font-medium text-neutral-900 truncate">
              {list.name}
            </p>
            {statusBadge(list.status)}
          </div>
          <p className="text-small text-neutral-400 mt-0.5">
            {list.checked_count}/{list.item_count} items checked
          </p>
        </div>
        <button
          type="button"
          className="p-2 text-neutral-400 hover:text-expensive rounded-lg hover:bg-neutral-50 transition-colors"
          onClick={(e) => {
            e.stopPropagation()
            setDeleteTarget(list)
          }}
          aria-label={`Delete ${list.name}`}
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
          </svg>
        </button>
      </div>
    )
  }

  function renderSection(title: string, items: ShoppingListWithCounts[]) {
    if (items.length === 0) return null
    return (
      <div>
        <h2 className="text-small font-semibold text-neutral-400 uppercase tracking-wide mb-2">
          {title}
        </h2>
        <div className="flex flex-col gap-2">{items.map(renderListCard)}</div>
      </div>
    )
  }

  return (
    <div className="max-w-2xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <h1 className="font-display text-subhead font-bold text-neutral-900">Shopping Lists</h1>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          + New List
        </Button>
      </div>

      {listsQuery.isLoading ? (
        <p className="text-body text-neutral-400">Loading lists...</p>
      ) : listsQuery.isError ? (
        <p className="text-body text-expensive">Failed to load lists.</p>
      ) : lists.length === 0 ? (
        <div className="text-center py-12">
          <p className="text-body text-neutral-400 mb-4">No shopping lists yet.</p>
          <Button onClick={() => setShowCreate(true)}>Create your first list</Button>
        </div>
      ) : (
        <div className="flex flex-col gap-6">
          {renderSection('Active', activeLists)}
          {renderSection('Completed', completedLists)}
          {renderSection('Archived', archivedLists)}
        </div>
      )}

      {/* Create List Modal */}
      <Modal
        open={showCreate}
        onClose={() => {
          setShowCreate(false)
          setNewListName('')
        }}
        title="New Shopping List"
        footer={
          <>
            <Button
              variant="secondary"
              onClick={() => {
                setShowCreate(false)
                setNewListName('')
              }}
            >
              Cancel
            </Button>
            <Button
              onClick={handleCreate}
              disabled={!newListName.trim() || createMutation.isPending}
            >
              {createMutation.isPending ? 'Creating...' : 'Create'}
            </Button>
          </>
        }
      >
        <Input
          label="List name"
          placeholder="e.g. Weekly Groceries"
          value={newListName}
          onChange={(e) => setNewListName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') handleCreate()
          }}
          autoFocus
        />
      </Modal>

      {/* Delete Confirmation Modal */}
      <Modal
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        title="Delete List"
        footer={
          <>
            <Button variant="secondary" onClick={() => setDeleteTarget(null)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              className="!bg-expensive hover:!bg-expensive/80"
              onClick={() => {
                if (deleteTarget) deleteMutation.mutate(deleteTarget.id)
              }}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-900">
          Are you sure you want to delete &quot;{deleteTarget?.name}&quot;? This will remove all
          items in the list. This action cannot be undone.
        </p>
      </Modal>
    </div>
  )
}

export default ListsIndexPage
