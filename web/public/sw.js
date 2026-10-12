// Makes the app start instantly and without a connection: the app's own
// files are kept in a cache. Data is not handled here at all: it lives in
// IndexedDB and syncs over the API, which this worker never touches.
const CACHE = 'cruuze-shell-v1'

self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

// The page sends the list of files the current build consists of. Fetch the
// ones we lack, and drop what an older build left behind.
self.addEventListener('message', (event) => {
  if (event.data?.type !== 'precache' || !Array.isArray(event.data.files)) return
  event.waitUntil(
    caches.open(CACHE).then(async (cache) => {
      const wanted = new Set(event.data.files.map((file) => new URL(file, self.location.origin).href))
      for (const request of await cache.keys()) {
        if (!wanted.has(request.url) && new URL(request.url).pathname !== '/') await cache.delete(request)
      }
      for (const url of wanted) {
        if (!(await cache.match(url))) await cache.add(url).catch(() => {})
      }
    }),
  )
})

self.addEventListener('fetch', (event) => {
  const { request } = event
  if (request.method !== 'GET') return
  const url = new URL(request.url)
  if (url.origin !== self.location.origin || url.pathname.startsWith('/api/')) return

  if (request.mode === 'navigate') {
    // Every page is the same document. Prefer a fresh copy, so a new
    // release shows up on the next load; fall back to the cached one.
    event.respondWith(
      fetch(request)
        .then((response) => {
          if (response.ok) {
            const copy = response.clone()
            void caches.open(CACHE).then((cache) => cache.put('/', copy))
          }
          return response
        })
        .catch(async () => (await caches.match('/')) ?? Response.error()),
    )
    return
  }

  if (url.pathname.startsWith('/assets/')) {
    // Fingerprinted and never changing: the cached copy is always right.
    event.respondWith(
      caches.match(request).then(
        (cached) =>
          cached ??
          fetch(request).then((response) => {
            if (response.ok) {
              const copy = response.clone()
              void caches.open(CACHE).then((cache) => cache.put(request, copy))
            }
            return response
          }),
      ),
    )
  }
})
