import ReconnectingWebSocket from 'reconnecting-websocket'
import type { QueryClient } from '@tanstack/react-query'
import { getToken } from './client'
import type { WSMessage } from '@/types'

let socket: ReconnectingWebSocket | null = null

function getWsUrl(): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const token = getToken()
  return `${protocol}//${window.location.host}/api/v1/ws?token=${encodeURIComponent(token ?? '')}`
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
      case 'receipt.processed':
        void queryClient.invalidateQueries({ queryKey: ['receipts'] })
        break
      case 'list.updated':
      case 'list.item.updated':
        void queryClient.invalidateQueries({ queryKey: ['shopping-lists'] })
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
