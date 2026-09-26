<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

interface Crumb {
  label: string
  path: string
  last: boolean
}

defineProps<{
  crumbs: Crumb[]
  siteName: string
  hasToc: boolean
  tocOpen: boolean
}>()

const emit = defineEmits<{
  (e: 'nav', path: string): void
  (e: 'home'): void
  (e: 'scrollTop'): void
  (e: 'toggleToc'): void
}>()
</script>

<template>
  <nav class="breadcrumb">
    <div class="crumbs">
      <button class="crumb" @click="emit('home')">{{ siteName }}</button>
      <template v-for="(c, i) in crumbs" :key="i">
        <span class="crumb-sep">/</span>
        <button
          class="crumb"
          :class="{ current: c.last }"
          :disabled="c.last"
          @click="!c.last && emit('nav', c.path)"
        >
          {{ c.label }}
        </button>
      </template>
    </div>
    <div class="crumb-actions">
      <button v-if="hasToc" class="crumb-action" :class="{ active: tocOpen }" @click="emit('toggleToc')">
        <span>{{ t('breadcrumb.dir') }}</span> ☰
      </button>
      <button class="crumb-action" @click="emit('scrollTop')"><span>{{ t('breadcrumb.backToTop') }}</span> ↑</button>
    </div>
  </nav>
</template>
<style scoped>
.breadcrumb { display:flex; align-items:center; gap:6px; font-size:13px; color:var(--muted); padding:8px 0; }
.breadcrumb a { color:var(--accent); text-decoration:none; cursor:pointer; }
.breadcrumb a:hover { text-decoration:underline; }
.breadcrumb .sep { color:var(--border); user-select:none; }
.breadcrumb .crumb-action { font-size:12px; color:var(--muted); border:none; background:none; cursor:pointer; padding:2px 6px; border-radius:4px; }
.breadcrumb .crumb-action:hover { background:var(--panel-2); color:var(--accent); }
</style>
