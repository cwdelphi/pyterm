<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { TreeNode } from '../api'

const { t } = useI18n()

const props = defineProps<{
  node: TreeNode
  depth: number
  activePath: string
  expanded: Record<string, boolean>
}>()

const emit = defineEmits<{
  (e: 'click', node: TreeNode): void
  (e: 'toggle', path: string): void
  (e: 'new', path: string): void
  (e: 'rename', path: string): void
  (e: 'delete', path: string, name: string): void
  (e: 'move', path: string, name: string): void
}>()

const indent = props.depth * 18 + 8
</script>

<template>
  <div class="tree-node">
    <div
      class="dm-tree-row"
      :class="{ active: node.path === activePath && node.type === 'file' }"
    >
      <button
        class="dm-tree-item"
        :style="{ paddingLeft: indent + 'px' }"
        @click="emit('click', node)"
      >
        <span
          v-if="node.type === 'dir'"
          class="dm-chevron"
          :class="{ open: expanded[node.path] }"
          @click.stop="emit('toggle', node.path)"
        >▶</span>
        <span v-else class="dm-spacer"></span>
        <span class="dm-icon">
          <template v-if="node.type === 'dir'">
            <svg v-if="expanded[node.path]" width="16" height="16" viewBox="0 0 24 24" fill="var(--accent)" stroke="none"><path d="M2 6a2 2 0 012-2h5l2 2h9a2 2 0 012 2v10a2 2 0 01-2 2H4a2 2 0 01-2-2V6z"/></svg>
            <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="var(--muted)" stroke="none"><path d="M2 6a2 2 0 012-2h5l2 2h9a2 2 0 012 2v10a2 2 0 01-2 2H4a2 2 0 01-2-2V6z"/></svg>
          </template>
          <template v-else>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--muted)" stroke-width="2"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>
          </template>
        </span>
        <span class="dm-name">{{ node.name }}</span>
      </button>
      <div class="dm-row-actions">
        <button v-if="node.type === 'dir'" class="dm-act" @click.stop="emit('new', node.path)" :title="t('fileTree.new')">＋</button>
        <button class="dm-act" @click.stop="emit('move', node.path, node.name)" :title="t('fileTree.move')">⟶</button>
        <button class="dm-act" @click.stop="emit('rename', node.path)" :title="t('fileTree.rename')">✎</button>
        <button class="dm-act danger" @click.stop="emit('delete', node.path, node.name)" :title="t('fileTree.delete')">✕</button>
      </div>
    </div>
    <div v-if="node.type === 'dir' && expanded[node.path] && node.children" class="dm-children">
      <FileTreeNode
        v-for="child in node.children"
        :key="child.path"
        :node="child"
        :depth="depth + 1"
        :active-path="activePath"
        :expanded="expanded"
        @click="(n) => emit('click', n)"
        @toggle="(p) => emit('toggle', p)"
        @new="(p) => emit('new', p)"
        @rename="(p) => emit('rename', p)"
        @delete="(p, n) => emit('delete', p, n)"
        @move="(p, n) => emit('move', p, n)"
      />
      <div v-if="!node.children.length" class="dm-empty-row" :style="{ paddingLeft: (depth + 1) * 18 + 8 + 'px' }">
        {{ t('fileTree.emptyDir') }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.dm-tree-row {
  position: relative;
  display: flex;
  align-items: center;
}
.dm-tree-item {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 8px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  color: var(--fg-2);
  min-height: 34px;
  transition: background .1s;
}
.dm-tree-item:hover {
  background: var(--panel-2);
  color: var(--fg);
}
.dm-tree-row.active .dm-tree-item {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}
.dm-chevron {
  font-size: 10px;
  width: 14px;
  text-align: center;
  transition: transform .2s;
  color: var(--muted);
  flex-shrink: 0;
}
.dm-chevron.open {
  transform: rotate(90deg);
}
.dm-spacer {
  width: 14px;
  flex-shrink: 0;
}
.dm-icon {
  flex-shrink: 0;
  display: flex;
  align-items: center;
}
.dm-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 0 1 auto;
}
.dm-row-actions {
  position: absolute;
  right: 8px;
  top: 50%;
  transform: translateY(-50%);
  display: flex;
  gap: 2px;
  opacity: 0;
  transition: opacity .15s;
}
.dm-tree-row:hover .dm-row-actions,
.dm-tree-row.active .dm-row-actions {
  opacity: 1;
}
.dm-act {
  min-width: 22px;
  min-height: 22px;
  font-size: 11px;
  border-radius: 4px;
  color: var(--muted);
}
.dm-act:hover {
  background: var(--panel-2);
  color: var(--fg);
}
.dm-act.danger:hover {
  color: #dc2626;
}
.dm-empty-row {
  font-size: 11px;
  color: var(--muted);
  padding: 4px 8px;
}
</style>
