import { ref } from 'vue'

// The app's looks. Each theme's colors, type and corners are in themes.css
// under its id; keep the two in step. The choice is kept in this browser, so
// it applies before signing in and is not shared between devices.

export interface Theme {
  id: string
  name: string
}

export const THEMES = [
  { id: 'midnight', name: 'mode=midnight' },
  { id: 'hacker', name: 'mode=hacker' },
  { id: 'material', name: 'mode=material' },
  { id: 'quest', name: 'mode=quest' },
  { id: 'hyperdrive', name: 'mode=hyperdrive' },
] as const satisfies readonly Theme[]

export type ThemeId = (typeof THEMES)[number]['id']

export const DEFAULT_THEME: ThemeId = 'midnight'

const THEME_KEY = 'cruuze.theme'

function isTheme(value: unknown): value is ThemeId {
  return THEMES.some((theme) => theme.id === value)
}

function stored(): ThemeId {
  try {
    const value = localStorage.getItem(THEME_KEY)
    return isTheme(value) ? value : DEFAULT_THEME
  } catch {
    // Storage can be blocked; the default still works.
    return DEFAULT_THEME
  }
}

/** The theme the page is in. */
export const currentTheme = ref<ThemeId>(DEFAULT_THEME)

/** Puts the page in the theme chosen earlier in this browser. */
export function applyStoredTheme() {
  currentTheme.value = stored()
  document.documentElement.dataset.theme = currentTheme.value
}

/** Changes the theme, here and in the browser's other tabs when they next load. */
export function setTheme(id: ThemeId) {
  currentTheme.value = id
  document.documentElement.dataset.theme = id
  try {
    localStorage.setItem(THEME_KEY, id)
  } catch {
    // Not remembered, but applied.
  }
}
