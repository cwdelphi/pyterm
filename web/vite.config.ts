import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

export default defineConfig({
  plugins: [vue()],
  base: '/',
  server: {
    host: true,
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8000'
    }
  },
  build: {
    outDir: 'dist',
    chunkSizeWarningLimit: 500,
    target: 'esnext',
    reportCompressedSize: false,
    rollupOptions: {
      input: {
        main: resolve(__dirname, 'index.html')
      },
      output: {
        manualChunks(id: string) {
          if (id.includes('node_modules/vue')) return 'vue-vendor'
          if (id.includes('node_modules/@codemirror') || id.includes('node_modules/codemirror')) return 'codemirror'
          if (id.includes('node_modules/@novnc')) return 'novnc'
        }
      }
    }
  },
  optimizeDeps: {
    include: ['@novnc/novnc']
  }
})
