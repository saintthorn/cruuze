import { describe, expect, it } from 'vitest'

import { fuzzyFilter, fuzzyScore } from './fuzzy'

describe('fuzzyScore', () => {
  it('ranks prefix, word start, substring, then scattered letters', () => {
    const prefix = fuzzyScore('new', 'New issue')
    const wordStart = fuzzyScore('iss', 'New issue')
    const inside = fuzzyScore('ssu', 'New issue')
    const scattered = fuzzyScore('nwi', 'New issue')
    expect(prefix).toBeGreaterThan(wordStart)
    expect(wordStart).toBeGreaterThan(inside)
    expect(inside).toBeGreaterThan(scattered)
    expect(scattered).toBeGreaterThan(0)
  })

  it('rejects text missing a letter or with letters out of order', () => {
    expect(fuzzyScore('xyz', 'New issue')).toBe(0)
    expect(fuzzyScore('wen', 'New issue')).toBe(0)
  })

  it('matches anything with an empty query', () => {
    expect(fuzzyScore('  ', 'whatever')).toBeGreaterThan(0)
  })
})

describe('fuzzyFilter', () => {
  const commands = ['Go to settings', 'New issue', 'Set priority', 'Assign to…']

  it('filters and orders by score', () => {
    // Prefix, then word start, then letters merely in order (is-SuE); 'Assign' has no e.
    expect(fuzzyFilter(commands, 'se', (c) => c)).toEqual(['Set priority', 'Go to settings', 'New issue'])
  })

  it('keeps the given order without a query', () => {
    expect(fuzzyFilter(commands, '', (c) => c)).toEqual(commands)
  })
})
