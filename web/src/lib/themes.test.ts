import { readFileSync } from 'node:fs'

import { beforeEach, describe, expect, it, vi } from 'vitest'

import { applyStoredTheme, currentTheme, DEFAULT_THEME, setTheme, THEMES } from './themes'

// Just enough of a browser: the <html> element's data attributes and storage.
let dataset: Record<string, string>
let storage: Map<string, string>

beforeEach(() => {
  dataset = {}
  storage = new Map()
  vi.stubGlobal('document', { documentElement: { dataset } })
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => storage.get(key) ?? null,
    setItem: (key: string, value: string) => void storage.set(key, value),
  })
})

describe('themes', () => {
  it('starts in the default theme', () => {
    applyStoredTheme()
    expect(currentTheme.value).toBe(DEFAULT_THEME)
    expect(dataset.theme).toBe('midnight')
  })

  it('remembers the chosen theme', () => {
    setTheme('quest')
    expect(dataset.theme).toBe('quest')
    delete dataset.theme
    applyStoredTheme()
    expect(currentTheme.value).toBe('quest')
    expect(dataset.theme).toBe('quest')
  })

  it('ignores a stored theme that no longer exists', () => {
    storage.set('cruuze.theme', 'vaporwave')
    applyStoredTheme()
    expect(currentTheme.value).toBe(DEFAULT_THEME)
  })

  it('applies a theme even when storage is blocked', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    })
    setTheme('hacker')
    expect(dataset.theme).toBe('hacker')
    applyStoredTheme()
    expect(currentTheme.value).toBe(DEFAULT_THEME)
  })

  it('has styles for every theme', () => {
    const css = readFileSync(new URL('../themes.css', import.meta.url), 'utf8')
    for (const theme of THEMES) expect(css).toContain(`[data-theme='${theme.id}'] {`)
  })
})
