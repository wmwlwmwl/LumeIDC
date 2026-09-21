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

// 前台模板入口：扫描 themes/*/index.html，每套模板是独立的 SPA 入口。
// key 形如 themes/default/index，产物落到 dist/themes/<name>/index.html。
function themeEntries(): Record<string, string> {
  const dir = resolvePath('themes')
  const entries: Record<string, string> = {}
  for (const name of fs.readdirSync(dir)) {
    const html = path.join(dir, name, 'index.html')
    if (fs.existsSync(html)) entries[`themes/${name}/index`] = html
  }
  return entries
}

// 构建后把模板元信息（theme.json/theme.png）拷到 dist/themes/<name>/，
// Go 嵌入后由后台「前台模板」页列出与预览。
function copyThemeMeta(): Plugin {
  return {
    name: 'lume-theme-meta',
    apply: 'build',
    closeBundle() {
      const dir = resolvePath('themes')
      const distThemes = resolvePath('../internal/handler/webui/dist/themes')
      for (const name of fs.readdirSync(dir)) {
        const srcDir = path.join(dir, name)
        if (!fs.statSync(srcDir).isDirectory()) continue
        const destDir = path.join(distThemes, name)
        fs.mkdirSync(destDir, { recursive: true })
        for (const file of ['theme.json', 'theme.png']) {
          const f = path.join(srcDir, file)
          if (fs.existsSync(f)) fs.copyFileSync(f, path.join(destDir, file))
        }
      }
    },
  }
}

// 开发期：前台导航（非 /admin、/__api）回退到默认模板入口。
// 直接访问 /themes/<name>/index.html 可预览其他模板，不拦截。
// 注意：必须挂在 Vite 内部中间件之前（pre），否则根 index.html 缺失时
// Vite 的 SPA fallback 会先 404。
function themeEntry(): Plugin {
  return {
    name: 'lume-theme-entry',
    configureServer(server) {
      server.middlewares.use(async (req, res, next) => {
        const url = (req.url || '').split('?')[0]
        if (url === ADMIN_BASE || url.startsWith(ADMIN_BASE + '/')) return next()
        if (url.startsWith('/__api') || url.startsWith('/themes/')) return next()
        if (!String(req.headers.accept || '').includes('text/html')) return next()
        try {
          const file = fileURLToPath(new URL('./themes/default/index.html', import.meta.url))
          let html = await fs.promises.readFile(file, 'utf-8')
          html = await server.transformIndexHtml(req.url || '/', html, req)
          res.statusCode = 200
          res.setHeader('Content-Type', 'text/html')
          res.end(html)
        } catch (err) {
          next(err as Error)
        }
      })
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
      themeEntry(),
      copyThemeMeta(),
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
          admin: fileURLToPath(new URL('./admin.html', import.meta.url)),
          ...themeEntries(),
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
