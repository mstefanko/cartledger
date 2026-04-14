import { useState, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listRules, createRule, updateRule, deleteRule } from '@/api/matching'
import { listProducts } from '@/api/products'
import { listStores } from '@/api/stores'
import { Button } from '@/components/ui/Button'
import { Badge } from '@/components/ui/Badge'
import { Modal } from '@/components/ui/Modal'
import { RuleFormModal } from '@/components/matching/RuleFormModal'
import type { MatchingRule, CreateRuleRequest, UpdateRuleRequest } from '@/types'

function conditionLabel(op: string, val: string): string {
  switch (op) {
    case 'exact':
      return `exact '${val}'`
    case 'contains':
      return `contains '${val}'`
    case 'starts_with':
      return `starts with '${val}'`
    case 'matches':
      return `matches '${val}'`
    default:
      return `${op} '${val}'`
  }
}

function RulesPage() {
  const queryClient = useQueryClient()
  const [formOpen, setFormOpen] = useState(false)
  const [editingRule, setEditingRule] = useState<MatchingRule | null>(null)
  const [deleteConfirm, setDeleteConfirm] = useState<MatchingRule | null>(null)

  const { data: rules = [], isLoading } = useQuery({
    queryKey: ['rules'],
    queryFn: listRules,
  })

  const { data: products = [] } = useQuery({
    queryKey: ['products'],
    queryFn: () => listProducts(),
  })

  const { data: stores = [] } = useQuery({
    queryKey: ['stores'],
    queryFn: listStores,
  })

  const createMutation = useMutation({
    mutationFn: (data: CreateRuleRequest) => createRule(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['rules'] })
      setFormOpen(false)
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: UpdateRuleRequest }) => updateRule(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['rules'] })
      setEditingRule(null)
      setFormOpen(false)
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteRule(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['rules'] })
      setDeleteConfirm(null)
    },
  })

  // Priority up/down mutations
  const priorityMutation = useMutation({
    mutationFn: ({ id, priority }: { id: string; priority: number }) =>
      updateRule(id, { priority }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['rules'] })
    },
  })

  const handleOpenCreate = useCallback(() => {
    setEditingRule(null)
    setFormOpen(true)
  }, [])

  const handleOpenEdit = useCallback((rule: MatchingRule) => {
    setEditingRule(rule)
    setFormOpen(true)
  }, [])

  const handleFormSubmit = useCallback(
    (data: CreateRuleRequest | UpdateRuleRequest) => {
      if (editingRule) {
        updateMutation.mutate({ id: editingRule.id, data })
      } else {
        createMutation.mutate(data as CreateRuleRequest)
      }
    },
    [editingRule, createMutation, updateMutation],
  )

  const handlePriorityUp = useCallback(
    (rule: MatchingRule) => {
      if (rule.priority <= 0) return
      priorityMutation.mutate({ id: rule.id, priority: rule.priority - 1 })
    },
    [priorityMutation],
  )

  const handlePriorityDown = useCallback(
    (rule: MatchingRule) => {
      priorityMutation.mutate({ id: rule.id, priority: rule.priority + 1 })
    },
    [priorityMutation],
  )

  const productNameById = (id: string): string => {
    return products.find((p) => p.id === id)?.name ?? 'Unknown'
  }

  const storeNameById = (id: string | null): string | null => {
    if (!id) return null
    return stores.find((s) => s.id === id)?.name ?? null
  }

  const sortedRules = [...rules].sort((a, b) => a.priority - b.priority)

  return (
    <div className="py-8">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="font-display text-subhead font-bold text-neutral-900 tracking-tight">
            Auto-Match Rules
          </h1>
          <p className="mt-1 text-caption text-neutral-400">
            Automatically match receipt items to your products. Lower priority numbers are evaluated first.
          </p>
        </div>
        <Button size="sm" onClick={handleOpenCreate}>
          Add Rule
        </Button>
      </div>

      {isLoading ? (
        <p className="text-body text-neutral-400">Loading rules...</p>
      ) : sortedRules.length === 0 ? (
        <div className="text-center py-16">
          <p className="text-body text-neutral-400">No matching rules yet.</p>
          <div className="mt-4">
            <Button onClick={handleOpenCreate}>Create Your First Rule</Button>
          </div>
        </div>
      ) : (
        <div className="overflow-x-auto border border-neutral-200 rounded-lg">
          <table className="w-full border-collapse">
            <thead>
              <tr className="bg-neutral-50">
                <th className="px-3 py-2.5 text-left text-caption font-semibold text-neutral-600 w-24">
                  Priority
                </th>
                <th className="px-3 py-2.5 text-left text-caption font-semibold text-neutral-600">
                  Condition
                </th>
                <th className="px-3 py-2.5 text-left text-caption font-semibold text-neutral-600 w-36">
                  Store
                </th>
                <th className="px-3 py-2.5 text-left text-caption font-semibold text-neutral-600">
                  Maps to Product
                </th>
                <th className="px-3 py-2.5 text-left text-caption font-semibold text-neutral-600 w-36">
                  Category
                </th>
                <th className="px-3 py-2.5 text-right text-caption font-semibold text-neutral-600 w-40">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody>
              {sortedRules.map((rule) => {
                const storeName = storeNameById(rule.store_id)
                return (
                  <tr
                    key={rule.id}
                    className="border-t border-neutral-200 hover:bg-neutral-50 transition-colors"
                  >
                    <td className="px-3 py-2.5">
                      <div className="flex items-center gap-1">
                        <span className="text-body-medium font-medium text-neutral-900 w-6 text-center">
                          {rule.priority}
                        </span>
                        <div className="flex flex-col">
                          <button
                            type="button"
                            onClick={() => handlePriorityUp(rule)}
                            disabled={rule.priority <= 0 || priorityMutation.isPending}
                            className="text-neutral-400 hover:text-neutral-900 disabled:opacity-30 cursor-pointer disabled:cursor-not-allowed"
                            aria-label="Increase priority"
                          >
                            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                              <path strokeLinecap="round" strokeLinejoin="round" d="M5 15l7-7 7 7" />
                            </svg>
                          </button>
                          <button
                            type="button"
                            onClick={() => handlePriorityDown(rule)}
                            disabled={priorityMutation.isPending}
                            className="text-neutral-400 hover:text-neutral-900 disabled:opacity-30 cursor-pointer disabled:cursor-not-allowed"
                            aria-label="Decrease priority"
                          >
                            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                              <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
                            </svg>
                          </button>
                        </div>
                      </div>
                    </td>
                    <td className="px-3 py-2.5">
                      <code className="text-caption bg-neutral-50 px-2 py-0.5 rounded text-neutral-700">
                        {conditionLabel(rule.condition_op, rule.condition_val)}
                      </code>
                    </td>
                    <td className="px-3 py-2.5 text-caption text-neutral-600">
                      {storeName ?? <span className="text-neutral-400">Any</span>}
                    </td>
                    <td className="px-3 py-2.5 text-body-medium font-medium text-neutral-900">
                      {productNameById(rule.product_id)}
                    </td>
                    <td className="px-3 py-2.5">
                      {rule.category ? (
                        <Badge variant="neutral">{rule.category}</Badge>
                      ) : (
                        <span className="text-caption text-neutral-400">&mdash;</span>
                      )}
                    </td>
                    <td className="px-3 py-2.5 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <Button
                          size="sm"
                          variant="subtle"
                          onClick={() => handleOpenEdit(rule)}
                        >
                          Edit
                        </Button>
                        <Button
                          size="sm"
                          variant="secondary"
                          className="text-expensive hover:text-expensive"
                          onClick={() => setDeleteConfirm(rule)}
                        >
                          Delete
                        </Button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Rule Form Modal */}
      <RuleFormModal
        open={formOpen}
        onClose={() => {
          setFormOpen(false)
          setEditingRule(null)
        }}
        onSubmit={handleFormSubmit}
        isSubmitting={createMutation.isPending || updateMutation.isPending}
        rule={editingRule}
      />

      {/* Delete Confirmation Modal */}
      <Modal
        open={!!deleteConfirm}
        onClose={() => setDeleteConfirm(null)}
        title="Delete Rule"
        footer={
          <>
            <Button variant="secondary" size="sm" onClick={() => setDeleteConfirm(null)}>
              Cancel
            </Button>
            <Button
              size="sm"
              className="bg-expensive text-white hover:opacity-90"
              onClick={() => deleteConfirm && deleteMutation.mutate(deleteConfirm.id)}
              disabled={deleteMutation.isPending}
            >
              {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
            </Button>
          </>
        }
      >
        <p className="text-body text-neutral-600">
          Delete rule{' '}
          <code className="bg-neutral-50 px-1.5 py-0.5 rounded text-caption">
            {deleteConfirm && conditionLabel(deleteConfirm.condition_op, deleteConfirm.condition_val)}
          </code>
          ? This cannot be undone.
        </p>
      </Modal>
    </div>
  )
}

export default RulesPage
