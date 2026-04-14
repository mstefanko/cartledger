// CartLedger Service Worker — network-first with offline fallback
// mnz38gmj is replaced at build time by vite to bust the cache
const CACHE_NAME = 'cartledger-mnz38gmj'

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll(['/']))
  )
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  // Delete all caches from previous builds
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k)))
    )
  )
  self.clients.claim()
})

self.addEventListener('fetch', (event) => {
  const { request } = event

  // Never intercept non-GET or API calls
  if (request.method !== 'GET' || request.url.includes('/api/')) return

  // SPA navigation: network first, fall back to cached shell
  if (request.mode === 'navigate') {
    event.respondWith(
      fetch(request)
        .then((response) => {
          // Cache the latest shell
          const clone = response.clone()
          caches.open(CACHE_NAME).then((cache) => cache.put('/', clone))
          return response
        })
        .catch(() =>
          caches.match('/').then((cached) => {
            // Return cached shell or a minimal offline page
            if (cached) return cached
            return new Response('Offline — please check your connection and reload.', {
              status: 503,
              headers: { 'Content-Type': 'text/html' },
            })
          })
        )
    )
    return
  }

  // Static assets: network first, cache fallback
  event.respondWith(
    fetch(request)
      .then((response) => {
        const clone = response.clone()
        caches.open(CACHE_NAME).then((cache) => cache.put(request, clone))
        return response
      })
      .catch(() =>
        caches.match(request).then((cached) => {
          // Return cached asset or a 503 — never re-fetch after failure
          if (cached) return cached
          return new Response('', { status: 503 })
        })
      )
  )
})
