/** Suggests a workspace URL key from its name, e.g. "Acme Inc." -> "acme-inc". */
export function suggestUrlKey(name: string): string {
  return name
    .toLowerCase()
    .normalize('NFKD')
    .replace(/\p{M}/gu, '') // drop accents split off by normalization
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 32)
    .replace(/-+$/, '')
}

/** Suggests a team identifier from its name, e.g. "Engineering" -> "ENG". */
export function suggestTeamKey(name: string): string {
  const letters = name
    .toUpperCase()
    .normalize('NFKD')
    .replace(/[^A-Z0-9]/g, '')
    .replace(/^[0-9]+/, '')
  return letters.slice(0, 3)
}

export const URL_KEY_PATTERN = /^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$/
export const TEAM_KEY_PATTERN = /^[A-Z][A-Z0-9]{0,6}$/

/** Up to two initials for an avatar, falling back to the email's first letter. */
export function initials(name: string, email = ''): string {
  const words = name.trim().split(/\s+/).filter(Boolean)
  if (words.length === 0) return (email[0] ?? '?').toUpperCase()
  const first = words[0]![0] ?? ''
  const last = words.length > 1 ? (words[words.length - 1]![0] ?? '') : ''
  return (first + last).toUpperCase()
}
