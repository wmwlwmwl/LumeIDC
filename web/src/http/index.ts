import { ElMessage } from 'element-plus'
import { useSession } from './session'

// HTTP 客户端：同源会话 cookie + X-CSRF-Token 请求头。
//
// API 前缀：开发期用 /__api/*（由 Vite 的 server.proxy 转发到 Go 并剥掉前缀，见
// vite.config.ts）；生产期为空串——SPA 与 Go 同源，直接请求真实路径。
// 注意：生产没有代理，若沿用 /__api 会打到不存在的路由并被 SPA 兜底成 HTML。
const PREFIX = import.meta.env.DEV ? '/__api' : ''

// API 基址：后台 SPA 启用自定义后台路径（如 /wma）时，其接口都在该前缀下——
// /admin/* 被中间件屏蔽（避免泄漏自定义路径），因此后台入口须调用 setApiBase。
// 前台 SPA 不设置（保持空串，即同源根路径）。
let apiBase = ''
export function setApiBase(base: string): void {
  apiBase = (base || '').replace(/\/$/, '')
}

// 当前 API 前缀（含开发期 /__api），供 WebSocket 等非 fetch 通道拼同源地址，
// 与 request() 内 PREFIX + apiBase 的拼接规则保持一致。
export function apiPrefix(): string {
  return PREFIX + apiBase
}

export interface ApiResult {
  ok: number | boolean
  msg?: string
  code?: string | number
  [key: string]: unknown
}

let onUnauthorized: (() => void) | null = null
export function setOnUnauthorized(fn: () => void) {
  onUnauthorized = fn
}

type QueryValue = string | number | boolean | null | undefined
export type Query = Record<string, QueryValue | QueryValue[]>

export function buildQuery(q?: Query): string {
  if (!q) return ''
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) {
    if (v === null || v === undefined || v === '') continue
    if (Array.isArray(v)) {
      for (const item of v) if (item !== null && item !== undefined && item !== '') sp.append(k, String(item))
    } else {
      sp.append(k, String(v))
    }
  }
  const s = sp.toString()
  return s ? '?' + s : ''
}

export class ApiError extends Error {
  status: number
  data: ApiResult
  constructor(status: number, data: ApiResult) {
    super(data?.msg || '请求失败')
    this.status = status
    this.data = data
  }
}

async function parseResponse(res: Response): Promise<ApiResult> {
  const text = await res.text()
  if (!text) return { ok: false }
  try {
    return JSON.parse(text) as ApiResult
  } catch {
    return { ok: false, msg: text }
  }
}

interface RequestOptions {
  method?: string
  query?: Query
  body?: Record<string, unknown> | FormData
  headers?: Record<string, string>
  /** 401 时不触发全局跳转（如登录接口自身） */
  silent401?: boolean
  /** 跳过 API 基址（公共路径如 /logout、/session） */
  skipBase?: boolean
  /** 会话恢复后的单次重试标记，防止失效会话造成无限循环。 */
  retriedAfterSessionRefresh?: boolean
}

export function isExpiredSessionResponse(status: number, path: string, data: ApiResult): boolean {
  if (status !== 401) return false
  // 错误密码、验证码错误等也是 401，但不能因为它们刷新会话后再次提交。
  // 仅对后端明确表示 Cookie/CSRF 失效的响应做恢复。
  const message = String(data?.msg || '')
  if (!/会话已过期|页面已过期|登录已过期/.test(message)) return false
  // 登录接口也可能先经过 CSRF 中间件，所以允许它恢复一次；其它公共认证接口
  // 的业务 401（密码/验证码错误）由上面的消息判断自然排除。
  return path !== '/logout'
}

async function request<T = ApiResult>(path: string, opts: RequestOptions = {}): Promise<T> {
  const {
    method = 'GET',
    query,
    body,
    silent401 = false,
    skipBase = false,
    retriedAfterSessionRefresh = false,
  } = opts
  // 开发环境先放 /__api，再拼后台基址：/admin/__api 会绕过 Vite 的前缀代理。
  let url = PREFIX + (skipBase ? '' : apiBase) + path
  const qs = buildQuery(query)
  if (qs) url += qs

  // 复制一份请求头，避免改写调用方传入的对象
  const headers: Record<string, string> = { ...(opts.headers || {}) }
  // 后端按 Accept 内容协商返回 JSON 或 SSR HTML；SPA 一律要 JSON。
  headers['Accept'] = 'application/json'

  const init: RequestInit = { method, credentials: 'same-origin', headers }
  if (body) {
    if (body instanceof FormData) {
      init.body = body // 交给浏览器设置 multipart 边界
    } else {
      init.body = JSON.stringify(body)
      headers['Content-Type'] = 'application/json'
    }
  }

  // 会话 CSRF 令牌：写请求必须携带，否则被 CSRF 中间件拦截
  const sess = useSession()
  if (sess.csrf && method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
    headers['X-CSRF-Token'] = sess.csrf
  }

  let res: Response
  try {
    res = await fetch(url, init)
  } catch {
    // fetch 网络层异常（离线/DNS 失败）原样抛英文 TypeError，统一转中文业务错误
    throw new ApiError(0, { ok: 0, msg: '网络连接失败，请检查网络后重试' })
  }
  const data = await parseResponse(res)

  if (res.status === 401 && isExpiredSessionResponse(res.status, path, data) && !retriedAfterSessionRefresh) {
    // Go 重启后旧 lume_session 仍可能存在于浏览器，但服务端已没有对应内存会话。
    // 重新获取 /session 会让 Go 下发新 Cookie 和 CSRF，再只重试当前请求一次。
    try {
      const { loadSession } = await import('./session')
      await loadSession({ force: true })
      return request<T>(path, { ...opts, retriedAfterSessionRefresh: true })
    } catch {
      // 恢复失败时继续走统一 401 处理，避免吞掉原始错误。
    }
  }

  if (res.status === 401 && !silent401) {
    if (onUnauthorized) onUnauthorized()
  }
  if (!res.ok) {
    if (data && data.msg) {
      throw new ApiError(res.status, data)
    }
    // 服务端 5xx 裸错误（如 http.Error("查询失败")）
    if (res.status >= 500) {
      ElMessage.error('服务器内部错误')
    }
    throw new ApiError(res.status, data)
  }
  return data as unknown as T
}

export const http = {
  get<T = ApiResult>(
    path: string,
    query?: Query,
    opts: { silent401?: boolean; skipBase?: boolean } = {},
  ) {
    return request<T>(path, { method: 'GET', query, silent401: opts.silent401, skipBase: opts.skipBase })
  },
  post<T = ApiResult>(
    path: string,
    body?: Record<string, unknown> | FormData,
    opts: { silent401?: boolean; skipBase?: boolean } = {},
  ) {
    return request<T>(path, { method: 'POST', body, silent401: opts.silent401, skipBase: opts.skipBase })
  },
  put<T = ApiResult>(path: string, body?: Record<string, unknown>) {
    return request<T>(path, { method: 'PUT', body })
  },
  delete<T = ApiResult>(path: string, query?: Query, body?: Record<string, unknown>) {
    return request<T>(path, { method: 'DELETE', query, body })
  },
}