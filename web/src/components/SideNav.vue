<script setup lang="ts">
import { reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TreeNode } from '../api'

const { t } = useI18n()

const props = defineProps<{
  nodes: TreeNode[]
  active: string
}>()

const emit = defineEmits<{
  (e: 'select', node: TreeNode): void
}>()

const collapsed = reactive<Record<string, boolean>>({})

function defaultOpen(nodes: TreeNode[]) {
  for (const n of nodes) {
    if (n.type === 'dir') {
      collapsed[n.path] = false
      defaultOpen(n.children ?? [])
    }
  }
}

watch(
  () => props.nodes,
  (v) => defaultOpen(v),
  { immediate: true }
)

function toggle(node: TreeNode) {
  collapsed[node.path] = !collapsed[node.path]
}
</script>

<template>
  <ul class="sidenav">
    <li v-for="node in nodes" :key="node.path">
      <button
        v-if="node.type === 'dir'"
        class="nav-btn"
        @click="toggle(node)"
      >
        <span class="chev" :class="{ open: !collapsed[node.path] }">▶</span>
        <span class="dot"></span>
        <span class="nav-name">{{ node.name }}</span>
      </button>
      <button
        v-else
        class="nav-btn"
        :class="{ active: node.path === active }"
        @click="emit('select', node)"
      >
        <span class="file-dot"></span>
        <span class="nav-name">{{ node.name }}</span>
      </button>
      <div v-if="node.type === 'dir' && !collapsed[node.path]" class="nav-children">
        <SideNav
          v-if="node.children && node.children.length"
          :nodes="node.children"
          :active="active"
          @select="(n: TreeNode) => emit('select', n)"
        />
        <div v-else class="nav-empty">{{ t('fileTree.emptyDir') }}</div>
      </div>
    </li>
  </ul>
</template>

<style scoped>
.file-dot {
  width: 10px;
  height: 2px;
  border-radius: 1px;
  background: var(--muted);
  flex-shrink: 0;
}

.nav-empty {
  font-size: 12px;
  color: var(--muted);
  padding: 4px 8px 8px 24px;
}
</style>