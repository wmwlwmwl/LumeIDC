import { createApp } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'

import '@/utils/ui/iconify-loader'
import '@styles/core/tailwind.css'
import '@styles/index.scss'
import '../style.css'

import AdminRoot from './AdminRoot.vue'
import AdminApp from './AdminApp.vue'
import { adminPageRoutes, adminMenu } from './menu'
import { store } from '@/store'
import { setupGlobDirectives } from '@/directives'
import language from '@/locales'
import { loadSession, installSessionRefresh, useSession, setAdminApp, clearCurrentUser } from '@/http/session'
import { setOnUnauthorized, setApiBase } from '@/http/index'
import { setAppRouter } from '@/router/registry'
import { useMenuStore } from '@/store/modules/menu'
import { useWorktabStore } from '@/store/modules/worktab'
import { installSiteConfig } from '@/config/site'
import { bindNProgress } from '@/utils/router'

// 后台入口：会话身份取管理员通道（与前台用户通道并存，互不顶替）。
// 必须在首次路由守卫前置位。
setAdminApp(true)

const router = createRouter({
  // hash 路由：URL 的 # 部分不发给服务器，与自定义后台路径/改写中间件解耦。
  history: createWebHashHistory(),
  routes: [
    {
      path: '/login',
      name: 'admin-login',
      component: () => import('../views/AdminLogin.vue'),
      meta: { title: '登录', isHideTab: true },
    },
    {
      // 服务 VNC 控制台：满屏裸页，挂顶层不进后台框架（复用前台 ServiceConsole）。
      // 接口经 /admin/services/{id}/* 镜像路由代管（见 internal/handler/admin_service_proxy.go）。
      path: '/services/:id(\\d+)/console',
      name: 'admin-service-console',
      component: () => import('../views/ServiceConsole.vue'),
      meta: { title: 'VNC 控制台', isHideTab: true },
    },
    {
      path: '/',
      component: AdminApp,
      children: adminPageRoutes,
    },
    { path: '/403', name: 'Exception403', component: () => import('../views/Exception403.vue'), meta: { title: '403', isHideTab: true } },
    { path: '/500', name: 'Exception500', component: () => import('../views/Exception500.vue'), meta: { title: '500', isHideTab: true } },
    { path: '/:pathMatch(.*)*', name: 'Exception404', component: () => import('../views/Exception404.vue'), meta: { title: '404', isHideTab: true } },
  ],
})

// Art 生态的 store / 工具通过注册表获取当前生效路由。
setAppRouter(router)

router.beforeEach((to) => {
  const session = useSession()
  const isAdmin = !!session.adminUser
  if (to.path !== '/login' && !isAdmin) {
    return { path: '/login', query: to.fullPath !== '/' ? { next: to.fullPath } : undefined }
  }
  if (to.path === '/login' && isAdmin) {
    const next = to.query.next
    return { path: typeof next === 'string' ? next : '/' }
  }
  return true
})

router.afterEach((to) => {
  if (to.meta.isHideTab || to.path === '/login') return
  try {
    const worktab = useWorktabStore()
    worktab.openTab({
      title: (to.meta.title as string) || String(to.name || ''),
      path: to.path,
      name: to.name as string,
      keepAlive: !!to.meta.keepAlive,
      params: to.params,
      query: to.query,
    })
  } catch {
    // Pinia 尚未就绪时忽略（首次导航一定在 app.use(store) 之后）。
  }
})

async function bootstrap() {
  // 后端暂不可达时仍显示登录壳；后续焦点恢复会重新取 Cookie/CSRF。
  await loadSession().catch(() => undefined)

  const session = useSession()

  // 后台接口都挂在当前后台路径下（/admin/* 被中间件屏蔽以防泄漏自定义路径），
  // 因此把 API 基址设为 /session 下发的 admin.path。
  setApiBase((session.adminPath || '').replace(/\/$/, ''))

  installSiteConfig()

  setOnUnauthorized(() => {
    clearCurrentUser()
    if (router.currentRoute.value.path !== '/login') {
      const next = router.currentRoute.value.fullPath
      router.replace({ path: '/login', query: { next } })
    }
  })
  installSessionRefresh((wasAuthenticated) => {
    setApiBase((session.adminPath || '').replace(/\/$/, ''))
    if (wasAuthenticated && !session.adminUser && router.currentRoute.value.path !== '/login') {
      const next = router.currentRoute.value.fullPath
      router.replace({ path: '/login', query: { next } })
    }
  })

  const app = createApp(AdminRoot)
  app.use(store)
  app.use(router)
  app.use(language)
  bindNProgress(router)
  // Art 组件依赖 v-ripple 等全局指令（如图表卡片、数据列表卡片）。
  setupGlobDirectives(app)

  // Art 主题初始化（主色/圆角/明暗/盒模型）已移至 AdminRoot 的 onBeforeMount，
  // 与 Art 的 App.vue 保持一致。

  // 前端模式：后台菜单由本地配置注入 Art 侧栏。
  useMenuStore().setMenuList(adminMenu)

  await router.isReady()
  app.mount('#admin-app')
}

bootstrap()
