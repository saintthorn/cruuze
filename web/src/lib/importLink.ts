/**
 * Working out, from a link pasted into the import page, which tracker it
 * points at and what the importer needs to know about it.
 */
export type ImportLink =
  | { source: 'github'; repo: string; baseUrl?: string }
  | { source: 'jira'; baseUrl: string }
  | { source: 'linear' }

const REPO = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/

/** What a pasted link (or a bare owner/name) refers to, or null if it can't be told. */
export function parseImportLink(raw: string): ImportLink | null {
  const text = raw.trim()
  if (REPO.test(text)) return { source: 'github', repo: text.replace(/\.git$/, '') }
  // git@github.com:owner/name.git
  const ssh = /^git@github\.com:([^/\s]+\/[^/\s]+?)(?:\.git)?$/.exec(text)
  if (ssh && REPO.test(ssh[1]!)) return { source: 'github', repo: ssh[1]! }

  let url: URL
  try {
    url = new URL(/^[a-z]+:\/\//i.test(text) ? text : `https://${text}`)
  } catch {
    return null
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') return null
  const host = url.hostname.toLowerCase()
  const parts = url.pathname.split('/').filter(Boolean)

  if (host === 'github.com' || host === 'www.github.com') {
    const repo = parts.length >= 2 ? `${parts[0]}/${parts[1]!.replace(/\.git$/, '')}` : ''
    return REPO.test(repo) ? { source: 'github', repo } : null
  }
  if (host === 'linear.app' || host.endsWith('.linear.app')) return { source: 'linear' }
  if (host.endsWith('.atlassian.net') || host.endsWith('.jira.com')) return { source: 'jira', baseUrl: url.origin }
  // A Jira Server or Data Center: its issue and project pages give it away,
  // and whatever comes before them is where it is served from.
  const marker = parts.findIndex((part, i) => part === 'browse' || part === 'secure' || (part === 'projects' && parts[i + 1] !== undefined))
  if (marker >= 0 || host.split('.').includes('jira') || parts[0] === 'jira') {
    const base = marker >= 0 ? parts.slice(0, marker) : parts[0] === 'jira' ? ['jira'] : []
    return { source: 'jira', baseUrl: [url.origin, ...base].join('/') }
  }
  return null
}
