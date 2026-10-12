import type { Issue } from '@/sync/types'

// Words that say nothing about what an issue is about: grammar, and the
// verbs nearly every title starts with.
const NOISE = new Set(
  ('the and for with when from that this not are was were has have had its into than then can cannot does did will ' +
    'add fix make use update remove improve support allow issue bug error problem').split(' '),
)

/** The distinct meaningful words of a title, crudely stemmed so "crashes" meets "crash". */
export function titleWords(title: string): string[] {
  const words = new Set<string>()
  for (const raw of title.toLowerCase().split(/[^\p{L}\p{N}]+/u)) {
    if (raw.length < 3 || NOISE.has(raw)) continue
    words.add(raw.replace(/(ing|ed|es|s)$/, '') || raw)
  }
  return [...words]
}

// Tokenizing every title on every keystroke would be wasteful; issue
// records are replaced, never mutated, so the words can be remembered.
const cache = new WeakMap<Issue, string[]>()

function wordsOf(issue: Issue): string[] {
  let words = cache.get(issue)
  if (!words) {
    words = titleWords(issue.title)
    cache.set(issue, words)
  }
  return words
}

/**
 * Issues whose titles share enough words with `title` to be about the same
 * thing, best match first. Meant for "does this already exist?" while
 * someone types a new issue.
 */
export function similarIssues(title: string, issues: readonly Issue[], limit = 5): Issue[] {
  const wanted = titleWords(title)
  if (wanted.length < 2) return []
  const lookup = new Set(wanted)
  const scored: { issue: Issue; score: number }[] = []
  for (const issue of issues) {
    const words = wordsOf(issue)
    let shared = 0
    for (const word of words) if (lookup.has(word)) shared++
    if (shared < 2) continue
    // Cosine similarity of the two word sets.
    const score = shared / Math.sqrt(wanted.length * words.length)
    if (score >= 0.5) scored.push({ issue, score })
  }
  return scored
    .sort((a, b) => b.score - a.score)
    .slice(0, limit)
    .map((entry) => entry.issue)
}
