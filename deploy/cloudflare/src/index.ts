// Cruuze on Cloudflare. This Worker receives every request and passes it,
// WebSockets included, to one container running the Cruuze server.
// See docs/cloudflare.md.
import { Container, getContainer } from '@cloudflare/containers'

// Where the container remembers which settings it was started with.
const STARTED_WITH = 'startedWith'

export class CruuzeServer extends Container<Env> {
  defaultPort = 8080
  // Longer than the gap between two keepAlive calls, so the container never
  // sleeps: the server's background jobs (notification emails, webhooks,
  // cycles, auto-archive) run inside it.
  sleepAfter = '10m'
  envVars = serverEnv(this.env)

  override async onStart() {
    await this.ctx.storage.put(STARTED_WITH, await fingerprint(this.envVars))
  }

  // keepAlive runs every minute. A container reads its environment only when
  // it starts, so one started with other settings than the Worker has now
  // (after `wrangler secret put` or a deploy that changed a variable) is
  // stopped; the next request or keepAlive starts it again with the new ones.
  async keepAlive() {
    if (this.ctx.container?.running) {
      const startedWith = await this.ctx.storage.get<string>(STARTED_WITH)
      if (startedWith !== (await fingerprint(this.envVars))) {
        console.log('settings changed, restarting the server')
        await this.stop()
        return
      }
    }
    // A request starts the container if it is not running (after a crash, a
    // deploy or Cloudflare moving it) and counts as activity.
    const res = await this.containerFetch('http://cruuze/readyz')
    if (!res.ok) {
      console.error('server not ready', res.status, await res.text())
    }
  }
}

export default {
  async fetch(request, env, ctx) {
    // The app's scripts, styles and fonts are the same for everyone and say
    // how long they keep, so they are answered from Cloudflare's cache at the
    // edge; only the first request for each reaches the container.
    const url = new URL(request.url)
    if (request.method === 'GET' && STATIC_FILES.test(url.pathname)) {
      const key = new Request(url.origin + url.pathname)
      const cached = await caches.default.match(key)
      if (cached) {
        return cached
      }
      // Asked for uncompressed, so one copy serves every browser; Cloudflare
      // compresses it on the way out.
      const fresh = await getContainer(env.SERVER).fetch(key)
      if (keepsAtEdge(fresh)) {
        ctx.waitUntil(caches.default.put(key, fresh.clone()))
      }
      return fresh
    }

    // The server takes the visitor's address from these headers
    // (TRUST_PROXY), so only what Cloudflare itself reports may be in them.
    const headers = new Headers(request.headers)
    headers.delete('True-Client-IP')
    headers.delete('X-Forwarded-For')
    headers.delete('X-Real-IP')
    const ip = request.headers.get('CF-Connecting-IP')
    if (ip) {
      headers.set('X-Real-IP', ip)
    }
    // Likewise the request ID in its logs: Cloudflare's, never the visitor's.
    headers.delete('X-Request-Id')
    const ray = request.headers.get('CF-Ray')
    if (ray) {
      headers.set('X-Request-Id', ray)
    }
    return getContainer(env.SERVER).fetch(new Request(request, { headers }))
  },

  async scheduled(_controller, env) {
    await getContainer(env.SERVER).keepAlive()
  },
} satisfies ExportedHandler<Env>

// The files of the web app that never differ between visitors.
const STATIC_FILES = /^\/(assets|fonts)\//

// keepsAtEdge says whether a static file's response may be cached: it has to
// be the file itself (the server answers an unknown path with the app's
// page, which must not be kept under that path) and say how long it keeps.
function keepsAtEdge(response: Response): boolean {
  const type = response.headers.get('Content-Type') ?? ''
  const keeps = /max-age=[1-9]/.test(response.headers.get('Cache-Control') ?? '')
  return response.status === 200 && keeps && !type.startsWith('text/html')
}

// serverEnv is the server's environment: every variable and secret of this
// Worker that has a value. Bindings are objects, not strings, and are left out.
function serverEnv(env: Env): Record<string, string> {
  const vars: Record<string, string> = { TRUST_PROXY: 'true' }
  for (const [name, value] of Object.entries(env)) {
    if (typeof value === 'string' && value !== '') {
      vars[name] = value
    }
  }
  return vars
}

// fingerprint identifies a set of variables without storing their values,
// some of which are secrets.
async function fingerprint(vars: Record<string, string>): Promise<string> {
  const sorted = JSON.stringify(Object.entries(vars).sort(([a], [b]) => a.localeCompare(b)))
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(sorted))
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('')
}
