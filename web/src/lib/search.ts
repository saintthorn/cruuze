/** A run of snippet text, highlighted or not. */
export interface SnippetPart {
  text: string
  mark: boolean
}

/**
 * Splits a search snippet into plain and highlighted runs. The server wraps
 * matches in `<mark>`…`</mark>`; everything else is literal text, never HTML,
 * so the parts are meant to be rendered as text nodes.
 */
export function snippetParts(snippet: string): SnippetPart[] {
  snippet = plainSnippet(snippet)
  const parts: SnippetPart[] = []
  // Splitting on both markers leaves highlighted text at the odd positions.
  snippet.split(/<mark>|<\/mark>/).forEach((text, index) => {
    if (text) parts.push({ text, mark: index % 2 === 1 })
  })
  return parts
}

/**
 * Removes the Markdown syntax from an excerpt, which is cut from the middle
 * of a description or comment: links and mentions keep only their text, and
 * emphasis, code and heading markers go.
 */
function plainSnippet(snippet: string): string {
  return snippet
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1') // whole links and images
    .replace(/\]\([^)]*$/, '') // a link the excerpt cut off
    .replace(/\\?[*`~]/g, '')
    .replace(/\\_/g, '_') // underscores stay: they are mostly in names like user_id
    .replace(/(^|\s)#{1,6}\s/g, '$1')
    .replace(/\s+/g, ' ')
    .trim()
}
