import { describe, it, expect, beforeEach } from 'vitest'
import { themeMode, accent, setThemeMode, setAccent, applyTheme } from '../theme'

describe('theme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.dataset.theme = 'light'
    document.documentElement.dataset.accent = 'blue'
  })

  it('has default theme mode', () => {
    expect(['light', 'dark', 'auto']).toContain(themeMode.value)
  })

  it('has default accent', () => {
    expect(['blue', 'green', 'purple']).toContain(accent.value)
  })

  it('setThemeMode changes mode', () => {
    setThemeMode('dark')
    expect(themeMode.value).toBe('dark')
    expect(localStorage.getItem('themeMode')).toBe('dark')
  })

  it('setAccent changes accent', () => {
    setAccent('green')
    expect(accent.value).toBe('green')
    expect(localStorage.getItem('accent')).toBe('green')
  })

  it('applyTheme sets dataset', () => {
    setThemeMode('dark')
    applyTheme()
    expect(document.documentElement.dataset.theme).toBe('dark')

    setAccent('purple')
    applyTheme()
    expect(document.documentElement.dataset.accent).toBe('purple')
  })
})
