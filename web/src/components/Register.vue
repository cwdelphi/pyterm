<script setup lang="ts">
import { ref, defineEmits } from 'vue'
import { useI18n } from 'vue-i18n'
import { reloadLogLevel } from '../utils/logger'

const { t } = useI18n()
const emit = defineEmits<{
  (e: 'register', token: string, user: any): void
  (e: 'switch-to-login'): void
}>()

const username = ref('')
const email = ref('')
const password = ref('')
const confirmPassword = ref('')
const loading = ref(false)
const error = ref('')

async function handleRegister() {
  if (!username.value || !email.value || !password.value || !confirmPassword.value) {
    error.value = t('auth.allFieldsRequired')
    return
  }

  if (password.value !== confirmPassword.value) {
    error.value = t('auth.passwordMismatch')
    return
  }

  if (password.value.length < 6) {
    error.value = t('auth.passwordMinLength')
    return
  }

  loading.value = true
  error.value = ''

  try {
    const response = await fetch('/api/auth/register', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        username: username.value,
        email: email.value,
        password: password.value
      })
    })

    const data = await response.json()

    if (!response.ok) {
      throw new Error(data.detail || t('auth.registerFailed'))
    }

    // 自动登录
    const loginResponse = await fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        username: username.value,
        password: password.value
      })
    })

    const loginData = await loginResponse.json()

    if (!loginResponse.ok) {
      throw new Error(loginData.detail || t('auth.autoLoginFailed'))
    }

    // 存储token
    localStorage.setItem('token', loginData.token)
    localStorage.setItem('user', JSON.stringify(loginData.user))
    reloadLogLevel()  // 日志等级按账户隔离

    emit('register', loginData.token, loginData.user)
  } catch (e: any) {
    error.value = e.message || t('auth.registerFailed')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="register-container">
    <div class="register-card">
      <div class="register-header">
        <h1>{{ t('site.name') }}</h1>
        <p>{{ t('auth.registerTitle') }}</p>
      </div>

      <form @submit.prevent="handleRegister" class="register-form">
        <div class="form-group">
          <label for="username">{{ t('auth.username') }}</label>
          <input
            id="username"
            v-model="username"
            type="text"
            :placeholder="t('auth.usernameMinLength')"
            :disabled="loading"
            autocomplete="username"
          />
        </div>

        <div class="form-group">
          <label for="email">{{ t('auth.email') || 'Email' }}</label>
          <input
            id="email"
            v-model="email"
            type="email"
            :placeholder="t('auth.emailPlaceholder')"
            :disabled="loading"
            autocomplete="email"
          />
        </div>

        <div class="form-group">
          <label for="password">{{ t('auth.password') }}</label>
          <input
            id="password"
            v-model="password"
            type="password"
            :placeholder="t('auth.passwordMinLength6')"
            :disabled="loading"
            autocomplete="new-password"
          />
        </div>

        <div class="form-group">
          <label for="confirmPassword">{{ t('auth.confirmPassword') }}</label>
          <input
            id="confirmPassword"
            v-model="confirmPassword"
            type="password"
            :placeholder="t('auth.confirmPasswordPlaceholder')"
            :disabled="loading"
            autocomplete="new-password"
          />
        </div>

        <div v-if="error" class="error-message">
          {{ error }}
        </div>

        <button type="submit" class="register-button" :disabled="loading">
          {{ loading ? t('auth.registering') : t('auth.register') }}
        </button>
      </form>

      <div class="register-footer">
        <p>{{ t('auth.hasAccount') }} <a href="#" @click.prevent="emit('switch-to-login')">{{ t('auth.loginNow') }}</a></p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.register-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background: var(--bg);
}

.register-card {
  width: 100%;
  max-width: 400px;
  padding: 40px;
  background: var(--card-bg);
  border-radius: 12px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.1);
}

.register-header {
  text-align: center;
  margin-bottom: 32px;
}

.register-header h1 {
  font-size: 28px;
  font-weight: 600;
  color: var(--text);
  margin: 0 0 8px 0;
}

.register-header p {
  font-size: 14px;
  color: var(--text-secondary);
  margin: 0;
}

.register-form {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.form-group label {
  font-size: 14px;
  font-weight: 500;
  color: var(--text);
}

.form-group input {
  padding: 12px 16px;
  border: 1px solid var(--border);
  border-radius: 8px;
  font-size: 14px;
  background: var(--input-bg);
  color: var(--text);
  transition: border-color 0.2s, box-shadow 0.2s;
}

.form-group input:focus {
  outline: none;
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accent-light);
}

.form-group input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.error-message {
  padding: 12px;
  background: var(--error-bg);
  color: var(--error-text);
  border-radius: 8px;
  font-size: 14px;
  text-align: center;
}

.register-button {
  padding: 12px 24px;
  background: var(--accent);
  color: white;
  border: none;
  border-radius: 8px;
  font-size: 16px;
  font-weight: 500;
  cursor: pointer;
  transition: background-color 0.2s, opacity 0.2s;
}

.register-button:hover:not(:disabled) {
  background: var(--accent-hover);
}

.register-button:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.register-footer {
  margin-top: 24px;
  text-align: center;
}

.register-footer p {
  font-size: 14px;
  color: var(--text-secondary);
  margin: 0;
}

.register-footer a {
  color: var(--accent);
  text-decoration: none;
  font-weight: 500;
}

.register-footer a:hover {
  text-decoration: underline;
}
</style>
