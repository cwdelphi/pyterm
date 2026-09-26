<script setup lang="ts">
defineProps<{
  cols?: number
}>()
</script>

<template>
  <div class="skeleton-table">
    <div class="skeleton-row" v-for="i in (cols || 5)" :key="i">
      <div class="skeleton-cell"></div>
      <div class="skeleton-cell"></div>
      <div class="skeleton-cell"></div>
      <div class="skeleton-cell"></div>
    </div>
  </div>
</template>

<style scoped>
.skeleton-table { width: 100%; }
.skeleton-row {
  display: flex;
  gap: var(--spacing-md);
  padding: 14px 16px;
  border-bottom: 1px solid var(--border);
}
.skeleton-cell {
  height: 14px;
  border-radius: var(--radius-sm);
  background: linear-gradient(90deg, var(--panel-2) 25%, var(--border) 50%, var(--panel-2) 75%);
  background-size: 200% 100%;
  animation: shimmer 1.5s infinite;
  flex: 1;
}
@keyframes shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}
</style>
