import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Plugin } from 'vite'
import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import AutoImport from 'unplugin-auto-import/vite'
import Components from 'unplugin-vue-components/vite'
import ElementPlus from 'unplugin-element-plus/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'

// Go 后端地址（默认 :8080，与 config.yaml 的 listen 默认值一致）。
// 本地若改了监听端口，用环境变量覆盖：LUME_DEV_API=http://localhost:18090 npm run dev
const GO_TARGET = process.env.LUME_DEV_API || 'http://localhost:8080'
// 管理后台 SPA 的文档挂载路径（生产由 middleware.AdminPath 支持自定义路径；
// 开发期固定 /admin，后台 SPA 统一走 hash 路由，与自定义路径解耦）。
const ADMIN_BASE = '/admin'

function resolvePath(p: string): string {
  return path.resolve(import.meta.dirname, p)
}

// 开发期：让 Vite 在 /admin 下提供 admin.html（后台入口的 SPA fallback）。
function adminEntry(): Plugin {
  return {
    name: 'lume-admin-entry',
    configureServer(server) {
      return () => {
        server.middlewares.use(async (req, res, next) => {
          const url = (req.url || '').split('?')[0]
          if (url !== ADMIN_BASE && url !== ADMIN_BASE + '/') return next()
          if (!String(req.headers.accept || '').includes('text/html')) return next()
          try {
            const file = fileURLToPath(new URL('./admin.html', import.meta.url))
            let html = await fs.promises.readFile(file, 'utf-8')
            html = await server.transformIndexHtml(req.url || ADMIN_BASE, html, req)
            res.statusCode = 200
            res.setHeader('Content-Type', 'text/html')
            res.end(html)
          } catch (err) {
            next(err as Error)
          }
        })
      }
    },
  }
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd())
  return {
    base: env.VITE_BASE_URL || '/',
    define: {
      __APP_VERSION__: JSON.stringify(env.VITE_VERSION || '0.0.0'),
    },
    plugins: [
      vue(),
      tailwindcss(),
      AutoImport({
        imports: ['vue', 'vue-router', 'pinia', '@vueuse/core'],
        dts: 'src/types/import/auto-imports.d.ts',
        resolvers: [ElementPlusResolver()],
      }),
      Components({
        dts: 'src/types/import/components.d.ts',
        resolvers: [ElementPlusResolver()],
      }),
      ElementPlus({ useSource: true }),
      adminEntry(),
    ],
    resolve: {
      alias: {
        '@': resolvePath('src'),
        '@views': resolvePath('src/views'),
        '@imgs': resolvePath('src/assets/images'),
        '@icons': resolvePath('src/assets/icons'),
        '@utils': resolvePath('src/utils'),
        '@stores': resolvePath('src/store'),
        '@plugins': resolvePath('src/plugins'),
        '@styles': resolvePath('src/assets/styles'),
      },
    },
    build: {
      outDir: '../internal/handler/webui/dist',
      assetsDir: 'app',
      chunkSizeWarningLimit: 2000,
      rolldownOptions: {
        input: {
          store: fileURLToPath(new URL('./index.html', import.meta.url)),
          admin: fileURLToPath(new URL('./admin.html', import.meta.url)),
        },
      },
    },
    server: {
      port: 5173,
      // 开发期:/__api/* 转发到 Go（摘掉前缀,路径与生产保持一致）。
      // 生产不产生 /__api，SPA 直接请求同源路径。会话 cookie 同源共享。
      proxy: {
        '/__api': {
          target: GO_TARGET,
          changeOrigin: true,
          ws: true, // VNC 隧道 (/__api/*/services/{id}/vnc-ws) 走 WebSocket 升级
          rewrite: (p) => p.replace(/^\/__api/, ''),
          // 标记本地开发代理：Go 据此在 /session 下发真实后台路径（生产不下发，
          // 见 middleware.DevProxyHeader），开发期自定义路径的后台才能继续工作。
          headers: { 'X-Lume-Dev-Proxy': '1' },
        },
      },
    },
    css: {
      preprocessorOptions: {
        scss: {
          additionalData: `
            @use "@styles/core/el-light.scss" as *;
            @use "@styles/core/mixin.scss" as *;
          `,
        },
      },
      postcss: {
        plugins: [
          {
            postcssPlugin: 'internal:charset-removal',
            AtRule: {
              charset: (atRule) => {
                if (atRule.name === 'charset') {
                  atRule.remove()
                }
              },
            },
          },
        ],
      },
    },
  }
})
