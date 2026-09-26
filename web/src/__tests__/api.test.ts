import { describe, it, expect, vi, beforeEach } from 'vitest'
import { api, collectFiles, findNode, type TreeNode } from '../api'

// Mock fetch
const mockFetch = vi.fn()
vi.stubGlobal('fetch', mockFetch)

beforeEach(() => {
  mockFetch.mockReset()
})

function mockJSON(data: any) {
  mockFetch.mockResolvedValueOnce({
    ok: true,
    json: () => Promise.resolve(data),
  })
}

function mockText(text: string) {
  mockFetch.mockResolvedValueOnce({
    ok: true,
    text: () => Promise.resolve(text),
  })
}

describe('api.config', () => {
  it('fetches config', async () => {
    mockJSON({ site_name: 'test', home: 'home.md' })
    const cfg = await api.config()
    expect(cfg.site_name).toBe('test')
  })
})

describe('api.filesTree', () => {
  it('fetches file tree', async () => {
    mockJSON([{ name: 'test.md', path: 'test.md', type: 'file' }])
    const tree = await api.filesTree()
    expect(tree.length).toBe(1)
    expect(tree[0].name).toBe('test.md')
  })
})

describe('api.filesRead', () => {
  it('reads file content', async () => {
    mockJSON({ content: '# Hello' })
    const content = await api.filesRead('test.md')
    expect(content).toBe('# Hello')
  })
})

describe('api.filesWrite', () => {
  it('writes file', async () => {
    mockJSON({ ok: true })
    const result = await api.filesWrite('test.md', 'content')
    expect(result.ok).toBe(true)
    expect(mockFetch).toHaveBeenCalledWith('/api/docs/write', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ path: 'test.md', content: 'content' }),
    }))
  })
})

describe('collectFiles', () => {
  it('collects all files recursively', () => {
    const tree: TreeNode[] = [
      { name: 'dir', path: 'dir', type: 'dir', children: [
        { name: 'a.md', path: 'dir/a.md', type: 'file' },
        { name: 'sub', path: 'dir/sub', type: 'dir', children: [
          { name: 'b.md', path: 'dir/sub/b.md', type: 'file' },
        ]},
      ]},
      { name: 'c.md', path: 'c.md', type: 'file' },
    ]
    const files = collectFiles(tree)
    expect(files.length).toBe(3)
  })
})

describe('findNode', () => {
  it('finds a node by path', () => {
    const tree: TreeNode[] = [
      { name: 'dir', path: 'dir', type: 'dir', children: [
        { name: 'a.md', path: 'dir/a.md', type: 'file' },
      ]},
    ]
    const node = findNode(tree, 'dir/a.md')
    expect(node).not.toBeNull()
    expect(node!.name).toBe('a.md')
  })

  it('returns null for nonexistent path', () => {
    const tree: TreeNode[] = []
    const node = findNode(tree, 'nope.md')
    expect(node).toBeNull()
  })
})
