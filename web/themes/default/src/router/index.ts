import { createRouter, createWebHistory } from 'vue-router'
import { useSession } from '@/http/session'
import { setPageTitle } from '@/utils/router'

const router = createRouter({
  history: createWebHistory('/'),
  // 前台滚动容器是窗口：不配置 scrollBehavior 时浏览器会沿用上一页的滚动位置，
  // 导致「切到新页面还停在原来的位置」。新页面回到顶部，浏览器前进/后退恢复原位置。
  scrollBehavior(_to, _from, savedPosition) {
    return savedPosition ?? { top: 0 }
  },
  routes: [
    { path: '/install', name: 'install', component: () => import('../views/Install.vue'), meta: { bare: true, title: '安装向导' } },
    { path: '/', name: 'home', component: () => import('../views/Home.vue'), meta: { title: '首页' } },
    { path: '/cart', name: 'cart', component: () => import('../views/Catalog.vue'), meta: { title: '产品中心' } },
    { path: '/promotions', name: 'promotions', component: () => import('../views/Promotions.vue'), meta: { title: '营销活动' } },
    { path: '/promotion/:id(\\d+)', name: 'promotion', component: () => import('../views/Promotion.vue'), meta: { title: '活动' } },
    { path: '/buy/:id(\\d+)', name: 'buy', component: () => import('../views/Buy.vue'), meta: { title: '购买' } },
    { path: '/pay/:id(\\d+)', name: 'pay', component: () => import('../views/Pay.vue'), meta: { title: '支付' } },
    // 公告中心页面由 announcement 插件提供（web/src/plugins/announcement/）
    { path: '/notices', name: 'notices', component: () => import('@/plugins/announcement/Notices.vue'), meta: { title: '公告' } },
    { path: '/notices/:id(\\d+)', name: 'notice-detail', component: () => import('@/plugins/announcement/NoticeDetail.vue'), meta: { title: '公告详情' } },
    { path: '/login', name: 'login', component: () => import('../views/Login.vue'), meta: { guest: true, authLayout: true, title: '登录' } },
    { path: '/register', name: 'register', component: () => import('../views/Register.vue'), meta: { guest: true, authLayout: true, title: '注册' } },
    { path: '/forgot', name: 'forgot', component: () => import('../views/Forgot.vue'), meta: { guest: true, authLayout: true, title: '找回密码' } },
    {
      path: '/user',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [
        { path: '', name: 'user-home', component: () => import('../views/UserHome.vue'), meta: { title: '账户概览' } },
      ],
    },
    {
      path: '/services',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [
        { path: '', name: 'services', component: () => import('../views/Services.vue'), meta: { title: '我的服务' } },
        { path: ':id(\\d+)', name: 'service-detail', component: () => import('@views/ServiceDetail.vue'), meta: { title: '服务详情' } },
        { path: ':id(\\d+)/upgrade', name: 'service-upgrade', component: () => import('@views/ServiceUpgrade.vue'), meta: { title: '升降级' } },
      ],
    },
    // VNC 控制台是满屏裸页：必须脱离 /services 的 UserShell（该外壳固定带侧栏，
    // 子路由无法去侧栏），故与上面同路径但定义为顶级路由——不能同时保留两条。
    {
      path: '/services/:id(\\d+)/console',
      name: 'service-console',
      component: () => import('@views/ServiceConsole.vue'),
      meta: { auth: true, bare: true, title: 'VNC 控制台' },
    },
    {
      path: '/user/recharge',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [{ path: '', name: 'recharge', component: () => import('../views/Recharge.vue'), meta: { title: '账户充值' } }],
    },
    {
      path: '/user/invoices',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [{ path: '', name: 'invoices', component: () => import('../views/Invoices.vue'), meta: { title: '财务记录' } }],
    },
    {
      path: '/user/promotion-coupons',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [{ path: '', name: 'promotion-coupons', component: () => import('../views/UserPromotionCoupons.vue'), meta: { title: '活动优惠券' } }],
    },
    {
      path: '/notifications',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [{ path: '', name: 'notifications', component: () => import('../views/Notifications.vue'), meta: { title: '消息中心' } }],
    },
    {
      // 工单页面由 tickets 插件提供（web/src/plugins/tickets/）
      path: '/tickets',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [
        { path: '', name: 'tickets', component: () => import('@/plugins/tickets/Tickets.vue'), meta: { title: '工单支持' } },
        { path: ':id(\\d+)', name: 'ticket-detail', component: () => import('@/plugins/tickets/TicketDetail.vue'), meta: { title: '工单详情' } },
      ],
    },
    {
      // 前台插件页槽位：ClientShell 按 :name 从 web/src/plugins/registry.ts 的 clientRegistry 渲染。
      path: '/plugin',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [
        { path: ':name', name: 'client-plugin', component: () => import('@/plugins/ClientShell.vue'), meta: { title: '插件' } },
      ],
    },
    {
      path: '/user/password',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [{ path: '', name: 'password', component: () => import('../views/Password.vue'), meta: { title: '安全设置' } }],
    },
    {
      path: '/user/verification',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [{ path: '', name: 'verification', component: () => import('../views/Verification.vue'), meta: { title: '实名认证' } }],
    },
    {
      path: '/user/profile',
      component: () => import('../views/UserShell.vue'),
      meta: { auth: true },
      children: [{ path: '', name: 'profile', component: () => import('../views/Profile.vue'), meta: { title: '资料与手机号' } }],
    },
    { path: '/403', name: 'error-403', component: () => import('@views/Exception403.vue'), meta: { title: '无访问权限' } },
    { path: '/404', name: 'error-404', component: () => import('@views/Exception404.vue'), meta: { title: '页面不存在' } },
    { path: '/500', name: 'error-500', component: () => import('@views/Exception500.vue'), meta: { title: '服务异常' } },
    { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('@views/Exception404.vue'), meta: { title: '页面不存在' } },
  ],
})

router.beforeEach((to) => {
  const session = useSession()
  if (to.meta.auth && !session.user) {
    return { path: '/login', query: { next: to.fullPath } }
  }
  if (to.meta.guest && session.user) {
    return { path: '/' }
  }
  return true
})

// 浏览器标签标题：meta.title + /session 下发的站点名（bootstrap 已 await loadSession，首次导航即真名）
router.afterEach((to) => setPageTitle(to))

export default router
