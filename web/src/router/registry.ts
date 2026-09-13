import type { Router } from 'vue-router'

/**
 * 应用级路由实例注册表。
 *
 * 前台（history 路由）和后台（hash 路由）是两个独立的 SPA 入口，同一时刻只会
 * 运行其中一个。Art 生态的 store/工具需要在运行时拿到“当前生效”的路由实例，
 * 因此这里用一个可注册的单例持有者，而不是在模块顶层硬编码某个 router。
 */
let routerInstance: Router | null = null

export function setAppRouter(router: Router): void {
  routerInstance = router
}

export function getAppRouter(): Router {
  if (!routerInstance) {
    throw new Error('[router] 应用路由尚未注册，请先调用 setAppRouter()')
  }
  return routerInstance
}

/** 后台首页路径（菜单首项回退用）。 */
export const HOME_PAGE_PATH = ''
