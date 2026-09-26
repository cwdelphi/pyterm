<script setup lang="ts">
import type { TreeNode } from '../api'

defineProps<{
  node: TreeNode
  depth: number
  selected: string
}>()

const emit = defineEmits<{
  (e: 'select', path: string): void
}>()
</script>

<template>
  <div>
    <div
      class="move-dir-item"
      :class="{ active: selected === node.path }"
      :style="{ paddingLeft: (depth * 20 + 10) + 'px' }"
      @click="emit('select', node.path)"
    >
      <span class="move-dir-icon">📂</span>
      <span>{{ node.name }}</span>
    </div>
    <DirSelectNode
      v-if="node.children && node.children.length"
      v-for="child in node.children.filter(c => c.type === 'dir')"
      :key="child.path"
      :node="child"
      :depth="depth + 1"
      :selected="selected"
      @select="(p) => emit('select', p)"
    />
  </div>
</template>
<style scoped>
.dir-node { padding:2px 0; }
.dir-label { display:flex; align-items:center; gap:4px; padding:4px 8px; font-size:13px; color:var(--fg-2); cursor:pointer; border-radius:4px; }
.dir-label:hover { background:var(--panel-2); color:var(--fg); }
.dir-label.active { background:var(--accent-soft); color:var(--accent); font-weight:600; }
.dir-icon { font-size:14px; width:16px; text-align:center; }
.dir-children { padding-left:16px; }
</style>
