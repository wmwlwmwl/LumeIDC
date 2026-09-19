import type { RouteRecordRaw } from 'vue-router'
import type { AppRouteRecord } from '@/types/router'

/**
 * 后台页面路由（作为 AdminApp 布局路由的子路由）。
 *
 * 页面组件继续复用 LumeIDC 现有后台视图，只在外层套用 Art 后台壳。
 * 隐藏路由（新增/编辑/详情）不进入侧栏菜单。
 */
export const adminPageRoutes: RouteRecordRaw[] = [
  { path: '', name: 'admin-home', component: () => import('@views/AdminHome.vue'), meta: { title: '概览看板', icon: 'ri:dashboard-line' } },
  { path: 'orders', name: 'admin-orders', component: () => import('@views/AdminOrders.vue'), meta: { title: '订单管理', icon: 'ri:file-list-3-line' } },
  { path: 'refunds', name: 'admin-refunds', component: () => import('@views/AdminRefunds.vue'), meta: { title: '退款记录', icon: 'ri:refund-2-line' } },

  { path: 'products', name: 'admin-products', component: () => import('@views/AdminProducts.vue'), meta: { title: '产品管理', icon: 'ri:apps-2-line' } },
  { path: 'products/new', name: 'admin-product-new', component: () => import('@views/AdminProductForm.vue'), meta: { title: '新增产品', isHide: true, activePath: '/products' } },
  { path: 'products/:id(\\d+)/edit', name: 'admin-product-edit', component: () => import('@views/AdminProductForm.vue'), meta: { title: '编辑产品', isHide: true, activePath: '/products' } },
  { path: 'types', name: 'admin-types', component: () => import('@views/AdminTypes.vue'), meta: { title: '分类管理', icon: 'ri:price-tag-3-line' } },
  { path: 'services', name: 'admin-services', component: () => import('@views/AdminServices.vue'), meta: { title: '服务实例', icon: 'ri:server-line' } },
  // 服务管理：直接复用用户端服务详情/升降级页，接口由 /admin/services/{id}/... 代管镜像（见后端 admin_service_proxy.go）。
  // meta.admin 标记「代管视图」：页面内跳向前台（/pay、/user/verification）的入口需改道。
  { path: 'services/:id(\\d+)', name: 'admin-service-detail', component: () => import('@views/ServiceDetail.vue'), meta: { title: '服务管理', isHide: true, activePath: '/services', admin: true } },
  { path: 'services/:id(\\d+)/upgrade', name: 'admin-service-upgrade', component: () => import('@views/ServiceUpgrade.vue'), meta: { title: '服务升降级', isHide: true, activePath: '/services', admin: true } },
  { path: 'cancel-requests', name: 'admin-cancel-requests', component: () => import('@views/AdminCancelRequests.vue'), meta: { title: '停用申请', icon: 'ri:chat-delete-line' } },
  { path: 'servers', name: 'admin-servers', component: () => import('@views/AdminServers.vue'), meta: { title: '上游服务器', icon: 'ri:hard-drive-3-line' } },
  { path: 'servers/new', name: 'admin-server-new', component: () => import('@views/AdminServerForm.vue'), meta: { title: '新增服务器', isHide: true, activePath: '/servers' } },
  { path: 'servers/:id(\\d+)/edit', name: 'admin-server-edit', component: () => import('@views/AdminServerForm.vue'), meta: { title: '编辑服务器', isHide: true, activePath: '/servers' } },
  { path: 'servers/:id(\\d+)/catalog', name: 'admin-server-catalog', component: () => import('@views/AdminCatalog.vue'), meta: { title: '目录导入', isHide: true, activePath: '/servers' } },
  { path: 'coupons', name: 'admin-coupons', component: () => import('@views/AdminCoupons.vue'), meta: { title: '优惠折扣', icon: 'ri:coupon-3-line' } },
  { path: 'promotions', name: 'admin-promotions', component: () => import('@views/AdminPromotions.vue'), meta: { title: '营销活动', icon: 'ri:flashlight-line' } },
  { path: 'promotions/new', name: 'admin-promotion-new', component: () => import('@views/AdminPromotionForm.vue'), meta: { title: '新增活动', isHide: true, activePath: '/promotions' } },
  { path: 'promotions/:id(\\d+)/edit', name: 'admin-promotion-edit', component: () => import('@views/AdminPromotionForm.vue'), meta: { title: '编辑活动', isHide: true, activePath: '/promotions' } },

  { path: 'users', name: 'admin-users', component: () => import('@views/AdminUsers.vue'), meta: { title: '用户管理', icon: 'ri:user-3-line' } },
  { path: 'users/:id(\\d+)/edit', name: 'admin-user-edit', component: () => import('@views/AdminUserEdit.vue'), meta: { title: '编辑用户', isHide: true, activePath: '/users' } },
  { path: 'verifications', name: 'admin-verifications', component: () => import('@views/AdminVerifications.vue'), meta: { title: '实名审核', icon: 'ri:shield-check-line' } },
  { path: 'verifications/:id(\\d+)', name: 'admin-verification-detail', component: () => import('@views/AdminVerificationDetail.vue'), meta: { title: '实名详情', isHide: true, activePath: '/verifications' } },

  { path: 'gateway', name: 'admin-gateway', component: () => import('@views/AdminGateways.vue'), meta: { title: '支付网关', icon: 'ri:bank-card-line' } },
  { path: 'plugins', name: 'admin-plugins', component: () => import('@views/AdminPlugins.vue'), meta: { title: '插件管理', icon: 'ri:puzzle-line' } },
  { path: 'site', name: 'admin-site', component: () => import('@views/AdminSite.vue'), meta: { title: '站点设置', icon: 'ri:global-line' } },
  { path: 'settings', redirect: { name: 'admin-notify-settings' } },
  { path: 'settings/notify', name: 'admin-notify-settings', component: () => import('@views/AdminNotifySettings.vue'), meta: { title: '通知设置', icon: 'ri:mail-send-line' } },
  { path: 'email-templates', name: 'admin-email-templates', component: () => import('@views/AdminEmailTemplates.vue'), meta: { title: '邮件模板', icon: 'ri:mail-send-line' } },
  { path: 'sms-templates', name: 'admin-sms-templates', component: () => import('@views/AdminSMSTemplates.vue'), meta: { title: '短信模板', icon: 'ri:message-2-line' } },
  { path: 'settings/lifecycle', name: 'admin-lifecycle-settings', component: () => import('@views/AdminLifecycleSettings.vue'), meta: { title: '服务生命周期', icon: 'ri:hourglass-line' } },
  { path: 'settings/auth', name: 'admin-auth-settings', component: () => import('@views/AdminAuthSettings.vue'), meta: { title: '登录与验证', icon: 'ri:shield-keyhole-line' } },
  { path: 'settings/identity', name: 'admin-identity-settings', component: () => import('@views/AdminIdentitySettings.vue'), meta: { title: '实名认证', icon: 'ri:id-card-line' } },
  { path: 'logs', name: 'admin-logs', component: () => import('@views/AdminLogs.vue'), meta: { title: '审计日志', icon: 'ri:file-text-line' } },
  { path: 'totp', name: 'admin-totp', component: () => import('@views/AdminTotp.vue'), meta: { title: '安全验证', icon: 'ri:fingerprint-line' } },
  { path: 'password', name: 'admin-password', component: () => import('@views/AdminPassword.vue'), meta: { title: '账户设置', icon: 'ri:user-settings-line' } },
  { path: 'update', name: 'admin-update', component: () => import('@views/AdminUpdate.vue'), meta: { title: '系统更新', icon: 'ri:refresh-line' } },
  // 插件页：动态壳按 :name 从 web/src/plugins/registry.ts 取插件组件渲染。
  { path: 'plugin/:name', name: 'admin-plugin', component: () => import('@/plugins/PluginShell.vue'), meta: { title: '插件', isHide: true } },
]

/** 叶子菜单项需要非空 component 才会被 Art 侧栏渲染；实际跳转只用到 path。 */
const leaf = (
  path: string,
  name: string,
  title: string,
  icon: string,
): AppRouteRecord => ({
  path,
  name,
  component: 'page',
  meta: { title, icon },
})

/**
 * 后台侧栏菜单分组。
 * 使用 LumeIDC 业务分组：运营 / 业务 / 用户与内容 / 系统。
 */
export const adminMenu: AppRouteRecord[] = [
  {
    path: '/group-ops',
    name: 'group-ops',
    meta: { title: '运营概览', icon: 'ri:dashboard-2-line' },
    children: [
      leaf('/', 'admin-home', '概览看板', 'ri:dashboard-line'),
      leaf('/orders', 'admin-orders', '订单管理', 'ri:file-list-3-line'),
      leaf('/refunds', 'admin-refunds', '退款记录', 'ri:refund-2-line'),
    ],
  },
  {
    path: '/group-business',
    name: 'group-business',
    meta: { title: '业务管理', icon: 'ri:apps-2-line' },
    children: [
      leaf('/products', 'admin-products', '产品管理', 'ri:apps-2-line'),
      leaf('/types', 'admin-types', '分类管理', 'ri:price-tag-3-line'),
      leaf('/services', 'admin-services', '服务实例', 'ri:server-line'),
      leaf('/cancel-requests', 'admin-cancel-requests', '停用申请', 'ri:chat-delete-line'),
      leaf('/servers', 'admin-servers', '上游服务器', 'ri:hard-drive-3-line'),
      leaf('/coupons', 'admin-coupons', '优惠折扣', 'ri:coupon-3-line'),
      leaf('/promotions', 'admin-promotions', '营销活动', 'ri:flashlight-line'),
    ],
  },
  {
    path: '/group-users',
    name: 'group-users',
    meta: { title: '用户与内容', icon: 'ri:group-line' },
    children: [
      leaf('/users', 'admin-users', '用户管理', 'ri:user-3-line'),
      leaf('/verifications', 'admin-verifications', '实名审核', 'ri:shield-check-line'),
    ],
  },
  {
    path: '/group-settings',
    name: 'group-settings',
    meta: { title: '设置', icon: 'ri:settings-3-line' },
    children: [
      leaf('/site', 'admin-site', '站点设置', 'ri:global-line'),
      leaf('/settings/notify', 'admin-notify-settings', '通知设置', 'ri:mail-send-line'),
      leaf('/settings/lifecycle', 'admin-lifecycle-settings', '服务生命周期', 'ri:hourglass-line'),
      leaf('/email-templates', 'admin-email-templates', '邮件模板', 'ri:mail-send-line'),
      leaf('/sms-templates', 'admin-sms-templates', '短信模板', 'ri:message-2-line'),
      leaf('/settings/auth', 'admin-auth-settings', '登录与验证', 'ri:shield-keyhole-line'),
      leaf('/settings/identity', 'admin-identity-settings', '实名认证', 'ri:id-card-line'),
    ],
  },
  {
    path: '/group-system',
    name: 'group-system',
    meta: { title: '系统设置', icon: 'ri:tools-line' },
    children: [
      leaf('/gateway', 'admin-gateway', '支付网关', 'ri:bank-card-line'),
      leaf('/plugins', 'admin-plugins', '插件管理', 'ri:puzzle-line'),
      leaf('/logs', 'admin-logs', '审计日志', 'ri:file-text-line'),
      leaf('/totp', 'admin-totp', '安全验证', 'ri:fingerprint-line'),
      leaf('/password', 'admin-password', '账户设置', 'ri:user-settings-line'),
      leaf('/update', 'admin-update', '系统更新', 'ri:refresh-line'),
    ],
  },
]

/** 后端插件清单条目（GET /admin/plugins 返回）。 */
export interface PluginMenuInfo {
  name: string
  title: string
  enabled?: boolean
  hasAdminPage?: boolean
  menuTitle?: string
  menuIcon?: string
}

/**
 * 把有后台页的启用插件追加为「插件」菜单分组；无插件时原样返回核心菜单。
 * 插件页面路由统一为 /plugin/{name}（PluginShell 动态渲染）。
 */
export function mergePluginMenus(base: AppRouteRecord[], plugins: PluginMenuInfo[]): AppRouteRecord[] {
  const items = plugins
    .filter((p) => p.hasAdminPage && p.enabled !== false)
    .map((p) =>
      leaf(`/plugin/${p.name}`, `admin-plugin-${p.name}`, p.menuTitle || p.title || p.name, p.menuIcon || 'ri:puzzle-line'),
    )
  if (items.length === 0) return base
  return [
    ...base,
    {
      path: '/group-plugins',
      name: 'group-plugins',
      meta: { title: '插件', icon: 'ri:puzzle-line' },
      children: items,
    },
  ]
}
