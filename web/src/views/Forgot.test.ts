import source from './Forgot.vue?raw'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as Vue from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { hasCaptchaResult } from '../components/captcha-registry'

const router = { push: vi.fn(async () => {}) }
const api = {
  fetchCaptcha: vi.fn(async (scene: string) => ({ enabled: false, id: '', image: '' })),
  sendForgotCode: vi.fn(async () => {}),
  resetPassword: vi.fn(async () => {}),
}
const messages = { error: vi.fn(), success: vi.fn(), warning: vi.fn() }

// 编译真实 setup，使用 Vue 自身挂载/卸载；不引入测试框架适配层或修改生产组件导出。
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'forgot-regression', genDefaultAs: 'ForgotComponent' })
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const modules: Record<string, unknown> = {
  vue: Vue,
  'vue-router': { useRouter: () => router },
  'element-plus': { ElMessage: messages },
  '@element-plus/icons-vue': {},
  '../api/store': api,
  '@/components/public/PublicAuthCard.vue': {},
  '@/components/phone/PhoneInput.vue': {},
  '../http/session': { useSession: () => ({ auth: { forgot_code_external: false, profile_code_external: false } }) },
  '../components/ExternalCaptcha.vue': {},
  '../components/captcha-registry': { hasCaptchaResult },
}
const component = new Function('require', 'exports', `${compiled}\nreturn ForgotComponent`)((name: string) => {
  if (!(name in modules)) throw new Error(`未模拟的模块：${name}`)
  return modules[name]
}, {}) as Vue.Component

interface ForgotState {
  mode: 'email' | 'phone'
  form: { email: string; phone: string; code: string; password: string; confirm: string; captchaAnswer: string }
  captcha: { enabled: boolean; id?: string; image?: string }
  cooldown: number
  formRef: Record<string, unknown>
  phoneInputRef: { check: () => { ok: boolean; msg?: string } } | undefined
  send: () => Promise<void>
  submit: () => Promise<void>
  loadCaptcha: () => Promise<void>
  account: string
}

let app: Vue.App | undefined
function mount() {
  app = Vue.createApp({ ...component, render: () => null })
  const vm = app.mount(document.createElement('div'))
  const state = (vm.$ as unknown as { setupState: ForgotState }).setupState
  // render:null 下无真实 el-form / PhoneInput 实例：注入通过校验的表单桩与手机号校验桩
  // （setupState 为 proxyRefs，赋值即写回 ref.value）
  state.formRef = { validate: async () => true }
  state.phoneInputRef = { check: () => ({ ok: true, msg: '' }) }
  return state
}

async function flush() {
  await Promise.resolve()
  await Promise.resolve()
  await Vue.nextTick()
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  api.fetchCaptcha.mockImplementation(async (scene) => ({ enabled: false, id: '', image: '' }))
  api.sendForgotCode.mockResolvedValue(undefined)
  api.resetPassword.mockResolvedValue(undefined)
  router.push.mockResolvedValue(undefined)
})

afterEach(() => {
  app?.unmount()
  app = undefined
  vi.clearAllTimers()
  vi.useRealTimers()
})

describe('找回密码渠道判定', () => {
  it('默认邮箱模式，挂载时按邮箱场景拉取验证码', async () => {
    mount()
    await flush()
    expect(api.fetchCaptcha).toHaveBeenCalledWith('forgot_code')
  })
  it('切换到手机模式后按手机场景刷新验证码', async () => {
    const state = mount()
    await flush()
    state.mode = 'phone'
    await flush()
    expect(api.fetchCaptcha).toHaveBeenLastCalledWith('forgot_code')
  })
})

describe('找回密码发送验证码', () => {
  it('未输入目标时拦截并提示', async () => {
    const state = mount()
    await state.send()
    expect(messages.warning).toHaveBeenCalledWith('请先填写邮箱')
    expect(api.sendForgotCode).not.toHaveBeenCalled()
  })
  it('邮箱模式直接发送并进入倒计时', async () => {
    const state = mount()
    state.form.email = 'user@example.com'
    await state.send()
    expect(api.sendForgotCode).toHaveBeenCalledWith('user@example.com', {})
    expect(messages.success).toHaveBeenCalled()
    expect(state.cooldown).toBe(60)
  })
  it('手机模式提交完整 E.164 号码', async () => {
    const state = mount()
    state.mode = 'phone'
    state.form.phone = '+852612345678'
    await flush()
    await state.send()
    expect(api.sendForgotCode).toHaveBeenCalledWith('+852612345678', {})
  })
  it('验证码开启但未填图形码时拦截', async () => {
    api.fetchCaptcha.mockResolvedValue({ enabled: true, id: 'cid', image: 'data:image/png;base64,x' })
    const state = mount()
    state.form.email = 'user@example.com'
    await state.loadCaptcha()
    await state.send()
    expect(messages.warning).toHaveBeenCalledWith('请输入图形验证码')
    expect(api.sendForgotCode).not.toHaveBeenCalled()
  })
  it('携图形验证码字段发送', async () => {
    api.fetchCaptcha.mockResolvedValue({ enabled: true, id: 'cid', image: 'data:image/png;base64,x' })
    const state = mount()
    state.form.email = 'user@example.com'
    await state.loadCaptcha()
    state.form.captchaAnswer = 'ab12'
    await state.send()
    expect(api.sendForgotCode).toHaveBeenCalledWith('user@example.com', {
      captcha_id_code: 'cid',
      captcha_answer_code: 'ab12',
    })
  })
  it('发送失败提示并刷新验证码', async () => {
    api.sendForgotCode.mockRejectedValue(new Error('发送失败'))
    const state = mount()
    state.form.email = 'user@example.com'
    await state.send()
    expect(messages.error).toHaveBeenCalledWith('发送失败')
    expect(api.fetchCaptcha).toHaveBeenLastCalledWith('forgot_code')
  })
  it('手机模式号码格式非法时拦截', async () => {
    const state = mount()
    state.mode = 'phone'
    state.form.phone = '+852612345678'
    await flush()
    state.phoneInputRef = { check: () => ({ ok: false, msg: '手机号格式不正确' }) }
    await state.send()
    expect(messages.warning).toHaveBeenCalledWith('手机号格式不正确')
    expect(api.sendForgotCode).not.toHaveBeenCalled()
  })
})

describe('找回密码重置提交', () => {
  it('成功重置并跳转登录页', async () => {
    const state = mount()
    state.form.email = 'user@example.com'
    state.form.code = '123456'
    state.form.password = 'newpass123'
    await state.submit()
    expect(api.resetPassword).toHaveBeenCalledWith('user@example.com', '123456', 'newpass123')
    expect(messages.success).toHaveBeenCalledWith('密码已重置，请重新登录')
    expect(router.push).toHaveBeenCalledWith('/login')
  })
  it('重置失败提示且不跳转', async () => {
    api.resetPassword.mockRejectedValue(new Error('验证码错误或已过期'))
    const state = mount()
    state.form.email = 'user@example.com'
    state.form.code = '000000'
    state.form.password = 'newpass123'
    await state.submit()
    expect(messages.error).toHaveBeenCalledWith('验证码错误或已过期')
    expect(router.push).not.toHaveBeenCalled()
  })
})