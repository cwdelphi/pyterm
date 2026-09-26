import { createI18n } from 'vue-i18n'
import zhCN from './zh-CN.json'
import en from './en.json'

const savedLang = localStorage.getItem('lang') || 'zh-CN'

const i18n = createI18n({
  legacy: false,
  locale: savedLang,
  fallbackLocale: 'zh-CN',
  messages: { 'zh-CN': zhCN, en }
})

export function setLocale(lang: string) {
  ;(i18n.global.locale as any).value = lang
  localStorage.setItem('lang', lang)
}

export function getLocale(): string {
  return (i18n.global.locale as any).value
}

export default i18n
