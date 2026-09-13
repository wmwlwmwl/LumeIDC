import { http } from '../http/index'

// 前台公共 JSON 接口的类型与封装（同 URL，服务端按 Accept 内容协商）。

export interface CategoryChild {
  id: number
  name: string
  description?: string
}
export interface Category {
  id: number
  name: string
  children?: CategoryChild[]
}
export interface ProductLite {
  id: number
  name: string
  desc?: string
  monthly: string
  stock: number
}
export interface Announcement {
  id: number
  title: string
  category?: string
  summary?: string
  content?: string
  cover?: string
  pinned: boolean
  reads?: number
  created_at: string
}

export interface NoticeListResult {
  list: Announcement[]
  total: number
  page: number
  limit: number
  categories: string[]
}

export async function fetchNotices(
  params: { category?: string; keyword?: string; page?: number; limit?: number } = {},
): Promise<NoticeListResult> {
  const res = await http.get<NoticeListResult>('/announcements', params)
  return res as unknown as NoticeListResult
}

export async function fetchNotice(id: number | string): Promise<Announcement | null> {
  const res = await http.get<{ item?: Announcement }>(`/announcements/${id}`)
  return (res.item || null) as unknown as Announcement | null
}

export interface HomeData {
  catalog: Category[]
  products: ProductLite[]
  announcements: Announcement[]
}

export interface ConfigSub {
  name: string
  value?: string
  min?: number
  max?: number
  pricing?: Record<string, number>
  /** 各周期初装费（上游一次性费用，仅首购收取）：monthly/quarterly/yearly */
  setup?: Record<string, number>
}
export interface ConfigOption {
  field: string
  name: string
  option_mode: 'select' | 'range'
  min?: number
  max?: number
  step?: number
  unit?: string
  required?: boolean
  hidden?: boolean
  sub?: ConfigSub[]
}
export interface BuyData {
  product: {
    id: number
    name: string
    description?: string
    requires_identity: boolean
    stock: number
  }
  base: Record<string, number>
  cycle: { monthly: string; quarterly?: string; yearly?: string }
  show_q: boolean
  show_y: boolean
  options: ConfigOption[]
  profit_type: number
  profit_value: number
}

export async function fetchHome(): Promise<HomeData> {
  const res = await http.get<HomeData>('/')
  return res as unknown as HomeData
}

export async function fetchCatalog(fid?: string | number, gid?: string | number) {
  const res = await http.get<{ catalog: Category[]; products: ProductLite[]; fid: string; gid: string; announcements: Announcement[] }>(
    '/cart',
    { fid: fid ?? '', gid: gid ?? '' },
  )
  return res as unknown as {
    catalog: Category[]
    products: ProductLite[]
    fid: string
    gid: string
    announcements: Announcement[]
  }
}

export async function fetchBuy(id: string | number): Promise<BuyData> {
  const res = await http.get<BuyData>(`/buy/${id}`)
  return res as unknown as BuyData
}

export interface OrderResult {
  ok: number
  paid?: boolean
  invoice_id?: number
  amount?: string
  redirect?: string
  code?: string
  msg?: string
}

export async function createOrder(body: Record<string, unknown>): Promise<OrderResult> {
  return http.post<OrderResult>('/order', body)
}

export async function login(body: Record<string, string>): Promise<void> {
  await http.post<{ ok: number }>('/login', body, { silent401: true })
}
export async function register(body: Record<string, string>): Promise<void> {
  await http.post<{ ok: number }>('/register', body, { silent401: true })
}
export async function logout(): Promise<void> {
  await http.post('/logout')
}

// ---- 图形验证码（canvas 由后端生成，PNG data-uri 下发） ----
export interface CaptchaData {
  enabled: boolean
  id?: string
  image?: string
}
export async function fetchCaptcha(scene: 'login' | 'register' | 'phone_code' | 'email_code'): Promise<CaptchaData> {
  const res = await http.get<CaptchaData>('/captcha', { scene })
  return res as unknown as CaptchaData
}

// 手机号验证码登录（extra 可携带发送前所需图形/外部验证码字段）
export async function sendPhoneCode(phone: string, extra: Record<string, string> = {}): Promise<void> {
  await http.post('/auth/phone-code', { phone, ...extra }, { silent401: true })
}
export async function loginByPhoneCode(phone: string, code: string, next: string): Promise<void> {
  await http.post('/auth/login-by-code', { phone, code, next }, { silent401: true })
}
// 注册邮箱验证码（extra 可携带发送前所需图形验证码字段 captcha_id_code/captcha_answer_code）
export async function sendRegisterCode(mode: 'email' | 'phone', target: string, extra: Record<string, string> = {}): Promise<void> {
  const body: Record<string, string> = mode === 'email' ? { mode, email: target } : { mode, phone: target }
  Object.assign(body, extra)
  await http.post('/auth/register-code', body, { silent401: true })
}