/**
 * Manual ordering uses fractional positions: to put an issue between two
 * others it gets a sort order between theirs, so nothing else has to move.
 */

/**
 * `count` evenly spaced sort orders strictly between `before` and `after`.
 * Either bound may be missing: at the top of a list there is nothing before,
 * at the bottom nothing after.
 */
export function sortOrdersBetween(before: number | undefined, after: number | undefined, count = 1): number[] {
  if (before === undefined && after === undefined) {
    return Array.from({ length: count }, (_, i) => i)
  }
  if (before === undefined) {
    // Above the first item: count down from it.
    return Array.from({ length: count }, (_, i) => after! - (count - i))
  }
  if (after === undefined) {
    return Array.from({ length: count }, (_, i) => before + i + 1)
  }
  const step = (after - before) / (count + 1)
  return Array.from({ length: count }, (_, i) => before + step * (i + 1))
}

/**
 * Sort orders for `count` items dropped at `index` among `zone`, the sort
 * orders of the group or column they land in (ascending, without the items
 * being moved). `all` holds the sort orders of every other issue on screen:
 * at the edge of a zone the new position is squeezed in before the next issue
 * of any zone, so it can't collide with one. Returns undefined when the zone
 * is empty, meaning the items can keep their sort orders.
 */
export function sortOrdersForDrop(
  zone: readonly number[],
  index: number,
  count = 1,
  all: readonly number[] = zone,
): number[] | undefined {
  if (zone.length === 0) return undefined
  let before = zone[index - 1]
  let after = zone[index]
  if (after === undefined && before !== undefined) {
    const above = all.filter((order) => order > before!)
    if (above.length > 0) after = Math.min(...above)
  } else if (before === undefined && after !== undefined) {
    const below = all.filter((order) => order < after!)
    if (below.length > 0) before = Math.max(...below)
  }
  return sortOrdersBetween(before, after, count)
}
