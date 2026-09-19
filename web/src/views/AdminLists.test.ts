import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import type { AdminService, RecoverySummary } from '../admin/api'
import ts from 'typescript'
import * as Vue from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as table from '../admin/useAdminTable'
import * as labels from '../utils/admin-labels'
import * as gatewayDrivers from '../admin/gateway-drivers'

const messages = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn(), warning: vi.fn() }))
vi.mock('element-plus', () => ({ ElMessage: messages }))
const sources = import.meta.glob('./Admin*.vue', { eager: true, query: '?raw', import: 'default' }) as Record<string, string>
const api: Record<string, ReturnType<typeof vi.fn>> = {}
const post = vi.fn()
const route = Vue.reactive({ params: { id: '1' }, query: {} as Record<string, string> })
const cases = [
  ['Orders', 'fetchOrders'], ['Users', 'fetchUsers'], ['Tickets', 'fetchAdminTickets'],
  ['Services', 'fetchAdminServices'], ['CancelRequests', 'fetchAdminCancelRequests'],
  ['Announcements', 'fetchAdminAnnouncements'], ['Coupons', 'fetchAdminCoupons'],
  ['Gateways', 'fetchAdminGateways'], ['Products', 'fetchAdminProducts'],
  ['Servers', 'fetchAdminServers'], ['Types', 'fetchAdminTypes'],
  ['Verifications', 'fetchAdminVerifications'], ['Logs', 'fetchAdminLogs'],
  ['Refunds', 'fetchAdminRefunds'], ['Catalog', 'fetchServerCatalog'],
] as const

// 编译真实 setup；恢复对话框额外编译模板，验证关闭事件绑定。
const templates: Record<string, Vue.ComponentOptions['render']> = {}
const components = Object.fromEntries(cases.map(([name]) => {
  const { descriptor } = parse(sources[`./Admin${name}.vue`])
  const script = compileScript(descriptor, { id: name, genDefaultAs: 'Component' })
  if (name === 'Services') {
    const template = compileTemplate({
      source: descriptor.template!.content, filename: 'AdminServices.vue', id: name,
      compilerOptions: { bindingMetadata: script.bindings },
    })
    const output = ts.transpileModule(template.code, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText
    const exports: { render?: Vue.ComponentOptions['render'] } = {}
    new Function('require', 'exports', output)(() => Vue, exports)
    templates[name] = exports.render
  }
  const compiled = ts.transpileModule(script.content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const modules: Record<string, unknown> = {
    vue: Vue, 'vue-router': { useRouter: () => ({ push: vi.fn() }), useRoute: () => route },
    'element-plus': {
      ElMessage: messages, ElMessageBox: { confirm: vi.fn().mockResolvedValue(true) },
      ElButton: Vue.defineComponent({ setup: (_, { slots }) => () => Vue.h('button', slots.default?.()) }),
    },
    '@element-plus/icons-vue': {}, '../admin/api': api, '../admin/useAdminTable': table,
    '../http/index': { http: { post } }, '../utils/admin-labels': labels,
    // AdminGateways 在 setup 阶段就会调 gatewayDriverList() 并读 GATEWAY_DRIVERS.mock.style，
    // 这里直接给真实注册表，避免用空桩再炸一次。
    '../admin/gateway-drivers': gatewayDrivers,
    '@/utils/clipboard': { copyText: vi.fn(async () => true) },
  }
  const component = new Function('require', 'exports', `const useRouter = () => ({ push() {} });\n${compiled}\nreturn Component`)((id: string) => {
    if (id.endsWith('.vue')) return {}
    if (!(id in modules)) throw new Error(`未模拟的模块：${id}`)
    return modules[id]
  }, {}) as Vue.Component
  return [name, component]
}))

type State = {
  list: unknown[]; rows: unknown[]; loading: boolean; total: number; profit: string
  products: unknown[]; stats: Record<string, number>; pending: number; serverName: string
  searchForm: { q: string }; page: number
  load: (silent?: boolean) => Promise<void>
  handleSearch: () => void
  handleCurrentChange: (page: number) => void
  handlePageChange: (page: number) => void
  handleSortChange: (sort: { prop: string; order: string }) => void
  act: (row: { id: number }, action: string) => Promise<void>
}
let app: Vue.App | undefined
function mount(name: string) {
  app = Vue.createApp({ ...components[name], render: () => null })
  const vm = app.mount(document.createElement('div'))
  return (vm.$ as unknown as { setupState: State }).setupState
}
type RecoveryState = State & {
  openRecover: (row: AdminService) => Promise<void>
  submitRecover: () => Promise<void>
  recoverVisible: boolean; recoverLoading: boolean; recoverSubmitting: boolean
  recoverRow: AdminService | null; recoverSummary: RecoverySummary | null
  recoverForm: {
    decision: '' | 'confirmed_completed' | 'confirmed_not_executed'; evidence: string
    remote_stable: boolean; billing_verified: boolean; delivery_verified: boolean
    verified_host_id: number | ''
  }
}
let emitRecovery: (event: 'close' | 'closed' | 'update:modelValue', ...args: unknown[]) => void
let closeRecovery: () => void
function mountRecovery(template = false) {
  if (!template) return mount('Services') as RecoveryState
  const stub = Vue.defineComponent({ setup: (_, { slots }) => () => Vue.h('div', [slots.default?.(), slots.footer?.()]) })
  const dialog = Vue.defineComponent({
    props: ['title', 'modelValue', 'beforeClose'], emits: ['close', 'closed', 'update:modelValue'],
    setup(props, { emit, slots }) {
      if (props.title === '对账恢复') {
        emitRecovery = emit
        closeRecovery = () => {
          if (props.beforeClose) props.beforeClose(() => {})
          emit('close')
        }
      }
      return () => Vue.h('div', { 'data-dialog': props.title }, [slots.default?.(), slots.footer?.()])
    },
  })
  app = Vue.createApp({ ...components.Services, render: templates.Services })
  for (const name of new Set(sources['./AdminServices.vue'].match(/(?:El|Art)[A-Z]\w+/g))) {
    app.component(name, name === 'ElDialog' ? dialog : stub)
  }
  const vm = app.mount(document.createElement('div'))
  return (vm.$ as unknown as { setupState: RecoveryState }).setupState
}
function recovery(id: number): RecoverySummary {
  return {
    service_id: id, job_id: id * 10, version: id, kind: 'provision', order_id: id,
    cycle: 'monthly', provider: '', server_id: 1, account: '', host_id: id * 100,
    checkpoint_host: '', upstream_invoice: '', invoice_id: id, invoice_status: 1,
    order_status: 1, amount: '10', paid_amount: '10', refunded: '0', grants: 1,
    pending_jobs: 1, target_product_id: 1, target_pid: 1, can_resume: true,
    resume_reason: '', block_reason: '',
  }
}
const recoveryRow = (id: number) => ({ id } as AdminService)
function fillRecovery(state: RecoveryState) {
  Object.assign(state.recoverForm, {
    decision: 'confirmed_not_executed', evidence: '已经逐项核对上游账单和实例状态',
    remote_stable: true, billing_verified: true, delivery_verified: true,
  })
}
function unmount() { app?.unmount(); app = undefined }
function deferred() {
  let resolve!: (data: unknown) => void
  let reject!: (error: Error) => void
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
function result(name: string, id: number) {
  const list = [{ id }]
  if (name === 'Catalog') return { rows: list, server: { name: `服务器${id}` }, types: [], error: '' }
  return ['Orders', 'Users', 'Tickets', 'Services', 'CancelRequests'].includes(name)
    ? { list, total: id, profit: String(id), products: list, pending: id } : list
}
async function flush() {
  for (let i = 0; i < 10; i++) await Promise.resolve()
  await Vue.nextTick()
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  for (const [name, fetcher] of cases) api[fetcher] = vi.fn().mockResolvedValue(result(name, 0))
  api.fetchAdminTicketStats = vi.fn().mockResolvedValue({ pending: 0 })
  api.fetchTicketAssignees = vi.fn().mockResolvedValue([])
  api.fetchServiceRecovery = vi.fn().mockImplementation(async (id: number) => recovery(id))
  api.confirmServiceRecovery = vi.fn().mockResolvedValue(undefined)
  post.mockResolvedValue({ ok: 1 })
})
afterEach(() => { unmount(); vi.clearAllTimers(); vi.useRealTimers() })

describe.each(cases)('管理列表 %s', (name, fetcher) => {
  it.each(['成功', '失败'])('可控乱序：旧请求%s不覆盖最新结果或提示', async (outcome) => {
    const old = deferred()
    api[fetcher].mockReturnValueOnce(old.promise).mockResolvedValueOnce(result(name, 2))
    const state = mount(name)
    await state.load()
    const latest = JSON.stringify({ list: state.list, rows: state.rows, total: state.total, profit: state.profit, products: state.products, pending: state.pending, serverName: state.serverName })
    if (outcome === '成功') old.resolve(result(name, 1))
    else old.reject(new Error('过期请求失败'))
    await flush()
    expect(JSON.stringify({ list: state.list, rows: state.rows, total: state.total, profit: state.profit, products: state.products, pending: state.pending, serverName: state.serverName })).toBe(latest)
    expect(messages.error).not.toHaveBeenCalled()
    expect(state.loading).toBe(false)
  })

  it('旧请求结束不能关闭新请求的加载状态', async () => {
    const old = deferred(), latest = deferred()
    api[fetcher].mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    const state = mount(name)
    const loading = state.load()
    old.resolve(result(name, 1))
    await flush()
    expect(state.loading).toBe(true)
    latest.resolve(result(name, 2))
    await loading
    expect(state.loading).toBe(false)
  })

  it.each(['成功', '失败'])('卸载后%s不得写状态、提示或恢复定时器', async (outcome) => {
    const pending = deferred()
    api[fetcher].mockReturnValueOnce(pending.promise)
    const state = mount(name)
    const before = JSON.stringify({ list: state.list, rows: state.rows, loading: state.loading })
    unmount()
    if (outcome === '成功') pending.resolve(result(name, 1))
    else pending.reject(new Error('卸载后的请求失败'))
    await flush()
    expect(JSON.stringify({ list: state.list, rows: state.rows, loading: state.loading })).toBe(before)
    expect(messages.error).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
    const calls = api[fetcher].mock.calls.length
    await state.load()
    expect(api[fetcher]).toHaveBeenCalledTimes(calls)
  })
})

describe('对账恢复请求隔离', () => {
  it.each(['成功', '失败'])('A 关闭再开 B：旧摘要%s不影响 B', async (outcome) => {
    const old = deferred()
    api.fetchServiceRecovery.mockReturnValueOnce(old.promise)
    const state = mountRecovery(true)
    const opening = state.openRecover(recoveryRow(1))
    await flush()
    closeRecovery()
    await state.openRecover(recoveryRow(2))
    fillRecovery(state)
    const before = JSON.stringify([state.recoverSummary, state.recoverForm])
    const calls = api.fetchAdminServices.mock.calls.length
    if (outcome === '成功') old.resolve(recovery(1))
    else old.reject(new Error('旧摘要失败'))
    await opening
    expect(JSON.stringify([state.recoverSummary, state.recoverForm])).toBe(before)
    expect(state.recoverVisible).toBe(true)
    expect(state.recoverLoading).toBe(false)
    expect(messages.error).not.toHaveBeenCalled()
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls)
  })

  it.each(['成功', '失败'])('同服务重开：旧摘要%s不能结束新加载', async (outcome) => {
    const old = deferred(), latest = deferred()
    api.fetchServiceRecovery.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    const state = mountRecovery()
    const opening = state.openRecover(recoveryRow(1))
    state.recoverVisible = false
    const reopening = state.openRecover(recoveryRow(1))
    if (outcome === '成功') old.resolve(recovery(1))
    else old.reject(new Error('旧摘要失败'))
    await opening
    expect(state.recoverLoading).toBe(true)
    expect(state.recoverSummary).toBeNull()
    expect(state.recoverVisible).toBe(true)
    expect(messages.error).not.toHaveBeenCalled()
    latest.resolve({ ...recovery(1), version: 9 })
    await reopening
    expect(state.recoverSummary?.version).toBe(9)
    expect(state.recoverLoading).toBe(false)
  })

  it.each(['取消', '关闭按钮或遮罩', '卸载'])('%s立即使摘要成功和失败回调失效', async (way) => {
    for (const outcome of ['成功', '失败']) {
      const pending = deferred()
      api.fetchServiceRecovery.mockReturnValueOnce(pending.promise)
      const state = mountRecovery(true)
      const opening = state.openRecover(recoveryRow(1))
      await flush()
      if (way === '卸载') unmount()
      else if (way === '取消') {
        const button = app!._instance!.proxy!.$el.querySelector('[data-dialog="对账恢复"] button') as HTMLButtonElement
        button.click()
      } else closeRecovery()
      const before = JSON.stringify([state.recoverSummary, state.recoverForm, state.recoverLoading, state.recoverVisible])
      const calls = api.fetchAdminServices.mock.calls.length
      if (outcome === '成功') pending.resolve(recovery(1))
      else pending.reject(new Error('失效摘要失败'))
      await opening
      expect(JSON.stringify([state.recoverSummary, state.recoverForm, state.recoverLoading, state.recoverVisible])).toBe(before)
      expect(messages.error).not.toHaveBeenCalled()
      expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls)
      unmount()
      expect(vi.getTimerCount()).toBe(0)
    }
  })

  it('旧关闭动画事件不能清空或关闭新对话框', async () => {
    const state = mountRecovery(true)
    await state.openRecover(recoveryRow(1))
    closeRecovery()
    await state.openRecover(recoveryRow(2))
    emitRecovery('close')
    emitRecovery('closed')
    emitRecovery('update:modelValue', false)
    expect(state.recoverSummary?.service_id).toBe(2)
    expect(state.recoverVisible).toBe(true)
  })

  it('列表刷新不作废恢复请求，当前失败仍提示并刷新列表', async () => {
    const pending = deferred()
    api.fetchServiceRecovery.mockReturnValueOnce(pending.promise)
    const state = mountRecovery()
    const opening = state.openRecover(recoveryRow(1))
    await state.load()
    pending.resolve(recovery(1))
    await opening
    expect(state.recoverSummary?.service_id).toBe(1)
    api.fetchServiceRecovery.mockRejectedValueOnce(new Error('当前摘要失败'))
    await state.openRecover(recoveryRow(2))
    expect(messages.error).toHaveBeenCalledWith('当前摘要失败')
    expect(state.recoverVisible).toBe(false)
    expect(state.recoverLoading).toBe(false)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(3)
  })

  it.each(['成功', '失败'])('旧提交%s不污染新提交，即使同服务重开', async (outcome) => {
    const old = deferred(), latest = deferred()
    api.confirmServiceRecovery.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    fillRecovery(state)
    const submitting = state.submitRecover()
    state.recoverVisible = false
    await state.openRecover(recoveryRow(1))
    expect(state.recoverSubmitting).toBe(false)
    fillRecovery(state)
    const resubmitting = state.submitRecover()
    const calls = api.fetchAdminServices.mock.calls.length
    if (outcome === '成功') old.resolve(undefined)
    else old.reject(new Error('旧提交失败'))
    await submitting
    expect(state.recoverVisible).toBe(true)
    expect(state.recoverSubmitting).toBe(true)
    expect(state.recoverSummary?.service_id).toBe(1)
    expect(messages.error).not.toHaveBeenCalled()
    expect(messages.success).not.toHaveBeenCalled()
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls)
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls)
    latest.resolve(undefined)
    await resubmitting
    expect(state.recoverSubmitting).toBe(false)
    expect(messages.success).toHaveBeenCalledTimes(1)
  })

  it.each(['关闭', '卸载'])('%s后旧提交的成功和失败均无副作用', async (way) => {
    for (const outcome of ['成功', '失败']) {
      const pending = deferred()
      api.confirmServiceRecovery.mockReturnValueOnce(pending.promise)
      const state = mountRecovery()
      await state.openRecover(recoveryRow(1))
      fillRecovery(state)
      const submitting = state.submitRecover()
      if (way === '卸载') unmount()
      else state.recoverVisible = false
      const before = JSON.stringify([state.recoverSummary, state.recoverSubmitting, state.recoverVisible])
      const calls = api.fetchAdminServices.mock.calls.length
      if (outcome === '成功') pending.resolve(undefined)
      else pending.reject(new Error('旧提交失败'))
      await submitting
      expect(JSON.stringify([state.recoverSummary, state.recoverSubmitting, state.recoverVisible])).toBe(before)
      expect(messages.error).not.toHaveBeenCalled()
      expect(messages.success).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(3000)
      expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls)
      unmount()
      expect(vi.getTimerCount()).toBe(0)
    }
  })

  it('重复提交被阻止，提交快照不受后续表单修改影响', async () => {
    const pending = deferred()
    api.confirmServiceRecovery.mockReturnValueOnce(pending.promise)
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    fillRecovery(state)
    const submitting = state.submitRecover()
    await state.submitRecover()
    expect(api.confirmServiceRecovery).toHaveBeenCalledTimes(1)
    state.recoverForm.decision = 'confirmed_completed'
    state.recoverForm.evidence = '后来改动的表单不能改变既有提交'
    state.recoverRow!.id = 2
    state.recoverSummary!.version = 8
    pending.resolve(undefined)
    await submitting
    expect(api.confirmServiceRecovery).toHaveBeenCalledWith(1, {
      job_id: 10, expected_version: 1, decision: 'confirmed_not_executed',
      evidence: '已经逐项核对上游账单和实例状态', verified_host_id: 0,
      remote_stable: true, billing_verified: true, delivery_verified: true,
    })
    const calls = api.fetchAdminServices.mock.calls.length
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls + 1)
  })

  it.each(['重开', '卸载'])('提交成功后的列表刷新期间%s，不得安排旧补刷', async (way) => {
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    fillRecovery(state)
    const refresh = deferred()
    api.fetchAdminServices.mockReturnValueOnce(refresh.promise)
    const submitting = state.submitRecover()
    await flush()
    if (way === '卸载') unmount()
    else await state.openRecover(recoveryRow(2))
    refresh.resolve(result('Services', 1))
    await submitting
    const calls = api.fetchAdminServices.mock.calls.length
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls)
    if (way === '重开') expect(state.recoverVisible).toBe(true)
    else expect(vi.getTimerCount()).toBe(0)
  })

  it('补刷已安排后重开对话框，旧定时回调也不得刷新', async () => {
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    fillRecovery(state)
    await state.submitRecover()
    await state.openRecover(recoveryRow(2))
    const calls = api.fetchAdminServices.mock.calls.length
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls)
    expect(state.recoverSummary?.service_id).toBe(2)
  })

  it('不允许重新排队时仍可提交已生效决策', async () => {
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    fillRecovery(state)
    state.recoverSummary!.can_resume = false
    state.recoverForm.decision = 'confirmed_completed'
    await state.submitRecover()
    expect(api.confirmServiceRecovery).toHaveBeenCalledWith(1, expect.objectContaining({
      decision: 'confirmed_completed', verified_host_id: 100,
    }))
    expect(state.recoverVisible).toBe(false)
    const calls = api.fetchAdminServices.mock.calls.length
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls)
  })

  it('当前提交失败保留表单，允许纠正后重试', async () => {
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    fillRecovery(state)
    const before = JSON.stringify(state.recoverForm)
    api.confirmServiceRecovery.mockRejectedValueOnce(new Error('证据版本冲突'))
    await state.submitRecover()
    expect(messages.error).toHaveBeenCalledWith('证据版本冲突')
    expect(state.recoverVisible).toBe(true)
    expect(state.recoverSubmitting).toBe(false)
    expect(JSON.stringify(state.recoverForm)).toBe(before)
    await state.submitRecover()
    expect(api.confirmServiceRecovery).toHaveBeenCalledTimes(2)
  })

  it('拒绝响应中的服务身份不匹配', async () => {
    api.fetchServiceRecovery.mockResolvedValueOnce(recovery(2))
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    expect(state.recoverSummary).toBeNull()
    expect(messages.error).toHaveBeenCalled()
  })

  it.each(['隐藏', '加载中', '无服务', '无摘要', '服务不一致', '证据冲突', '禁止重排', '未选决策', '证据过短', '未稳定', '未核账单', '未核交付', '主机为零', '主机小数', '主机无穷'])('表单保护：%s不发请求', async (guard) => {
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    fillRecovery(state)
    switch (guard) {
      case '隐藏': state.recoverVisible = false; break
      case '加载中': state.recoverLoading = true; break
      case '无服务': state.recoverRow = null; break
      case '无摘要': state.recoverSummary = null; break
      case '服务不一致': state.recoverSummary!.service_id = 2; break
      case '证据冲突': state.recoverSummary!.block_reason = '证据冲突'; break
      case '禁止重排': state.recoverSummary!.can_resume = false; break
      case '未选决策': state.recoverForm.decision = ''; break
      case '证据过短': state.recoverForm.evidence = '   太短   '; break
      case '未稳定': state.recoverForm.remote_stable = false; break
      case '未核账单': state.recoverForm.billing_verified = false; break
      case '未核交付': state.recoverForm.delivery_verified = false; break
      default:
        state.recoverForm.decision = 'confirmed_completed'
        state.recoverForm.verified_host_id = guard === '主机为零' ? 0 : guard === '主机小数' ? 1.5 : Infinity
    }
    await state.submitRecover()
    expect(api.confirmServiceRecovery).not.toHaveBeenCalled()
    expect(state.recoverSubmitting).toBe(false)
  })

  it.each(['provision', 'renew', 'upgrade'])('%s 正确执行主机校验并提交去空格证据', async (kind) => {
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    state.recoverSummary!.kind = kind
    fillRecovery(state)
    state.recoverForm.verified_host_id = ''
    state.recoverForm.evidence = '  已经逐项核对上游账单和实例状态  '
    await state.submitRecover()
    if (kind !== 'provision') {
      expect(api.confirmServiceRecovery).not.toHaveBeenCalled()
      state.recoverForm.verified_host_id = 123
      await state.submitRecover()
    }
    expect(api.confirmServiceRecovery).toHaveBeenCalledWith(1, expect.objectContaining({
      verified_host_id: kind === 'provision' ? 0 : 123, evidence: '已经逐项核对上游账单和实例状态',
    }))
  })

  it('卸载后禁止再次打开或提交', async () => {
    const state = mountRecovery()
    await state.openRecover(recoveryRow(1))
    fillRecovery(state)
    unmount()
    await state.submitRecover()
    await state.openRecover(recoveryRow(2))
    expect(api.fetchServiceRecovery).toHaveBeenCalledTimes(1)
    expect(api.confirmServiceRecovery).not.toHaveBeenCalled()
  })
})

describe('搜索、分页和后台刷新', () => {
  it.each(['Orders', 'Users', 'Tickets'])('%s 搜索/分页/排序均发出新查询而不被加载锁丢弃', async (name) => {
    const fetcher = cases.find(([key]) => key === name)![1]
    const pending = deferred()
    api[fetcher].mockReturnValueOnce(pending.promise)
    const state = mount(name)
    state.searchForm.q = '新关键词'
    state.handleSearch()
    if (name === 'Tickets') state.handlePageChange(2)
    else state.handleCurrentChange(2)
    state.handleSortChange({ prop: 'id', order: 'ascending' })
    await flush()
    expect(api[fetcher]).toHaveBeenCalledTimes(4)
    if (name === 'Tickets') expect(api[fetcher]).toHaveBeenLastCalledWith(expect.objectContaining({ q: '新关键词', page: 1, sort: 'id', order: 'asc' }))
    else expect(api[fetcher]).toHaveBeenLastCalledWith('新关键词', 1, 'id', 'asc')
    pending.resolve(result(name, 9))
    await flush()
    expect(state.list).toEqual([{ id: 0 }])
  })

  it('工单统计慢响应也必须服从列表代次', async () => {
    const oldStats = deferred()
    api.fetchAdminTicketStats.mockReturnValueOnce(oldStats.promise).mockResolvedValueOnce({ pending: 2 })
    const state = mount('Tickets')
    await flush()
    await state.load()
    oldStats.resolve({ pending: 1 })
    await flush()
    expect(state.stats).toEqual({ pending: 2 })
  })

  it('服务慢轮询不重叠，但新搜索立即执行，旧轮询不能覆盖', async () => {
    const state = mount('Services')
    await flush()
    const poll = deferred()
    api.fetchAdminServices.mockReturnValueOnce(poll.promise)
    await vi.advanceTimersByTimeAsync(10000)
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(2)
    state.searchForm.q = '新条件'
    state.handleSearch()
    await flush()
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(3)
    expect(api.fetchAdminServices).toHaveBeenLastCalledWith(expect.objectContaining({ q: '新条件' }))
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(3)
    poll.resolve(result('Services', 9))
    await flush()
    expect(state.list).toEqual([{ id: 0 }])
    expect(vi.getTimerCount()).toBe(1)
    await vi.advanceTimersByTimeAsync(10000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(4)
  })

  it('服务轮询失败后继续，静默失败不提示', async () => {
    mount('Services')
    await flush()
    api.fetchAdminServices.mockRejectedValueOnce(new Error('轮询失败'))
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(3)
    expect(messages.error).not.toHaveBeenCalled()
  })

  it.each(['补刷已安排', '操作仍在途'])('卸载清理服务补刷：%s', async (phase) => {
    const state = mount('Services')
    await flush()
    const pending = deferred()
    if (phase === '操作仍在途') post.mockReturnValueOnce(pending.promise)
    const action = state.act({ id: 1 }, 'retry')
    await flush()
    unmount()
    const calls = api.fetchAdminServices.mock.calls.length
    pending.resolve({ ok: 1 })
    await action
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.fetchAdminServices).toHaveBeenCalledTimes(calls)
    expect(vi.getTimerCount()).toBe(0)
  })
})
