// Module-level event bus for list-lock WebSocket events.
//
// ws.ts can't call the useListLock hook directly (it's not React), and more
// than one component may need to react to the same event (banner, toast,
// future presence indicators). A tiny typed pub/sub keeps the wiring flat.

export type LockEventType =
  | 'list.lock.acquired'
  | 'list.lock.released'
  | 'list.lock.taken_over'

export interface LockAcquiredPayload {
  list_id: string
  user_id: string
  user_name: string
}

export interface LockReleasedPayload {
  list_id: string
  user_id: string
}

export interface LockTakenOverPayload {
  list_id: string
  new_user_id: string
  new_user_name: string
  prior_user_id?: string
}

export type LockPayloadByType = {
  'list.lock.acquired': LockAcquiredPayload
  'list.lock.released': LockReleasedPayload
  'list.lock.taken_over': LockTakenOverPayload
}

type Listener<T extends LockEventType> = (payload: LockPayloadByType[T]) => void

const listeners: { [K in LockEventType]: Set<Listener<K>> } = {
  'list.lock.acquired': new Set(),
  'list.lock.released': new Set(),
  'list.lock.taken_over': new Set(),
}

export function on<T extends LockEventType>(type: T, fn: Listener<T>): void {
  ;(listeners[type] as Set<Listener<T>>).add(fn)
}

export function off<T extends LockEventType>(type: T, fn: Listener<T>): void {
  ;(listeners[type] as Set<Listener<T>>).delete(fn)
}

export function emit<T extends LockEventType>(
  type: T,
  payload: LockPayloadByType[T],
): void {
  for (const fn of listeners[type] as Set<Listener<T>>) {
    try {
      fn(payload)
    } catch {
      // swallow — one broken listener must not block the rest
    }
  }
}
