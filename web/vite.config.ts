import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { resolve } from 'path'

// 仅生产构建剥离 console/debugger；dev 保留完整日志能力
export default defineConfig(({ mode }) => {
  const isProd = mode === 'production'
  return {
    plugins: [vue()],
    base: '/',
    server: {
      host: true,
      port: 5173,
      proxy: {
        '/api': 'http://127.0.0.1:8000'
      }
    },
    // 注意：本项目的 `vite` bin 指向 rolldown-vite（Rust/oxc），
    // 它不认 `esbuild.drop`，必须用 terser 才能在最终 bundle 上剥离 console。
    build: {
      outDir: 'dist',
      chunkSizeWarningLimit: 500,
      target: 'esnext',
      reportCompressedSize: false,
      minify: isProd ? 'terser' : false,
      terserOptions: {
        compress: {
          drop_debugger: true,
          passes: 2,
          // 只剥 log/debug/info/trace：热路径全是 console.log；
          // warn/error 留着，非热路径且是线上排障的最后手段。
          pure_funcs: ['console.log', 'console.debug', 'console.info', 'console.trace']
        }
      },
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
  }
})
