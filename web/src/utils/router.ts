/**
 * 路由工具函数
 *
 * 提供路由相关的工具函数
 *
 * @module utils/router
 */
import { RouteLocationNormalized, RouteRecordRaw, Router } from 'vue-router'
import AppConfig from '@/config'
import NProgress from 'nprogress'
import 'nprogress/nprogress.css'
import i18n, { $t } from '@/locales'
import { useSettingStore } from '@/store/modules/setting'

/** 扩展的路由配置类型 */
export type AppRouteRecordRaw = RouteRecordRaw & {
  hidden?: boolean
}

/** 顶部进度条配置 */
export const configureNProgress = () => {
  NProgress.configure({
    easing: 'ease',
    speed: 600,
    showSpinner: false,
    parent: 'body'
  })
}

/**
 * 绑定路由切换进度条：beforeEach 启动、afterEach/onError 结束。
 * 受设置面板 showNprogress 开关控制（此前样式与开关已存在但从未接入）。
 * 需在 app.use(store) 之后调用。
 */
export const bindNProgress = (router: Router): void => {
  configureNProgress()
  router.beforeEach(() => {
    try {
      if (useSettingStore().showNprogress) NProgress.start()
    } catch {
      // Pinia 未就绪时静默跳过进度条
    }
  })
  router.afterEach(() => NProgress.done())
  router.onError(() => NProgress.done())
}

/**
 * 设置浏览器标签标题：拼成「页面名 - 站点名」，无 meta.title 时只显示站点名。
 * 站点名取 /session 下发的 site.name（bootstrap 中 await loadSession 后才挂载，首次导航即为真名），
 * 不再退回 index.html 里写死的产品名。
 * @param to 当前路由对象
 */
export const setPageTitle = (to: RouteLocationNormalized): void => {
  const title = to.meta.title
  const siteName = AppConfig.systemInfo.name
  document.title = title ? `${formatMenuTitle(String(title))} - ${siteName}` : siteName
}

/**
 * 格式化菜单标题
 * @param title 菜单标题，可以是 i18n 的 key，也可以是字符串
 * @returns 格式化后的菜单标题
 */
export const formatMenuTitle = (title: string): string => {
  if (title) {
    if (title.startsWith('menus.')) {
      // 使用 te() 方法检查翻译键值是否存在，避免控制台警告
      if (i18n.global.te(title)) {
        return $t(title)
      } else {
        // 如果翻译不存在，返回键值的最后部分作为fallback
        return title.split('.').pop() || title
      }
    }
    return title
  }
  return ''
}
