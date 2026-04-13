import { useCallback } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Modal } from '@/components/ui/Modal'
import { Button } from '@/components/ui/Button'
import { createRule } from '@/api/matching'

interface CreateRuleModalProps {
  open: boolean
  onClose: () => void
  rawName: string
  productName: string
  productId: string
  storeId: string | null
}

function CreateRuleModal({
  open,
  onClose,
  rawName,
  productName,
  productId,
  storeId,
}: CreateRuleModalProps) {
  const createRuleMutation = useMutation({
    mutationFn: () =>
      createRule({
        condition_op: 'exact',
        condition_val: rawName,
        product_id: productId,
        store_id: storeId ?? undefined,
      }),
    onSuccess: () => {
      onClose()
    },
  })

  const handleJustThisTime = useCallback(() => {
    onClose()
  }, [onClose])

  const handleCreateRule = useCallback(() => {
    createRuleMutation.mutate()
  }, [createRuleMutation])

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Create Matching Rule"
      footer={
        <>
          <Button
            variant="secondary"
            size="sm"
            onClick={handleJustThisTime}
            disabled={createRuleMutation.isPending}
          >
            Just this time
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={handleCreateRule}
            disabled={createRuleMutation.isPending}
          >
            {createRuleMutation.isPending ? 'Creating...' : 'Create Rule'}
          </Button>
        </>
      }
    >
      <p className="text-body text-neutral-900">
        Always match{' '}
        <span className="font-semibold">&ldquo;{rawName}&rdquo;</span> to{' '}
        <span className="font-semibold text-brand">{productName}</span>?
      </p>
      <p className="mt-2 text-caption text-neutral-400">
        Creating a rule will automatically match this receipt text to the
        selected product on future receipts.
      </p>
      {createRuleMutation.isError && (
        <p className="mt-3 text-caption text-expensive">
          Failed to create rule. Please try again.
        </p>
      )}
    </Modal>
  )
}

CreateRuleModal.displayName = 'CreateRuleModal'

export { CreateRuleModal }
export type { CreateRuleModalProps }
