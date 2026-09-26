<script setup lang="ts">
import { ref, onBeforeUnmount } from 'vue'

export interface ToastMsg {
  id: number
  text: string
  type: 'success' | 'error' | 'info' | 'warn'
}

const toasts = ref<ToastMsg[]>([])
let nextId = 0
const timerMap: Record<number, ReturnType<typeof setTimeout>> = {}

function add(text: string, type: ToastMsg['type'] = 'info', duration = 3000) {
  const id = nextId++
  toasts.value.push({ id, text, type })
  if (duration > 0) {
    timerMap[id] = setTimeout(() => remove(id), duration)
  }
}

function remove(id: number) {
  if (timerMap[id]) { clearTimeout(timerMap[id]); delete timerMap[id] }
  toasts.value = toasts.value.filter(t => t.id !== id)
}

function success(text: string) { add(text, 'success') }
function error(text: string) { add(text, 'error', 4000) }
function info(text: string) { add(text, 'info') }
function warn(text: string) { add(text, 'warn', 4000) }

onBeforeUnmount(() => { Object.values(timerMap).forEach(clearTimeout) })

defineExpose({ add, remove, success, error, info, warn })
</script>

<template>
  <div class="toast-container">
    <TransitionGroup name="toast">
      <div
        v-for="t in toasts" :key="t.id"
        class="toast-item" :class="t.type"
        @click="remove(t.id)"
      >
        <span class="toast-icon">
          <template v-if="t.type==='success'">✓</template>
          <template v-else-if="t.type==='error'">✗</template>
          <template v-else-if="t.type==='warn'">⚠</template>
          <template v-else>ℹ</template>
        </span>
        <span class="toast-text">{{ t.text }}</span>
      </div>
    </TransitionGroup>
  </div>
</template>

<style scoped>
.toast-container {
  position: fixed;
  top: 64px;
  right: 20px;
  z-index: 9999;
  display: flex;
  flex-direction: column;
  gap: 8px;
  pointer-events: none;
}
.toast-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 18px;
  border-radius: 8px;
  font-size: 13px;
  font-weight: 500;
  background: var(--panel);
  border: 1px solid var(--border);
  box-shadow: 0 8px 30px rgba(0,0,0,.15);
  pointer-events: auto;
  cursor: pointer;
  min-width: 200px;
  max-width: 400px;
}
.toast-item.success { border-left: 3px solid #16a34a; color: #16a34a; }
.toast-item.error { border-left: 3px solid #dc2626; color: #dc2626; }
.toast-item.warn { border-left: 3px solid #f59e0b; color: #f59e0b; }
.toast-item.info { border-left: 3px solid var(--accent); color: var(--accent); }
.toast-icon { font-size: 15px; font-weight: 700; flex-shrink: 0; }
.toast-text { flex: 1; }

.toast-enter-active { animation: toastIn .25s ease; }
.toast-leave-active { animation: toastOut .2s ease; }
@keyframes toastIn { from { opacity:0; transform: translateX(40px); } to { opacity:1; transform: translateX(0); } }
@keyframes toastOut { from { opacity:1; transform: translateX(0); } to { opacity:0; transform: translateX(40px); } }
</style>
