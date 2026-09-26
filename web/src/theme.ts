import { computed, ref } from 'vue'

export type ThemeMode = 'light' | 'dark' | 'auto'
export type Accent = 'blue' | 'green' | 'purple'

const MODE_KEY = 'themeMode'
const ACCENT_KEY = 'accent'

const mq = window.matchMedia('(prefers-color-scheme: dark)')

function load<T>(key: string, fallback: T): T {
  try {
    const v = localStorage.getItem(key)
    return v ? (v as unknown as T) : fallback
  } catch {
    return fallback
  }
}

function save(key: string, val: string) {
  try {
    localStorage.setItem(key, val)
  } catch {
    /* ignore */
  }
}

export const themeMode = ref<ThemeMode>(load<ThemeMode>(MODE_KEY, 'auto'))
export const accent = ref<Accent>(load<Accent>(ACCENT_KEY, 'blue'))

export const isDark = computed(
  () => themeMode.value === 'dark' || (themeMode.value === 'auto' && mq.matches)
)

export function applyTheme() {
  const root = document.documentElement
  root.dataset.theme = isDark.value ? 'dark' : 'light'
  root.dataset.accent = accent.value
}

export function setThemeMode(mode: ThemeMode) {
  themeMode.value = mode
  save(MODE_KEY, mode)
  applyTheme()
}

export function setAccent(a: Accent) {
  accent.value = a
  save(ACCENT_KEY, a)
  applyTheme()
}

mq.addEventListener('change', () => {
  if (themeMode.value === 'auto') applyTheme()
})

applyTheme()