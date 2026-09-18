/**
 * 外部验证码 SDK 初始化注册表。
 * 消除 ExternalCaptcha.vue 中的 if/else if 分发。
 * 新增 provider：新增工厂函数 + 调用 registerCaptchaProvider。
 */

export interface CaptchaConfig {
  enabled: boolean
  provider?: string
  public_id?: string
  sdk_url?: string
  script_url?: string
  api_base_url?: string
  purpose?: string
  scene: string
}

/** SDK 工厂返回的 init 函数签名 */
export type CaptchaInitFn = (el: HTMLElement, cfg: CaptchaConfig, cbs: CaptchaCallbacks) => Promise<void>

export interface CaptchaCallbacks {
  setFields: (vals: Record<string, string>) => void
  clearFields: () => void
  readyOff: () => void
  /** Vaptcha 特殊：设置组件级 widget ref */
  setWidget?: (w: unknown | null) => void
}

type Factory = () => CaptchaInitFn
const factories: Record<string, Factory> = {}

export function registerCaptchaProvider(provider: string, factory: Factory) {
  factories[provider] = factory
}

export function getCaptchaInit(provider: string): CaptchaInitFn | null {
  const f = factories[provider]
  return f ? f() : null
}

// ===== Geetest =====

interface GtInstance {
  appendTo: (el: HTMLElement) => void
  onSuccess: (cb: () => void) => void
  onError: (cb: () => void) => void
  onClose: (cb: () => void) => void
  getValidate: () => Record<string, string>
}

registerCaptchaProvider('geetest', () => async (el, cfg, cbs) => {
  const w = window as Window & { initGeetest4?: (opts: Record<string, unknown>, cb: (i: GtInstance) => void) => void }
  if (typeof w.initGeetest4 !== 'function') throw new Error('Geetest SDK 初始化失败')
  w.initGeetest4({ captchaId: cfg.public_id, product: 'bind' }, (instance) => {
    instance.appendTo(el)
    instance.onSuccess(() => cbs.setFields(instance.getValidate()))
    instance.onError(() => { cbs.clearFields(); cbs.readyOff() })
    instance.onClose(() => { cbs.clearFields(); cbs.readyOff() })
  })
})

// ===== Corptcha =====

registerCaptchaProvider('corptcha', () => async (el, cfg, cbs) => {
  const w = window as Window & { Corptcha?: { render: (el: HTMLElement, opts: Record<string, unknown>) => void } }
  if (!w.Corptcha || typeof w.Corptcha.render !== 'function') throw new Error('Corptcha SDK 初始化失败')
  const mount = document.createElement('div')
  el.append(mount)
  w.Corptcha.render(mount, {
    siteKey: cfg.public_id,
    apiBaseUrl: cfg.api_base_url,
    purpose: cfg.purpose || cfg.scene,
    autoExecute: true,
    onSuccess: (token: string) => cbs.setFields({ captcha_token: token }),
    onError: () => { cbs.clearFields(); cbs.readyOff() },
    onExpired: () => { cbs.clearFields(); cbs.readyOff() },
  })
})

// ===== Vaptcha =====

interface VpWidget {
  validate: () => Promise<void>
  getVerifyResult: () => { token?: string; knock?: string; dfu?: string; ip?: string }
}

/** Vaptcha 的 init 需要组件级 mount 引用，这里只做桥接：mountVaptcha 由组件提供 */
let _vaptchaMount: (() => Promise<void>) | null = null
export function setVaptchaRemount(fn: () => Promise<void>) { _vaptchaMount = fn }

registerCaptchaProvider('vaptcha', () => async (el, cfg, cbs) => {
  const w = window as Window & { vaptcha?: (opts: Record<string, unknown>) => Promise<VpWidget> }
  if (typeof w.vaptcha !== 'function') throw new Error('Vaptcha SDK 初始化失败')
  // 把 mount 逻辑存起来（ExternalCaptcha.vue 会在 mountVaptcha 里赋具体值）
  const mount = document.createElement('div')
  el.replaceChildren(mount)
  const widget = await w.vaptcha({ vid: cfg.public_id, container: mount, mode: 'click' })
  if (!widget || typeof widget.validate !== 'function') {
    throw new Error('Vaptcha SDK 版本不受支持')
  }
  cbs.setWidget?.(widget)
})
