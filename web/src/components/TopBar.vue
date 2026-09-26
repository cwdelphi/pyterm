<script setup lang="ts">
import { ref, computed, inject, onMounted, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { themeMode, accent, setThemeMode, setAccent, type ThemeMode, type Accent } from '../theme'
import { getCurrentUser, logout, type User } from '../api'
import { setLocale, getLocale } from '../i18n'

const { t } = useI18n()

defineProps<{
  siteName: string
  view: string
}>()

const emit = defineEmits<{
  (e: 'switchView', view: string): void
  (e: 'home'): void
}>()

const popOpen = ref(false)
const popRef = ref<HTMLElement>()
const userPopOpen = ref(false)
const userPopRef = ref<HTMLElement>()
const currentUser = ref<User | null>(null)

function onDocClick(e: Event) {
  if (popRef.value && !popRef.value.contains(e.target as Node)) popOpen.value = false
  if (userPopRef.value && !userPopRef.value.contains(e.target as Node)) userPopOpen.value = false
}

onMounted(() => {
  document.addEventListener('click', onDocClick)
  currentUser.value = getCurrentUser()
})
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))

const modeLabels = computed<Record<ThemeMode, string>>(() => ({ light: t('topbar.light'), dark: t('topbar.dark'), auto: t('topbar.auto') }))
const accents: { key: Accent; label: string }[] = [
  { key: 'blue', label: '蓝' },
  { key: 'green', label: '绿' },
  { key: 'purple', label: '紫' }
]

const baseFeatures = computed(() => [
  { key: 'docs', label: t('nav.docs'), icon: '📖' },
  { key: 'links', label: t('nav.links'), icon: '🔗' },
  { key: 'ssh', label: t('nav.ssh'), icon: '💻' },
])

const features = computed(() => {
  const list = [...baseFeatures.value]
  if (currentUser.value) {
    list.push({ key: 'admin', label: t('nav.admin'), icon: '⚙️' })
  }
  return list
})

const currentLang = ref(getLocale())
function toggleLang() {
  const next = currentLang.value === 'zh-CN' ? 'en' : 'zh-CN'
  currentLang.value = next
  setLocale(next)
}

function handleLogout() {
  logout()
}

function getUserInitial(username: string): string {
  return username ? username.charAt(0).toUpperCase() : '?'
}
</script>

<template>
  <header class="topbar">
    <button class="brand" @click="emit('home')">
      <span class="brand-dot"></span>
      <span>{{ siteName }}</span>
    </button>

    <nav class="topmenus">
      <button
        v-for="f in features" :key="f.key"
        class="topmenu" :class="{ active: view === f.key }"
        @click="emit('switchView', f.key)"
      >
        <span class="tm-icon">{{ f.icon }}</span>
        {{ f.label }}
      </button>
    </nav>

    <div class="topbar-right">
      <div class="theme-box" ref="popRef">
        <button class="icon-btn" @click.stop="popOpen = !popOpen" :aria-label="t('topbar.themeMode')">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <circle cx="12" cy="12" r="4"/>
            <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/>
          </svg>
        </button>
        <div v-if="popOpen" class="theme-pop">
          <div class="theme-title">{{ t('topbar.themeMode') }}</div>
          <div class="theme-row">
            <button v-for="m in (['light', 'dark', 'auto'] as ThemeMode[])" :key="m"
              class="chip" :class="{ active: themeMode === m }" @click="setThemeMode(m)">
              {{ modeLabels[m] }}
            </button>
          </div>
          <div class="theme-title">{{ t('topbar.themeColor') }}</div>
          <div class="theme-row">
            <button v-for="a in accents" :key="a.key"
              class="swatch" :data-a="a.key" :title="a.label"
              :class="{ active: accent === a.key }" @click="setAccent(a.key)"></button>
          </div>
        </div>
      </div>

      <button class="icon-btn lang-btn" @click="toggleLang" :title="currentLang === 'zh-CN' ? 'Switch to English' : '切换到中文'">
        {{ t('topbar.langSwitch') }}
      </button>

      <!-- 用户菜单 -->
      <div class="user-box" ref="userPopRef" v-if="currentUser">
        <button class="user-btn" @click.stop="userPopOpen = !userPopOpen">
          <span class="user-avatar">{{ getUserInitial(currentUser.username) }}</span>
          <span class="user-name">{{ currentUser.username }}</span>
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="m6 9 6 6 6-6"/>
          </svg>
        </button>
        <div v-if="userPopOpen" class="user-pop">
          <div class="user-info">
            <div class="user-avatar-lg">{{ getUserInitial(currentUser.username) }}</div>
            <div class="user-details">
              <div class="user-name-lg">{{ currentUser.username }}</div>
              <div class="user-email">{{ currentUser.email }}</div>
              <div class="user-role">{{ currentUser.role === 'admin' ? t('topbar.admin') : t('topbar.user') }}</div>
            </div>
          </div>
          <div class="user-menu">
            <button class="menu-item" @click="handleLogout">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
                <polyline points="16 17 21 12 16 7"/>
                <line x1="21" y1="12" x2="9" y2="12"/>
              </svg>
              {{ t('topbar.logout') }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </header>
</template>

<style scoped>
.topbar-right {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
}

.tm-icon { font-size: 14px; }

.lang-btn {
  min-width: 32px;
  height: 32px;
  padding: 0 6px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--panel-2);
  color: var(--fg-2);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  transition: all .15s;
}
.lang-btn:hover {
  border-color: var(--accent);
  color: var(--accent);
  background: var(--accent-soft);
}

/* 用户菜单样式 */
.user-box {
  position: relative;
  margin-left: 8px;
}

.user-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 36px;
  padding: 0 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--panel-2);
  color: var(--fg);
  font-size: 13px;
  cursor: pointer;
  transition: all .15s;
}

.user-btn:hover {
  border-color: var(--accent);
  background: var(--panel);
}

.user-avatar {
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--accent);
  color: white;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 600;
}

.user-name {
  max-width: 100px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-pop {
  position: absolute;
  top: 100%;
  right: 0;
  margin-top: 8px;
  width: 240px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.15);
  z-index: 1000;
  overflow: hidden;
}

.user-info {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 16px;
  border-bottom: 1px solid var(--border);
}

.user-avatar-lg {
  width: 48px;
  height: 48px;
  border-radius: 50%;
  background: var(--accent);
  color: white;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 20px;
  font-weight: 600;
  flex-shrink: 0;
}

.user-details {
  flex: 1;
  min-width: 0;
}

.user-name-lg {
  font-size: 14px;
  font-weight: 600;
  color: var(--fg);
  margin-bottom: 2px;
}

.user-email {
  font-size: 12px;
  color: var(--muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-role {
  font-size: 11px;
  color: var(--accent);
  margin-top: 2px;
}

.user-menu {
  padding: 8px;
}

.menu-item {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 10px 12px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--fg);
  font-size: 14px;
  cursor: pointer;
  transition: background-color 0.15s;
}

.menu-item:hover {
  background: var(--panel-2);
}

.menu-item svg {
  color: var(--muted);
}

@media(max-width: 768px) {
  .tm-icon { display: none; }
  .user-name { display: none; }
}
</style>
