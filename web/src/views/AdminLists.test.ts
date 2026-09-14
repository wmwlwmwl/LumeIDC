import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as Vue from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as table from '../admin/useAdminTable'
import * as labels from '../utils/admin-labels'

const messages = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn(), warning: vi.fn() }))
vi.mock('element-plus', () => ({ ElMessage: messages }))
const sources = import.meta.glob('./Admin*.vue', { eager: true, query: '?raw', import: 'default' }) as Record<string, string>
const api: Record<string, ReturnType<typeof vi.fn>> = {}
const post = vi.fn()
const route = Vue.reactive({ params: { id: '1' } })
const cases = [
  ['Orders', 'fetchOrders'], ['Users', 'fetchUsers'], ['Tickets', 'fetchAdminTickets'],
  ['Services', 'fetchAdminServices'], ['CancelRequests', 'fetchAdminCancelRequests'],
  ['Announcements', 'fetchAdminAnnouncements'], ['Coupons', 'fetchAdminCoupons'],
  ['Gateways', 'fetchAdminGateways'], ['Products', 'fetchAdminProducts'],
  ['Servers', 'fetchAdminServers'], ['Types', 'fetchAdminTypes'],
  ['Verifications', 'fetchAdminVerifications'], ['Logs', 'fetchAdminLogs'],
  ['Refunds', 'fetchAdminRefunds'], ['Catalog', 'fetchServerCatalog'],
] as const

// 只编译真实 setup 并原生挂载，模板及网络边界不参与竞态回归。
const components = Object.fromEntries(cases.map(([name]) => {
  const { descriptor } = parse(sources[`./Admin${name}.vue`])
  const script = compileScript(descriptor, { id: name, genDefaultAs: 'Component' })
  const compiled = ts.transpileModule(script.content, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const modules: Record<string, unknown> = {
    vue: Vue, 'vue-router': { useRouter: () => ({ push: vi.fn() }), useRoute: () => route },
    'element-plus': { ElMessage: messages, ElMessageBox: { confirm: vi.fn().mockResolvedValue(true) } },
    '@element-plus/icons-vue': {}, '../admin/api': api, '../admin/useAdminTable': table,
    '../http/index': { http: { post } }, '../utils/admin-labels': labels,
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
