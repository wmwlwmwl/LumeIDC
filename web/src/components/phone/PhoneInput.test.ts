import source from './PhoneInput.vue?raw'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as Vue from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// splitE164 为手机号解析纯函数（自定义分区），直接单测
import { splitE164 } from '../../constants/phone-codes'

const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'phone-input-test', genDefaultAs: 'PhoneInputComponent' })
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const modules: Record<string, unknown> = {
  vue: Vue,
  '@/constants/phone-codes': {
    PHONE_CODES: [
      { code: '+86', label: '中国' },
      { code: '+852', label: '中国香港' },
    ],
    MAINLAND_RE: /^1[3-9][0-9]{9}$/,
    splitE164,
  },
}
const component = new Function('require', 'exports', `${compiled}\nreturn PhoneInputComponent`)((name: string) => {
  if (!(name in modules)) throw new Error(`未模拟的模块：${name}`)
  return modules[name]
}, {}) as Vue.Component

interface PhoneInputState {
  code: string
  number: string
  check: () => { ok: boolean; msg: string }
  onInput: (value: string) => void
  emitValue: () => void
}

let app: Vue.App | undefined
// render:null 下 PhoneInput 内部 setup 仍会执行；setupState 为 proxyRefs（ref 已解包，赋值即写回）
function mount() {
  app = Vue.createApp({ ...component, render: () => null })
  const vm = app.mount(document.createElement('div'))
  return (vm.$ as unknown as { setupState: PhoneInputState }).setupState
}

beforeEach(() => {
  vi.useRealTimers()
})

afterEach(() => {
  app?.unmount()
  app = undefined
})

describe('splitE164 拆分（最长前缀匹配而非正则贪婪匹配）', () => {
  it('大陆 E.164 正确拆出 +86，避免误拆成 +861', () => {
    expect(splitE164('+86130296157141')).toEqual({ code: '+86', number: '130296157141' })
  })
  it('港澳区号按更长前缀优先（+852 不会被吃掉数字）', () => {
    expect(splitE164('+85261234567')).toEqual({ code: '+852', number: '61234567' })
  })
  it('三分区号 +971 正确匹配', () => {
    expect(splitE164('+971501234567')).toEqual({ code: '+971', number: '501234567' })
  })
  it('美国 +1 正确匹配', () => {
    expect(splitE164('+14155552671')).toEqual({ code: '+1', number: '4155552671' })
  })
  it('空值重置为默认 +86', () => {
    expect(splitE164('')).toEqual({ code: '+86', number: '' })
  })
  it('未知前缀回退默认 +86 视作号码', () => {
    expect(splitE164('99912345678')).toEqual({ code: '+86', number: '99912345678' })
  })
})

describe('PhoneInput 输入净化', () => {
  it('输入仅保留数字字符', () => {
    const state = mount()
    state.onInput('138a0-0138 000')
    expect(state.number).toBe('13800138000')
  })
  it('默认区号为 +86', () => {
    const state = mount()
    expect(state.code).toBe('+86')
  })
})

describe('PhoneInput 校验', () => {
  it('空号码校验失败', () => {
    const state = mount()
    expect(state.check()).toEqual({ ok: false, msg: '请输入手机号码' })
  })
  it('大陆合法号段通过', () => {
    const state = mount()
    state.onInput('13900138000')
    expect(state.check().ok).toBe(true)
  })
  it('大陆非法号段失败', () => {
    const state = mount()
    state.onInput('12378901234')
    expect(state.check()).toEqual({ ok: false, msg: '手机号格式不正确' })
  })
  it('国际区号非空即通过（长度交由后端 E.164）', () => {
    const state = mount()
    state.onInput('61234567')
    state.code = '+852'
    expect(state.check().ok).toBe(true)
  })
})