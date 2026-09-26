<script setup lang="ts">
import { ref, onMounted, onErrorCaptured, provide } from 'vue'
import { useI18n } from 'vue-i18n'
import { isDark } from './theme'
import { api } from './api'
import TopBar from './components/TopBar.vue'
import DocManager from './components/DocManager.vue'
import LinkManager from './components/LinkManager.vue'
import SshManager from './components/SshManager.vue'
import AdminPanel from './components/AdminPanel.vue'
import Toast from './components/Toast.vue'
import Login from './components/Login.vue'
import Register from './components/Register.vue'

const { t } = useI18n()
const siteName = ref(t('site.name'))
const view = ref<'docs' | 'links' | 'ssh' | 'admin'>('ssh')
const toastRef = ref<InstanceType<typeof Toast>>()

// 认证状态
const isAuthenticated = ref(false)
const currentUser = ref<any>(null)
const authView = ref<'login' | 'register'>('login')

provide('toast', {
  success: (t: string) => toastRef.value?.success(t),
  error: (t: string) => toastRef.value?.error(t),
  info: (t: string) => toastRef.value?.info(t),
  warn: (t: string) => toastRef.value?.warn(t),
})

provide('auth', {
  user: currentUser,
  isAuthenticated,
  logout: () => {
    localStorage.removeItem('token')
    localStorage.removeItem('user')
    currentUser.value = null
    isAuthenticated.value = false
    authView.value = 'login'
  }
})

onMounted(async () => {
  const token = localStorage.getItem('token')
  const userStr = localStorage.getItem('user')
  
  if (token && userStr) {
    try {
      const user = JSON.parse(userStr)
      currentUser.value = user
      isAuthenticated.value = true

      // 验证token有效性
      try {
        const me = await api.authMe()
        currentUser.value = me
      } catch {
        // token无效，清除并跳转登录
        localStorage.removeItem('token')
        localStorage.removeItem('user')
        currentUser.value = null
        isAuthenticated.value = false
        authView.value = 'login'
      }
    } catch {
      localStorage.removeItem('token')
      localStorage.removeItem('user')
    }
  }

  if (isAuthenticated.value) {
    try {
      const cfg = await api.config()
      siteName.value = cfg.site_name || t('site.name')
    } catch {}
  }

  window.addEventListener('error', (e) => {
    try { api.frontendLog('error', `Uncaught: ${e.message} at ${e.filename}:${e.lineno}:${e.colno}`) } catch {}
  })
  window.addEventListener('unhandledrejection', (e) => {
    try { api.frontendLog('error', `UnhandledRejection: ${e.reason}`) } catch {}
  })
})

onErrorCaptured((err) => {
  try { api.frontendLog('error', `Captured: ${err.message}`) } catch {}
})

function switchView(v: string) {
  if (v === 'docs' || v === 'links' || v === 'ssh' || v === 'admin') view.value = v as any
}

function handleLogin(token: string, user: any) {
  currentUser.value = user
  isAuthenticated.value = true
  toastRef.value?.success(t('auth.loginSuccess'))
}

function handleRegister(token: string, user: any) {
  currentUser.value = user
  isAuthenticated.value = true
  toastRef.value?.success(t('auth.registerSuccess'))
}
</script>

<template>
  <Toast ref="toastRef" />

  <template v-if="!isAuthenticated">
    <Login
      v-if="authView === 'login'"
      @login="handleLogin"
      @switch-to-register="authView = 'register'"
    />
    <Register
      v-else
      @register="handleRegister"
      @switch-to-login="authView = 'login'"
    />
  </template>

  <template v-else>
    <TopBar
      :site-name="siteName"
      :view="view"
      @switch-view="switchView"
      @home="view = 'docs'"
    />

    <div class="layout">
      <SshManager v-show="view === 'ssh'" :view="view" />
      <DocManager v-if="view === 'docs'" :dark="isDark" />
      <LinkManager v-if="view === 'links'" />
      <AdminPanel v-show="view === 'admin'" />
    </div>
  </template>
</template>
