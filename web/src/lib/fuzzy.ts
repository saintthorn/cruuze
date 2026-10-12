/**
 * Scores how well `query` matches `text`: higher is better, 0 is no match.
 * A substring match beats letters merely appearing in order, and matches at
 * the start of the text or of a word beat matches in the middle.
 */
export function fuzzyScore(query: string, text: string): number {
  const q = query.trim().toLowerCase()
  if (!q) return 1
  const t = text.toLowerCase()

  const at = t.indexOf(q)
  if (at === 0) return 100
  if (at > 0) return /[\s\-_/]/.test(t[at - 1]!) ? 80 : 60

  // Letters in order, e.g. "nwi" matching "New issue".
  let from = 0
  let gaps = 0
  for (const ch of q) {
    const found = t.indexOf(ch, from)
    if (found === -1) return 0
    gaps += found - from
    from = found + 1
  }
  return Math.max(1, 40 - gaps)
}

/** The items that match `query`, best first; ties keep their original order. */
export function fuzzyFilter<T>(items: readonly T[], query: string, text: (item: T) => string): T[] {
  if (!query.trim()) return [...items]
  return items
    .map((item, index) => ({ item, index, score: fuzzyScore(query, text(item)) }))
    .filter((entry) => entry.score > 0)
    .sort((a, b) => b.score - a.score || a.index - b.index)
    .map((entry) => entry.item)
}
