import ReconnectingWebSocket from 'reconnecting-websocket'
import type { QueryClient } from '@tanstack/react-query'
import { emit as emitLockEvent } from './lockEvents'
import type {
  LockAcquiredPayload,
  LockReleasedPayload,
  LockTakenOverPayload,
} from './lockEvents'
import type { WSMessage } from '@/types'

let socket: ReconnectingWebSocket | null = null

function getWsUrl(): string {
  // Cookie auth: browsers attach same-origin cookies to WebSocket upgrade
  // requests automatically (subject to SameSite rules, which our Strict
  // cookie satisfies for same-origin connects). No token in the URL.
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}/api/v1/ws`
}

export function connectWebSocket(queryClient: QueryClient): ReconnectingWebSocket {
  if (socket) {
    return socket
  }

  socket = new ReconnectingWebSocket(getWsUrl, undefined, {
    maxReconnectionDelay: 10000,
    minReconnectionDelay: 1000,
    reconnectionDelayGrowFactor: 1.3,
    maxRetries: Infinity,
  })

  socket.addEventListener('message', (event: MessageEvent) => {
    let message: WSMessage
    try {
      message = JSON.parse(event.data as string) as WSMessage
    } catch {
      return
    }

    // Invalidate relevant React Query caches based on message type
    switch (message.type) {
      case 'receipt.complete':
        void queryClient.invalidateQueries({ queryKey: ['receipts'] })
        break
      case 'list.updated':
      case 'list.item.updated': {
        // Also invalidate the single-list detail cache so the currently-open
        // ShoppingListPage refetches. Without this, item-level updates from
        // other clients only refresh the lists index, not the active detail.
        const payload = (message.payload ?? {}) as { list_id?: string }
        if (payload.list_id) {
          void queryClient.invalidateQueries({ queryKey: ['shopping-list', payload.list_id] })
        }
        void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
        break
      }
      case 'list.items.bulk_updated':
      case 'list.items.bulk_removed': {
        const payload = (message.payload ?? {}) as { list_id?: string }
        if (payload.list_id) {
          void queryClient.invalidateQueries({ queryKey: ['shopping-list', payload.list_id] })
        }
        void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
        break
      }
      case 'list.lock.acquired':
        emitLockEvent('list.lock.acquired', message.payload as LockAcquiredPayload)
        break
      case 'list.lock.released':
        emitLockEvent('list.lock.released', message.payload as LockReleasedPayload)
        break
      case 'list.lock.taken_over':
        emitLockEvent('list.lock.taken_over', message.payload as LockTakenOverPayload)
        break
      case 'product.updated':
        void queryClient.invalidateQueries({ queryKey: ['products'] })
        break
      case 'store.updated':
        void queryClient.invalidateQueries({ queryKey: ['stores'] })
        break
      default:
        // Unknown message type — no-op
        break
    }
  })

  return socket
}

export function disconnectWebSocket(): void {
  if (socket) {
    socket.close()
    socket = null
  }
}

export function getWebSocket(): ReconnectingWebSocket | null {
  return socket
}
