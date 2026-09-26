<script setup lang="ts">
import { ref, watch, onMounted, nextTick } from 'vue'
import { renderDoc, renderMermaid, type TocItem } from '../markdown'

const props = defineProps<{
  content: string
  dark: boolean
}>()

const emit = defineEmits<{
  (e: 'toc', toc: TocItem[]): void
}>()

const root = ref<HTMLElement>()
const isLoading = ref(false)

async function doRender() {
  if (!root.value) return
  const { html, toc } = renderDoc(props.content ?? '')
  root.value.innerHTML = html
  emit('toc', toc)
  await nextTick()
  isLoading.value = true
  await renderMermaid(root.value, props.dark)
  isLoading.value = false
}

watch(() => props.content, doRender)
watch(() => props.dark, doRender)
onMounted(doRender)

defineExpose({ doRender })
</script>

<template>
  <div>
    <div v-if="isLoading" class="doc-loading">
      <div class="spinner"></div>
    </div>
    <article ref="root" class="markdown-body" v-show="!isLoading"></article>
  </div>
</template>
<style scoped>
.md-view { min-height:200px; }
.md-loading { display:flex; align-items:center; justify-content:center; min-height:200px; color:var(--muted); font-size:14px; }
</style>
