import { useEffect, useRef, useState, useCallback } from 'react'
import {
  acquireListLock,
  heartbeatListLock,
  takeOverListLock,
  type LockHolder,
} from '@/api/lists'
import { ApiClientError, getToken } from '@/api/client'
import { releaseListLock } from '@/api/lists'
import {
  on as onLockEvent,
  off as offLockEvent,
  type LockAcquiredPayload,
  type LockReleasedPayload,
  type LockTakenOverPayload,
} from '@/api/lockEvents'

export interface LockState {
  holder: LockHolder | null
  isHeldByMe: boolean
}

export interface UseListLockResult {
  state: LockState
  takeOver: () => Promise<void>
}

// Heartbeat every 30s; backend TTL defaults to 60s.
const HEARTBEAT_MS = 30_000

// Best-effort release on tab close. `navigator.sendBeacon` is the canonical
// fire-and-forget primitive but cannot set an Authorization header — and the
// release endpoint sits behind the JWT middleware. fetch({ keepalive: true })
// is the modern equivalent that DOES preserve headers across unload, so we
// use that when available and fall back to a plain fetch (which the browser
// may cancel if the tab dies too fast).
function bestEffortRelease(listId: string): void {
  const token = getToken()
  const url = `${window.location.origin}/api/v1/lists/${encodeURIComponent(
    listId,
  )}/lock/release`
  try {
    void fetch(url, {
      method: 'POST',
      headers: token ? { Authorization: `Bearer ${token}` } : {},
      keepalive: true,
    })
  } catch {
    // Nothing more we can do — server TTL (60s default) cleans up.
  }
}

// useListLock acquires an edit lock on mount, heartbeats on a timer, releases
// on unmount, and keeps state in sync with WS events from other clients.
//
// NOT cached in TanStack Query — lock state changes too fast and is transient
// to the page (multi-store-implementation-plan.md §3 "TanStack Query" note).
export function useListLock(listId: string, currentUserId: string): UseListLockResult {
  const [state, setState] = useState<LockState>({ holder: null, isHeldByMe: false })
  // Refs so the unmount handler always sees the latest owner without
  // re-wiring the cleanup effect on each state change.
  const isHeldByMeRef = useRef(false)
  const listIdRef = useRef(listId)
  listIdRef.current = listId

  const applyHolder = useCallback(
    (holder: LockHolder | null, isMine?: boolean) => {
      // `isMine` override: when we know from an HTTP response (200 from
      // acquire / takeover) that we hold the lock, use that directly rather
      // than comparing user_id. The caller may not yet have currentUserId
      // hydrated from /profile, which would otherwise render the
      // "someone else is editing" banner for the user's own lock.
      const isHeldByMe = isMine ?? holder?.user_id === currentUserId
      isHeldByMeRef.current = isHeldByMe
      setState({ holder, isHeldByMe })
    },
    [currentUserId],
  )

  // Mount: acquire. Unmount: release. Heartbeat while mounted and owner.
  useEffect(() => {
    if (!listId) return
    let cancelled = false
    let heartbeat: ReturnType<typeof setInterval> | null = null

    acquireListLock(listId).then(
      (resp) => {
        if (cancelled) return
        // 200 from acquire means the server granted (or re-affirmed) the
        // lock to us. Treat that as authoritative: we ARE the holder,
        // regardless of whether currentUserId has hydrated yet.
        applyHolder(resp.holder, true)
        heartbeat = setInterval(() => {
          heartbeatListLock(listId).catch((err) => {
            if (err instanceof ApiClientError && err.status === 409) {
              // Lost the lock — stop heartbeating; the WS event will
              // surface the new holder for the banner.
              if (heartbeat) {
                clearInterval(heartbeat)
                heartbeat = null
              }
              isHeldByMeRef.current = false
              setState((prev) => ({ ...prev, isHeldByMe: false }))
            }
          })
        }, HEARTBEAT_MS)
      },
      (err) => {
        if (cancelled) return
        if (err instanceof ApiClientError && err.status === 409) {
          // Conflict: another user holds. Payload carries the holder.
          // Our fetch client throws ApiClientError without the body — so we
          // wait for the WS event instead. Also preemptively set isHeldByMe
          // to false.
          isHeldByMeRef.current = false
          setState((prev) => ({ ...prev, isHeldByMe: false }))
        }
      },
    )

    const beforeUnload = () => {
      if (isHeldByMeRef.current) {
        bestEffortRelease(listIdRef.current)
      }
    }
    window.addEventListener('beforeunload', beforeUnload)

    return () => {
      cancelled = true
      if (heartbeat) clearInterval(heartbeat)
      window.removeEventListener('beforeunload', beforeUnload)
      if (isHeldByMeRef.current) {
        // Normal SPA unmount (navigating away) — full request, get a proper
        // response. Uses keepalive fallback path indirectly via fetch.
        void releaseListLock(listIdRef.current).catch(() => {
          // best effort
        })
      }
    }
    // currentUserId is captured; applyHolder depends on it. listId changes
    // remount the hook which is the intended semantics.
  }, [listId, currentUserId, applyHolder])

  // WS event subscriptions — update state when other clients act on the lock.
  useEffect(() => {
    if (!listId) return

    const onAcquired = (p: LockAcquiredPayload) => {
      if (p.list_id !== listId) return
      applyHolder({ user_id: p.user_id, user_name: p.user_name })
    }
    const onReleased = (p: LockReleasedPayload) => {
      if (p.list_id !== listId) return
      applyHolder(null)
    }
    const onTakenOver = (p: LockTakenOverPayload) => {
      if (p.list_id !== listId) return
      applyHolder({ user_id: p.new_user_id, user_name: p.new_user_name })
    }
    onLockEvent('list.lock.acquired', onAcquired)
    onLockEvent('list.lock.released', onReleased)
    onLockEvent('list.lock.taken_over', onTakenOver)
    return () => {
      offLockEvent('list.lock.acquired', onAcquired)
      offLockEvent('list.lock.released', onReleased)
      offLockEvent('list.lock.taken_over', onTakenOver)
    }
  }, [listId, applyHolder])

  const takeOver = useCallback(async () => {
    if (!listId) return
    const resp = await takeOverListLock(listId)
    // Like acquire: 200 from takeover means we own the lock now.
    applyHolder(resp.holder, true)
  }, [listId, applyHolder])

  return { state, takeOver }
}
