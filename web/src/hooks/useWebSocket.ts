import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { getWebSocket } from '@/api/ws'
import type { WSMessage } from '@/types'

interface UseWebSocketOptions {
  /** The list ID to subscribe to events for */
  listId: string
  /** Callback when another user makes a change (for toast/indicator) */
  onRemoteChange?: (eventType: string) => void
}

/**
 * Hook that subscribes to WebSocket events for a specific shopping list.
 * Invalidates relevant queries when events arrive so the UI stays in sync.
 */
export function useListWebSocket({ listId, onRemoteChange }: UseWebSocketOptions) {
  const queryClient = useQueryClient()
  const onRemoteChangeRef = useRef(onRemoteChange)
  onRemoteChangeRef.current = onRemoteChange

  useEffect(() => {
    const socket = getWebSocket()
    if (!socket) return

    function handleMessage(event: MessageEvent) {
      let message: WSMessage
      try {
        message = JSON.parse(event.data as string) as WSMessage
      } catch {
        return
      }

      const payload = message.payload as Record<string, unknown> | undefined
      const eventListId = payload?.list_id as string | undefined

      // Only react to events for our list
      if (eventListId !== listId) return

      const listEvents = [
        'list.item.checked',
        'list.item.added',
        'list.item.removed',
        'list.item.updated',
      ]

      if (listEvents.includes(message.type)) {
        // Invalidate the detail query for this specific list
        void queryClient.invalidateQueries({ queryKey: ['shopping-list', listId] })
        // Also invalidate the lists index (counts may have changed)
        void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })

        onRemoteChangeRef.current?.(message.type)
      }
    }

    socket.addEventListener('message', handleMessage)
    return () => {
      socket.removeEventListener('message', handleMessage)
    }
  }, [listId, queryClient])
}
