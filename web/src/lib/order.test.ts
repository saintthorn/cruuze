import { describe, expect, it } from 'vitest'

import { sortOrdersBetween, sortOrdersForDrop } from './order'

describe('sortOrdersBetween', () => {
  it('lands strictly between two neighbours', () => {
    expect(sortOrdersBetween(1, 2)).toEqual([1.5])
    const [a, b, c] = sortOrdersBetween(0, 4, 3)
    expect([a, b, c]).toEqual([1, 2, 3])
  })

  it('goes above the first item and below the last', () => {
    expect(sortOrdersBetween(undefined, 5)).toEqual([4])
    expect(sortOrdersBetween(undefined, 5, 2)).toEqual([3, 4])
    expect(sortOrdersBetween(5, undefined, 2)).toEqual([6, 7])
  })

  it('works in an empty list', () => {
    expect(sortOrdersBetween(undefined, undefined, 2)).toEqual([0, 1])
  })

  it('keeps moved items in their relative order', () => {
    const orders = sortOrdersBetween(-3, -2.5, 4)
    expect([...orders].sort((x, y) => x - y)).toEqual(orders)
    expect(orders[0]).toBeGreaterThan(-3)
    expect(orders[3]).toBeLessThan(-2.5)
  })
})

describe('sortOrdersForDrop', () => {
  const list = [10, 20, 30]

  it('maps a drop index to the gap it names', () => {
    expect(sortOrdersForDrop(list, 0)).toEqual([9])
    expect(sortOrdersForDrop(list, 1)).toEqual([15])
    expect(sortOrdersForDrop(list, 3)).toEqual([31])
  })

  it('leaves sort orders alone when the zone is empty', () => {
    expect(sortOrdersForDrop([], 0)).toBeUndefined()
  })

  it('stays clear of issues in other zones at the edges', () => {
    // Another column holds 31 and 9: a plain +1/-1 would land exactly on them.
    const all = [9, 10, 20, 30, 31]
    expect(sortOrdersForDrop(list, 3, 1, all)).toEqual([30.5])
    expect(sortOrdersForDrop(list, 0, 1, all)).toEqual([9.5])
    // With nothing beyond the edge, step away from it as before.
    expect(sortOrdersForDrop(list, 3, 1, [5, 10, 20, 30])).toEqual([31])
  })
})
