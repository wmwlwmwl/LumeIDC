import source from './Pay.vue?raw'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as Vue from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PayData, PayStatus } from '@/api/pay'
import { formatMoney } from '@/utils/format'

const route = Vue.reactive({ params: { id: '1' } })
const api = {
  fetchPay: vi.fn<(id: number) => Promise<PayData>>(),
  payStatus: vi.fn<(id: number) => Promise<PayStatus>>(),
  startPay: vi.fn<(id: number, gateway: string, balance: boolean) => Promise<{ ok: number; url?: string; paid?: boolean; redirect?: string }>>(),
  payByBalance: vi.fn<(id: number) => Promise<{ ok: number; msg?: string; redirect?: string }>>(),
}
const messages = { error: vi.fn(), success: vi.fn(), warning: vi.fn() }
const confirm = vi.fn<() => Promise<void>>()
const location = { href: '' }

// 编译真实 setup，使用 Vue 自身挂载/卸载；不引入测试框架适配层或修改生产组件导出。
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'pay-regression', genDefaultAs: 'PayComponent' })
const compiled = ts.transpileModule(script.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
const modules: Record<string, unknown> = {
  vue: Vue,
  'vue-router': { useRoute: () => route },
  'element-plus': { ElMessage: messages, ElMessageBox: { confirm } },
  '@element-plus/icons-vue': {},
  '@/api/pay': api,
  '@/utils/format': { formatMoney },
  '@/components/public/PublicContainer.vue': {},
}
const component = new Function('require', 'exports', `${compiled}\nreturn PayComponent`)((name: string) => {
  if (!(name in modules)) throw new Error(`未模拟的模块：${name}`)
  return modules[name]
}, {}) as Vue.Component

interface PayState {
  data: PayData | null
  loading: boolean
  loadError: boolean
  busy: boolean
  chosen: string
  useBalance: boolean
  load: () => Promise<boolean>
  chooseGateway: () => Promise<void>
  payBalance: () => Promise<void>
}

function bill(id = 1, extra: Partial<PayData> = {}): PayData {
  return {
    ok: 1,
    invoice: { id: String(id), no: `账单${id}`, amount: '10', credit: '0', remaining: '10', status: 'unpaid', recharge: false },
    paid: false, expired: false, balance_pay: true, balance: '20', csrf: '', site_name: '测试站点',
    gateways: [{ code: 'test', name: '模拟支付', fee_percent: '0', fee_amount: '0', amount: '10' }],
    ...extra,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

let app: Vue.App | undefined
function mount() {
  app = Vue.createApp({ ...component, render: () => null })
  const vm = app.mount(document.createElement('div'))
  return (vm.$ as unknown as { setupState: PayState }).setupState
}

async function flush() {
  await Promise.resolve()
  await Promise.resolve()
  await Vue.nextTick()
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  route.params.id = '1'
  location.href = ''
  vi.stubGlobal('location', location)
  api.fetchPay.mockImplementation(async (id) => bill(id))
  api.payStatus.mockResolvedValue({ paid: false, expired: false })
  api.startPay.mockResolvedValue({ ok: 0 })
  api.payByBalance.mockResolvedValue({ ok: 0 })
  confirm.mockResolvedValue(undefined)
})

afterEach(() => {
  app?.unmount()
  app = undefined
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('支付页异步生命周期', () => {
  it.each(['成功', '失败'])('卸载后的加载%s不写状态、不提示、不启动轮询', async (result) => {
    const pending = deferred<PayData>()
    api.fetchPay.mockReturnValueOnce(pending.promise)
    const state = mount()
    app!.unmount()
    app = undefined
    if (result === '成功') pending.resolve(bill())
    else pending.reject(new Error('旧请求失败'))
    await flush()
    expect(state.data).toBeNull()
    expect(state.loading).toBe(true)
    expect(messages.error).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['成功', '失败'])('快速切换 A/B/A 时旧加载%s不覆盖新状态且仅保留一个定时器', async (result) => {
    const first = deferred<PayData>()
    const second = deferred<PayData>()
    api.fetchPay.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const state = mount()
    route.params.id = '2'
    route.params.id = '1'
    await flush()
    const latest = state.data
    if (result === '成功') first.resolve(bill(1, { paid: true }))
    else first.reject(new Error('旧请求失败'))
    second.resolve(bill(2))
    await flush()
    expect(state.data).toBe(latest)
    expect(state.data?.paid).toBe(false)
    expect(state.loadError).toBe(false)
    expect(state.loading).toBe(false)
    expect(messages.error).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(1)
  })

  it('旧加载结束不能提前关闭新账单的加载态', async () => {
    const first = deferred<PayData>()
    const second = deferred<PayData>()
    api.fetchPay.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const state = mount()
    route.params.id = '2'
    first.resolve(bill())
    await flush()
    expect(state.loading).toBe(true)
    expect(state.data).toBeNull()
    second.resolve(bill(2))
    await flush()
    expect(state.data?.invoice.id).toBe('2')
  })

  it('失败后重试统一恢复轮询，重复加载不会产生多定时器', async () => {
    api.fetchPay.mockRejectedValueOnce(new Error('网络连接失败'))
    const state = mount()
    await flush()
    expect(state.loadError).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
    await state.load()
    expect(state.loadError).toBe(false)
    expect(vi.getTimerCount()).toBe(1)
    await state.load()
    expect(vi.getTimerCount()).toBe(1)
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.payStatus).toHaveBeenCalledTimes(1)
    expect(api.payStatus).toHaveBeenCalledWith(1)
  })

  it('慢状态请求串行执行，跨账单也不重叠，旧已支付结果不能跳转', async () => {
    const pending = deferred<PayStatus>()
    api.payStatus.mockReturnValueOnce(pending.promise)
    const state = mount()
    await flush()
    await vi.advanceTimersByTimeAsync(4000)
    await vi.advanceTimersByTimeAsync(12000)
    expect(api.payStatus).toHaveBeenCalledTimes(1)
    route.params.id = '2'
    await flush()
    await vi.advanceTimersByTimeAsync(12000)
    expect(api.payStatus).toHaveBeenCalledTimes(1)
    pending.resolve({ paid: true, expired: false })
    await flush()
    expect(state.data?.invoice.id).toBe('2')
    expect(state.data?.paid).toBe(false)
    expect(messages.success).not.toHaveBeenCalled()
    expect(location.href).toBe('')
    await vi.advanceTimersByTimeAsync(4000)
    expect(api.payStatus).toHaveBeenLastCalledWith(2)
    expect(vi.getTimerCount()).toBe(1)
  })

  it('状态请求失败后继续轮询', async () => {
    api.payStatus.mockRejectedValueOnce(new Error('网络连接失败'))
    mount()
    await flush()
    await vi.advanceTimersByTimeAsync(8000)
    expect(api.payStatus).toHaveBeenCalledTimes(2)
    expect(messages.error).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(1)
  })

  it.each(['切换账单', '卸载'])('已支付延迟跳转在%s时清除', async (action) => {
    api.payStatus.mockResolvedValueOnce({ paid: true, expired: false })
    mount()
    await flush()
    await vi.advanceTimersByTimeAsync(4000)
    expect(messages.success).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(1)
    if (action === '切换账单') route.params.id = '2'
    else { app!.unmount(); app = undefined }
    await flush()
    await vi.advanceTimersByTimeAsync(800)
    expect(location.href).toBe('')
    expect(vi.getTimerCount()).toBe(action === '切换账单' ? 1 : 0)
  })

  it('当前充值账单已支付后只跳转一次且停止轮询', async () => {
    const recharge = bill()
    recharge.invoice.recharge = true
    api.fetchPay.mockResolvedValueOnce(recharge)
    api.payStatus.mockResolvedValueOnce({ paid: true, expired: false })
    const state = mount()
    await flush()
    await vi.advanceTimersByTimeAsync(4800)
    expect(state.data?.paid).toBe(true)
    expect(location.href).toBe('/user/recharge')
    expect(messages.success).toHaveBeenCalledWith('充值成功，余额已到账')
    expect(vi.getTimerCount()).toBe(0)
  })

  it('过期刷新期间切换账单不显示旧警告、不覆盖新数据', async () => {
    const refresh = deferred<PayData>()
    api.fetchPay.mockResolvedValueOnce(bill()).mockReturnValueOnce(refresh.promise)
    api.payStatus.mockResolvedValueOnce({ paid: false, expired: true })
    const state = mount()
    await flush()
    await vi.advanceTimersByTimeAsync(4000)
    route.params.id = '2'
    await flush()
    refresh.resolve(bill(1, { expired: true }))
    await flush()
    expect(messages.warning).not.toHaveBeenCalled()
    expect(state.data?.invoice.id).toBe('2')
    expect(vi.getTimerCount()).toBe(1)
  })

  it('当前账单过期后刷新并停止轮询', async () => {
    api.fetchPay.mockResolvedValueOnce(bill()).mockResolvedValueOnce(bill(1, { expired: true }))
    api.payStatus.mockResolvedValueOnce({ paid: false, expired: true })
    const state = mount()
    await flush()
    await vi.advanceTimersByTimeAsync(4000)
    expect(state.data?.expired).toBe(true)
    expect(messages.warning).toHaveBeenCalledWith('账单已过期，请重新下单')
    expect(vi.getTimerCount()).toBe(0)
  })

  it('余额确认期间切换账单，确认后不得对新旧账单提交', async () => {
    const pending = deferred<void>()
    confirm.mockReturnValueOnce(pending.promise)
    const state = mount()
    await flush()
    const payment = state.payBalance()
    expect(state.busy).toBe(true)
    route.params.id = '2'
    await flush()
    pending.resolve(undefined)
    await payment
    expect(api.payByBalance).not.toHaveBeenCalled()
    expect(state.busy).toBe(false)
    expect(vi.getTimerCount()).toBe(1)
  })

  it.each(['在线支付', '余额支付'])('%s的旧成功响应不能跳转或清除新提交的忙碌状态', async (kind) => {
    const oldPayment = deferred<{ ok: number; url?: string; redirect?: string }>()
    const newPayment = deferred<{ ok: number; url?: string; redirect?: string }>()
    if (kind === '在线支付') api.startPay.mockReturnValueOnce(oldPayment.promise).mockReturnValueOnce(newPayment.promise)
    else api.payByBalance.mockReturnValueOnce(oldPayment.promise).mockReturnValueOnce(newPayment.promise)
    const state = mount()
    await flush()
    const submit = () => kind === '在线支付' ? state.chooseGateway() : state.payBalance()
    const oldRequest = submit()
    await flush()
    route.params.id = '2'
    await flush()
    const newRequest = submit()
    await flush()
    oldPayment.resolve({ ok: 1, url: '/旧网关', redirect: '/旧账单' })
    await oldRequest
    expect(location.href).toBe('')
    expect(messages.success).not.toHaveBeenCalled()
    expect(state.busy).toBe(true)
    newPayment.resolve({ ok: 0 })
    await newRequest
    expect(state.busy).toBe(false)
    expect(vi.getTimerCount()).toBe(1)
    const mock = kind === '在线支付' ? api.startPay : api.payByBalance
    expect(mock.mock.calls.map(([id]) => id)).toEqual([1, 2])
  })

  it.each(['在线支付', '余额支付'])('%s卸载后的失败不提示或恢复轮询', async (kind) => {
    const pending = deferred<{ ok: number }>()
    if (kind === '在线支付') api.startPay.mockReturnValueOnce(pending.promise)
    else api.payByBalance.mockReturnValueOnce(pending.promise)
    const state = mount()
    await flush()
    const request = kind === '在线支付' ? state.chooseGateway() : state.payBalance()
    await flush()
    app!.unmount()
    app = undefined
    pending.reject(new Error('旧支付失败'))
    await request
    expect(messages.error).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('取消余额确认恢复轮询，确认期间重复点击不会重复提交', async () => {
    const pending = deferred<void>()
    confirm.mockReturnValueOnce(pending.promise)
    const state = mount()
    await flush()
    const request = state.payBalance()
    await state.payBalance()
    await state.chooseGateway()
    expect(confirm).toHaveBeenCalledOnce()
    expect(api.startPay).not.toHaveBeenCalled()
    pending.reject(new Error('取消'))
    await request
    expect(state.busy).toBe(false)
    expect(messages.error).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(1)
  })

  it('支付提交使已在途的状态查询失效，失败后恢复轮询', async () => {
    const status = deferred<PayStatus>()
    const payment = deferred<{ ok: number }>()
    api.payStatus.mockReturnValueOnce(status.promise)
    api.startPay.mockReturnValueOnce(payment.promise)
    const state = mount()
    await flush()
    await vi.advanceTimersByTimeAsync(4000)
    const request = state.chooseGateway()
    status.resolve({ paid: true, expired: false })
    await flush()
    expect(messages.success).not.toHaveBeenCalled()
    expect(state.busy).toBe(true)
    expect(vi.getTimerCount()).toBe(0)
    payment.reject(new Error('发起支付失败'))
    await request
    expect(messages.error).toHaveBeenCalledWith('发起支付失败')
    expect(state.busy).toBe(false)
    expect(vi.getTimerCount()).toBe(1)
  })
})
