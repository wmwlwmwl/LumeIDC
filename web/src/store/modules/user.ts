/**
 * 用户状态管理模块（LumeIDC 适配版）
 *
 * Art 原版依赖 Bearer Token + mock 用户接口。LumeIDC 使用同源 HMAC Cookie
 * 会话，因此这里把登录态直接桥接到 `@/http/session` 的全局会话状态，不再
 * 维护独立的 token。Art 壳中的顶部栏、权限指令等统一从本 store 读取。
 */
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { AppRouteRecord } from '@/types/router'
import { LanguageEnum } from '@/enums/appEnum'
import { currentUser, clearCurrentUser } from '@/http/session'
import { http } from '@/http'
import { getAppRouter } from '@/router/registry'
import { useWorktabStore } from './worktab'

export const useUserStore = defineStore('userStore', () => {
  /** 界面语言（i18n） */
  const language = ref(LanguageEnum.ZH)

  /** 当前入口对应的登录身份（前台=普通用户通道，后台=管理员通道） */
  const activeUser = computed(() => currentUser())

  /** 是否已登录（由 LumeIDC 会话决定） */
  const isLogin = computed(() => Boolean(activeUser.value))

  /** 用户信息（映射为 Art 组件期望的字段名） */
  const getUserInfo = computed(() => ({
    userId: activeUser.value?.id,
    userName: activeUser.value?.name || activeUser.value?.email || '管理员',
    email: activeUser.value?.email || '',
    phone: activeUser.value?.phone || '',
    roles: activeUser.value?.isAdmin ? ['admin'] : [],
    buttons: [] as string[],
  }))

  /** 别名：部分组件使用 `info` */
  const info = getUserInfo

  /** 语言切换 */
  const setLanguage = (lang: LanguageEnum): void => {
    language.value = lang
  }

  /** 全局搜索历史（Art 全局搜索组件使用） */
  const searchHistory = ref<AppRouteRecord[]>([])

  /** Art 接口兼容：用户信息由会话同步，这里不再单独写入 */
  const setUserInfo = (): void => undefined
  const setLoginStatus = (): void => undefined
  const setSearchHistory = (list: AppRouteRecord[]): void => {
    searchHistory.value = list
  }
  const checkAndClearWorktabs = (): void => undefined

  /**
   * 退出登录：调用当前入口对应的登出接口（前台 /logout、后台 /admin/logout），
   * 只清对应会话通道，不影响同一浏览器上的另一通道登录。
   */
  const logOut = async (): Promise<void> => {
    try {
      // 后台入口的 apiBase 已指向后台路径，同一路径会被 AdminPath 改写为 /admin/logout。
      await http.post('/logout')
    } catch {
      // 即使后端登出失败也要清理本地状态，避免卡在“看起来已登录”。
    }
    clearCurrentUser()
    try {
      useWorktabStore().clearAll()
    } catch {
      // store 尚未初始化时忽略
    }
    const router = getAppRouter()
    if (router.currentRoute.value.path !== '/login') {
      const next = router.currentRoute.value.fullPath
      router.push({ path: '/login', query: next !== '/login' ? { next } : undefined })
    }
  }

  return {
    language,
    isLogin,
    info,
    getUserInfo,
    searchHistory,
    setLanguage,
    setUserInfo,
    setLoginStatus,
    setSearchHistory,
    checkAndClearWorktabs,
    logOut,
  }
})
