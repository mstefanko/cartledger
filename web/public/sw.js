// CartLedger Service Worker — network-first with offline fallback
// __BUILD_VERSION__ is replaced at build time by vite to bust the cache
const CACHE_NAME = 'cartledger-__BUILD_VERSION__'

// In dev, Vite's swVersionPlugin only runs on prod `writeBundle`, so the
// placeholder is never replaced and CACHE_NAME literally contains the raw
// token. We use that as a signal that this SW is running under `vite dev`
// and must self-destruct — otherwise a stale SW from a prior session keeps
// serving a cached bundle, intercepting fetches, and reload-looping the page.
const IS_DEV = CACHE_NAME.includes('__BUILD_VERSION__')

self.addEventListener('install', (event) => {
  if (IS_DEV) {
    // Clear every cache this SW (or an older version) ever created, then
    // activate immediately so the activate handler can unregister.
    event.waitUntil(
      caches
        .keys()
        .then((keys) => Promise.all(keys.map((k) => caches.delete(k))))
        .then(() => self.skipWaiting()),
    )
    return
  }

  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll(['/'])),
  )
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  if (IS_DEV) {
    // Unregister this SW, then force every controlled client to navigate
    // to its current URL — that re-fetches straight from the Vite dev
    // server with no SW in the way, breaking the reload loop.
    event.waitUntil(
      self.registration
        .unregister()
        .then(() => self.clients.matchAll({ includeUncontrolled: true }))
        .then((clients) => {
          clients.forEach((client) => {
            if ('navigate' in client) {
              client.navigate(client.url).catch(() => {})
            }
          })
        }),
    )
    return
  }

  // Delete all caches from previous builds
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))),
    ),
  )
  self.clients.claim()
})

self.addEventListener('fetch', (event) => {
  // In dev, do nothing — let the browser hit the Vite dev server directly.
  // Any respondWith() here would mask dev-only HMR/module requests and keep
  // the loop alive until the SW finishes unregistering.
  if (IS_DEV) return

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
          }),
        ),
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
        }),
      ),
  )
})
