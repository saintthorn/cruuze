/**
 * Registers the service worker that keeps the app's files available
 * offline, and tells it which files the current build has. Production only:
 * in development the files change under it.
 */
export function registerServiceWorker() {
  if (!import.meta.env.PROD || !('serviceWorker' in navigator)) return
  window.addEventListener('load', async () => {
    try {
      await navigator.serviceWorker.register('/sw.js')
      const registration = await navigator.serviceWorker.ready
      const manifest = (await (await fetch('/asset-manifest.json', { cache: 'no-store' })).json()) as { files: string[] }
      registration.active?.postMessage({ type: 'precache', files: manifest.files })
    } catch {
      // Offline, or service workers are blocked: the app works without one.
    }
  })
}
