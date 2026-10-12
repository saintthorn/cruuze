import { describe, expect, it } from 'vitest'

import { initials, suggestTeamKey, suggestUrlKey, TEAM_KEY_PATTERN, URL_KEY_PATTERN } from './keys'

describe('suggestUrlKey', () => {
  it('slugifies names', () => {
    expect(suggestUrlKey('Acme Inc.')).toBe('acme-inc')
    expect(suggestUrlKey('  Hello   World  ')).toBe('hello-world')
    expect(suggestUrlKey('Café Münster')).toBe('cafe-munster')
  })

  it('produces keys the server accepts', () => {
    const key = suggestUrlKey('A very long workspace name that keeps going and going')
    expect(key.length).toBeLessThanOrEqual(32)
    expect(key).toMatch(URL_KEY_PATTERN)
  })

  it('returns an empty string when nothing usable remains', () => {
    expect(suggestUrlKey('!!!')).toBe('')
  })
})

describe('suggestTeamKey', () => {
  it('takes the first three letters', () => {
    expect(suggestTeamKey('Engineering')).toBe('ENG')
    expect(suggestTeamKey('qa')).toBe('QA')
  })

  it('never starts with a digit', () => {
    expect(suggestTeamKey('2024 Planning')).toBe('PLA')
    expect(suggestTeamKey('R2 Unit')).toMatch(TEAM_KEY_PATTERN)
  })
})

describe('initials', () => {
  it('uses first and last name', () => {
    expect(initials('Ada Lovelace')).toBe('AL')
    expect(initials('Ada Augusta Lovelace')).toBe('AL')
    expect(initials('ada')).toBe('A')
  })

  it('falls back to the email', () => {
    expect(initials('', 'grace@example.com')).toBe('G')
    expect(initials('', '')).toBe('?')
  })
})
