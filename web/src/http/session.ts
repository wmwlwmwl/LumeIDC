import { reactive } from 'vue'
import { getAppRouter } from '@/router/registry'

// 全局会话状态：mount 前由 /session 填充，供路由守卫与请求头使用。
export interface SiteContact {
  type: string
  name: string
  value: string
  link?: string
}
interface SiteInfo {
  name: string
  description: string
  keywords: string
  email: string
  phone: string
  hours: string
  contacts: SiteContact[]
}

interface UserInfo {
  id: number
  isAdmin: boolean
  email?: string
  name?: string
  phone?: string
  balance?: string
}

/** 认证相关开关：登录/注册页据此决定展示哪些方式（与后端 /session 的 auth 块对应）。 */
interface AuthFlags {
  phone_otp_login: boolean
  email_registration: boolean
  phone_registration: boolean
  email_verification_required: boolean
  phone_verification_required: boolean
  /** 注册必须同时填写邮箱和手机号（不再分邮箱/手机两种注册方式） */
  require_both_registration: boolean
  /** 修改邮箱时强制先验证原邮箱（两步换绑）；false=可直接修改 */
  require_old_email_change: boolean
  /** 修改手机号时强制先验证原手机（两步换绑）；false=可直接修改 */
  require_old_phone_change: boolean
  captcha_register: boolean
  captcha_login: boolean
  external_captcha_login: boolean
  external_captcha_register: boolean
  /** 注册发码前使用外部验证码 */
  register_code_external: boolean
  /** 找回密码发码前使用外部验证码 */
  forgot_code_external: boolean
  /** 修改邮箱/手机号发验证码前使用外部验证码 */
  profile_code_external: boolean
  external_captcha_phone_login: boolean
}

export interface SessionLoadOptions {
  /** 认证完成后使用：等待已有请求结束，再发起新请求，不复用认证前的结果。 */
  force?: boolean
}

interface SessionState {
  loaded: boolean
  csrf: string
  site: SiteInfo
  /** 普通用户通道身份（前台入口使用） */
  user: UserInfo | null
  adminPath: string
  /** 管理员通道身份（后台入口使用；与用户通道相互独立，可同时在线） */
  adminUser: UserInfo | null
  auth: AuthFlags
  error: Error | null
}

const defaultAuth: AuthFlags = {
  phone_otp_login: false,
  email_registration: true,
  phone_registration: false,
  email_verification_required: false,
  phone_verification_required: true,
  require_both_registration: false,
  require_old_email_change: true,
  require_old_phone_change: true,
  captcha_register: false,
  captcha_login: false,
  external_captcha_login: false,
  external_captcha_register: false,
  register_code_external: false,
  forgot_code_external: false,
  profile_code_external: false,
  external_captcha_phone_login: false,
}

const state = reactive<SessionState>({
  loaded: false,
  csrf: '',
  site: { name: 'LumeIDC', description: '', keywords: '', email: '', phone: '', hours: '', contacts: [] },
  user: null,
  adminPath: '/admin',
  adminUser: null,
  auth: { ...defaultAuth },
  error: null,
})

// 前台与后台是两个独立入口，但共用本模块：adminApp 由后台入口置位，
// 决定「当前身份」取哪个通道（见 currentUser）。
let adminApp = false

/** 标记当前页面为后台入口（须在首次路由守卫前调用）。 */
export function setAdminApp(v: boolean): void {
  adminApp = v
}

/** 当前入口对应的登录身份：后台取管理员通道，前台取普通用户通道。 */
export function currentUser(): UserInfo | null {
  return adminApp ? state.adminUser : state.user
}

/** 清除当前入口对应通道的本地登录态（服务端会话由各自登出接口清理）。 */
export function clearCurrentUser(): void {
  if (adminApp) state.adminUser = null
  else state.user = null
}

const SESSION_PATH = import.meta.env.DEV ? '/__api/session' : '/session'
let pendingLoad: Promise<void> | null = null
let loadGeneration = 0
let lastFocusRefresh = 0
const FOCUS_REFRESH_INTERVAL = 30_000

export const useSession = (): SessionState => state

// 后台 SPA 走 hash 路由，pathname 即部署基址（自定义后台路径是单段，见
// ValidAdminPath）。生产 /session 不下发自定义路径（防匿名探测泄漏），后台入口
// 从自身地址推导——能打开后台登录页的人必然已知道路径。
function deriveAdminBase(): string {
  const seg = window.location.pathname.split('/')[1]
  return seg ? '/' + seg : '/admin'
}

/**
 * 获取或刷新会话。
 *
 * 开发环境必须走 Vite 的 /__api 代理，否则浏览器拿不到 Go 设置的
 * lume_session Cookie 和 CSRF；生产环境则直接请求同源 /session。
 */
export const loadSession = async (options: SessionLoadOptions = {}): Promise<void> => {
  // 普通刷新和并发 401 共用请求；认证后 force 必须在旧请求结束后重新获取。
  if (pendingLoad && !options.force) return pendingLoad
  const previousLoad = pendingLoad
  const generation = ++loadGeneration

  const load = (async () => {
    // 串行接续，避免 /session 响应互相覆盖 Cookie/CSRF；旧请求失败也要继续。
    if (previousLoad) await previousLoad.catch(() => undefined)
    try {
      const res = await fetch(SESSION_PATH, {
        credentials: 'same-origin',
        cache: 'no-store',
        headers: { Accept: 'application/json' },
      })
      const data = (await res.json()) as {
        csrf?: string
        site?: Partial<SiteInfo>
        user?: UserInfo | null
        admin?: { path?: string; user?: UserInfo | null }
        auth?: Partial<AuthFlags>
        msg?: string
      }
      if (!res.ok) throw new Error(data?.msg || `会话获取失败（${res.status}）`)
      if (generation !== loadGeneration) return
      state.csrf = data.csrf || ''
      state.site = { ...state.site, ...(data.site || {}) }
      // 服务重启后 Go 会用新匿名会话响应，此处必须覆盖旧的前端 user 状态。
      state.user = data.user || null
      // 后台路径：服务端下发优先（开发代理/默认 /admin）；生产匿名响应为空，
      // 后台入口改由自身 location 推导，且不允许被空值覆盖已推导的基址。
      const providedPath = data.admin?.path || ''
      if (providedPath) state.adminPath = providedPath
      else if (adminApp) state.adminPath = deriveAdminBase()
      state.adminUser = data.admin?.user || null
      state.auth = { ...defaultAuth, ...(data.auth || {}) }
      state.error = null
    } catch (err) {
      const error = err instanceof Error ? err : new Error('会话获取失败')
      if (generation === loadGeneration) state.error = error
      throw error
    } finally {
      if (generation === loadGeneration) state.loaded = true
    }
  })()
  pendingLoad = load

  try {
    await load
  } finally {
    if (pendingLoad === load) pendingLoad = null
  }
}

/**
 * 页面从后台恢复时轻量检查会话，避免 Go 重启后 UI 继续显示旧登录态。
 * 只做软刷新，不强制整页 reload，避免丢失正在填写的表单内容。
 */
export function installSessionRefresh(
  onRefresh?: (wasAuthenticated: boolean) => void,
): () => void {
  const refresh = () => {
    if (document.hidden) return
    const now = Date.now()
    if (now - lastFocusRefresh < FOCUS_REFRESH_INTERVAL) return
    lastFocusRefresh = now
    const wasAuthenticated = Boolean(currentUser())
    void loadSession()
      .then(() => onRefresh?.(wasAuthenticated))
      .catch(() => undefined)
  }
  const onVisibilityChange = () => {
    if (!document.hidden) refresh()
  }

  window.addEventListener('focus', refresh)
  document.addEventListener('visibilitychange', onVisibilityChange)
  // 切换页面时同样校验（共用上面的节流窗口）：Go 重启/会话过期后浏览器仍有旧
  // Cookie，只靠 focus 触发的话，用户不发起任何接口就会一直看到旧登录态。
  const removeRouteHook = getAppRouter().afterEach(() => refresh())
  return () => {
    window.removeEventListener('focus', refresh)
    document.removeEventListener('visibilitychange', onVisibilityChange)
    removeRouteHook()
  }
}

export default useSession
