import { describe, it, expect } from 'vitest'
import { renderDoc } from '../markdown'

describe('renderDoc', () => {
  it('renders simple markdown', () => {
    const { html } = renderDoc('# Hello')
    expect(html).toContain('<h1')
    expect(html).toContain('Hello')
  })

  it('generates TOC from headings', () => {
    const { toc } = renderDoc('# H1\n## H2\n### H3')
    expect(toc.length).toBe(3)
    expect(toc[0].level).toBe(1)
    expect(toc[1].level).toBe(2)
    expect(toc[2].level).toBe(3)
  })

  it('handles code blocks', () => {
    const { html } = renderDoc('```python\nprint("hi")\n```')
    expect(html).toContain('hljs')
    expect(html).toContain('print')
  })

  it('handles links', () => {
    const { html } = renderDoc('[link](https://example.com)')
    expect(html).toContain('href')
    expect(html).toContain('example.com')
  })

  it('handles bold and italic', () => {
    const { html } = renderDoc('**bold** and *italic*')
    expect(html).toContain('<strong>')
    expect(html).toContain('<em>')
  })

  it('handles blockquotes', () => {
    const { html } = renderDoc('> quote')
    expect(html).toContain('<blockquote>')
  })

  it('handles tables', () => {
    const md = '| A | B |\n|---|---|\n| 1 | 2 |'
    const { html } = renderDoc(md)
    expect(html).toContain('<table>')
    expect(html).toContain('A')
    expect(html).toContain('1')
  })

  it('deduplicates heading IDs', () => {
    const { toc } = renderDoc('## Same\n## Same\n## Same')
    expect(toc[0].id).not.toBe(toc[1].id)
    expect(toc[1].id).not.toBe(toc[2].id)
  })

  it('generates IDs for Chinese headings', () => {
    const { toc } = renderDoc('## 测试标题')
    expect(toc.length).toBe(1)
    expect(toc[0].id).toBeTruthy()
  })
})
