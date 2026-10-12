/**
 * Tokens given for imports in this tab, so that running one again soon after
 * needs nothing typed. They live in memory only: a reload forgets them, and
 * they are never stored.
 */
const secrets = new Map<string, string>()

const key = (source: string, label: string) => `${source}\n${label}`

export function rememberImportSecret(source: string, label: string, secret: string) {
  // A public GitHub repository is read without one, which is worth knowing too.
  if (secret || source === 'github') secrets.set(key(source, label), secret)
}

export function importSecret(source: string, label: string): string | undefined {
  return secrets.get(key(source, label))
}
