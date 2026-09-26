import MarkdownIt from 'markdown-it'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import python from 'highlight.js/lib/languages/python'
import json from 'highlight.js/lib/languages/json'
import yaml from 'highlight.js/lib/languages/yaml'
import sql from 'highlight.js/lib/languages/sql'
import go from 'highlight.js/lib/languages/go'
import nginx from 'highlight.js/lib/languages/nginx'
import css from 'highlight.js/lib/languages/css'
import plaintext from 'highlight.js/lib/languages/plaintext'
import mermaid from 'mermaid'

// 只注册实际使用的语言（节省 ~250KB）
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('python', python)
hljs.registerLanguage('json', json)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('go', go)
hljs.registerLanguage('nginx', nginx)
hljs.registerLanguage('css', css)
hljs.registerLanguage('plaintext', plaintext)
// conf 等未知语言回退到 plaintext

export interface TocItem {
  id: string
  text: string
  level: number
}

export interface RenderedDoc {
  html: string
  toc: TocItem[]
}

const utils = new MarkdownIt().utils

const md = new MarkdownIt({
  html: true,
  linkify: true,
  breaks: false,
  typographer: false,
  highlight(code: string, lang: string): string {
    if (lang && lang !== 'mermaid' && hljs.getLanguage(lang)) {
      try {
        const hl = hljs.highlight(code, { language: lang, ignoreIllegals: true }).value
        return `<pre class="hljs"><code class="language-${lang}">${hl}</code></pre>`
      } catch {
        /* fallthrough */
      }
    }
    return `<pre class="hljs"><code>${utils.escapeHtml(code)}</code></pre>`
  }
})

const defaultFence = md.renderer.rules.fence!
md.renderer.rules.fence = (tokens, idx, options, env, slf) => {
  const token = tokens[idx]
  const lang = token.info.trim().split(/\s+/)[0]
  if (lang === 'mermaid') {
    return `<div class="mermaid-wrap"><pre class="mermaid">${utils.escapeHtml(token.content)}</pre></div>`
  }
  return defaultFence(tokens, idx, options, env, slf)
}

function slugify(text: string): string {
  const slug = text
    .trim()
    .toLowerCase()
    .replace(/[\s\u3000]+/g, '-')
    .replace(/[^\w\u4e00-\u9fa5-]/g, '')
    .replace(/-+/g, '-')
  return slug || 'sec'
}

export function renderDoc(src: string): RenderedDoc {
  const env: unknown = {}
  const tokens = md.parse(src, env)
  const toc: TocItem[] = []
  const counts: Record<string, number> = {}
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i]
    if (t.type === 'heading_open') {
      const level = Number(t.tag.slice(1))
      const inline = tokens[i + 1]
      const text = inline ? inline.content : ''
      let id = slugify(text)
      if (counts[id] !== undefined) {
        counts[id] += 1
        id = `${id}-${counts[id]}`
      } else {
        counts[id] = 0
      }
      t.attrSet('id', id)
      toc.push({ id, text, level })
    }
  }
  const html = md.renderer.render(tokens, md.options, env)
  return { html, toc }
}

let _mermaidOverlayBound = false
function setupMermaidOverlay() {
  if (_mermaidOverlayBound) return
  _mermaidOverlayBound = true
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      const overlay = document.querySelector('.mermaid-overlay')
      if (overlay) overlay.remove()
    }
  })
}
function bindMermaidClick(wrap: HTMLElement) {
  wrap.addEventListener('click', () => {
    const svg = wrap.querySelector('svg')
    if (!svg) return
    const overlay = document.createElement('div')
    overlay.className = 'mermaid-overlay'
    const clone = svg.cloneNode(true) as SVGSVGElement
    clone.style.maxWidth = '90vw'
    clone.style.maxHeight = '90vh'
    clone.style.width = 'auto'
    clone.style.height = 'auto'
    clone.style.cursor = 'zoom-out'
    overlay.appendChild(clone)
    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) overlay.remove()
    })
    document.body.appendChild(overlay)
  })
}

export async function renderMermaid(container: HTMLElement, dark: boolean): Promise<void> {
  const nodes = container.querySelectorAll('pre.mermaid')
  if (!nodes.length) return
  // Assign unique IDs to mermaid nodes to avoid conflicts
  nodes.forEach((node, i) => {
    if (!node.id) node.id = 'mermaid-' + Date.now() + '-' + i
  })
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    theme: dark ? 'dark' : 'default',
    themeVariables: dark ? { darkMode: true } : {},
    flowchart: { useMaxWidth: false, htmlLabels: true }
  })
  try {
    await mermaid.run({ nodes: Array.from(nodes) as unknown as HTMLElement[] })
  } catch (e) {
    // Retry once after a short delay (getBBox race condition)
    await new Promise(r => setTimeout(r, 100))
    try {
      await mermaid.run({ nodes: Array.from(nodes) as unknown as HTMLElement[] })
    } catch (e2) {
      console.error('mermaid render failed:', e2)
    }
  }
  setupMermaidOverlay()
  container.querySelectorAll<HTMLElement>('.mermaid-wrap').forEach(bindMermaidClick)
}