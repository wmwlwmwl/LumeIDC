import { createApp } from 'vue'
import { createPinia } from 'pinia'

import '@/utils/ui/iconify-loader'
import '@styles/core/tailwind.css'
import '@styles/index.scss'
import '@/style.css'

import App from './App.vue'
import router from './router'
import language from '@/locales'
import { setAppRouter } from '@/router/registry'
import { loadSession, installSessionRefresh } from '@/http/session'
import { setOnUnauthorized } from '@/http/index'
import { installSiteConfig } from '@/config/site'
import { bindNProgress } from '@/utils/router'

setAppRouter(router)

async function bootstrap() {
  // 后端暂不可达时也挂载界面，保留明确错误状态；后续焦点恢复/请求可再次刷新会话。
  await loadSession().catch(() => undefined)
  const { useSession } = await import('@/http/session')
  installSiteConfig()
  setOnUnauthorized(() => {
    const s = useSession()
    s.user = null
    const next = router.currentRoute.value.fullPath
    if (router.currentRoute.value.path !== '/login') {
      router.push({ path: '/login', query: { next } })
    }
  })
  installSessionRefresh((wasAuthenticated) => {
    const s = useSession()
    if (wasAuthenticated && !s.user && router.currentRoute.value.path !== '/login') {
      const next = router.currentRoute.value.fullPath
      router.push({ path: '/login', query: { next } })
    }
  })

  const app = createApp(App)
  // 前台刻意不复用后台那份带持久化插件的 pinia（store/index.ts 的 store）：
  // 前台是独立公开站，避免后台的边框模式/圆角/容器宽度等设置串进前台。
  // 需要跨刷新的前台偏好（明暗）单独落 localStorage，见 components/public/usePublicTheme.ts。
  app.use(createPinia())
  app.use(router)
  app.use(language)
  bindNProgress(router)

  // 会话就绪后再挂载，路由守卫依赖 user/csrf
  await router.isReady()
  app.mount('#app')
}

bootstrap()
