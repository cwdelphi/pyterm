<script setup lang="ts">
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

defineProps<{
  icon?: string
  title?: string
  desc?: string
}>()
</script>

<template>
  <div class="page-empty">
    <div class="empty-icon">{{ icon || '📭' }}</div>
    <div class="empty-title">{{ title || t('empty.noData') }}</div>
    <div class="empty-desc" v-if="desc">{{ desc }}</div>
    <slot />
  </div>
</template>

<style scoped>
.empty-icon  { font-size:48px; margin-bottom:var(--spacing-md); opacity:0.6; }
.empty-title { font-size:16px; font-weight:600; color:var(--fg); margin-bottom:var(--spacing-xs); }
.empty-desc  { font-size:13px; color:var(--muted); max-width:320px; }
</style>
