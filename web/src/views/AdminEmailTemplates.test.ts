import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as Vue from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import source from './AdminEmailTemplates.vue?raw'

const confirm = vi.fn()
const api = {
  fetchEmailTemplates: vi.fn(), saveEmailTemplate: vi.fn(), resetEmailTemplate: vi.fn(),
  previewEmailTemplate: vi.fn(), testEmailTemplate: vi.fn(), setEmailMasterEnabled: vi.fn(),
  emailPreviewDocument: vi.fn((body: string) => body),
}
const messages = { error: vi.fn(), success: vi.fn(), warning: vi.fn() }
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'email-templates', genDefaultAs: 'Component' })
const template = compileTemplate({ source: descriptor.template!.content, filename: 'AdminEmailTemplates.vue', id: 'email-templates', compilerOptions: { bindingMetadata: script.bindings } })
const compiled = ts.transpileModule(script.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const component = new Function('require', 'exports', `${compiled}\nreturn Component`)((id: string) => ({
  vue: Vue, 'vue-router': { onBeforeRouteLeave: vi.fn() }, 'element-plus': { ElMessage: messages, ElMessageBox: { confirm } },
  '../http/index': { ApiError: class extends Error {} }, '../admin/emailTemplates': api,
})[id], {}) as Vue.Component
const item = { code: 'ticket_reply', subject: '原标题', body: '<p>原正文</p>', enabled: true, name: '工单回复', category: '工单', required: false, custom: false, variables: [] }
type State = {
  list: typeof item[]; draft: { subject: string; body: string }; selected: typeof item | null; dirty: boolean; busy: string;
  emailEnabled: boolean; toggleMaster: (next: boolean) => Promise<void>;
  edit: (value: typeof item) => void; select: (value: typeof item) => Promise<void>; closeEditor: () => Promise<void>;
  save: () => Promise<void>; reset: () => Promise<void>; beforeUnload: (event: BeforeUnloadEvent) => void;
}
let app: Vue.App | undefined
function mount() {
  app = Vue.createApp({ ...component, render: () => null })
  const vm = app.mount(document.createElement('div'))
  return (vm.$ as unknown as { setupState: State }).setupState
}
beforeEach(() => {
  vi.clearAllMocks()
  api.fetchEmailTemplates.mockResolvedValue({ list: [{ ...item }], emailEnabled: true })
  api.setEmailMasterEnabled.mockResolvedValue(undefined)
  confirm.mockResolvedValue(true)
})
afterEach(() => { app?.unmount(); app = undefined })

describe('邮件模板草稿保护', () => {
  it('模板可编译且预览只使用无权限沙箱', () => {
    expect(template.errors).toEqual([])
    expect(source).toContain('sandbox=""')
    expect(source).not.toContain('v-html')
    expect(source).not.toContain('allow-scripts')
    expect(source).not.toContain('allow-same-origin')
  })
  it('保存失败保留草稿和未保存状态', async () => {
    api.saveEmailTemplate.mockRejectedValueOnce(new Error('失败'))
    const state = mount()
    state.edit({ ...item })
    state.draft.subject = '尚未保存标题'
    await state.save()
    expect(state.draft.subject).toBe('尚未保存标题')
    expect(state.dirty).toBe(true)
    expect(state.selected?.subject).toBe('原标题')
    expect(state.busy).toBe('')
  })
  it('取消切换或关闭时保留编辑器与草稿', async () => {
    const state = mount()
    state.edit({ ...item })
    state.draft.body = '未保存正文'
    confirm.mockRejectedValueOnce('取消')
    await state.select({ ...item, code: 'payment_success' })
    expect(state.selected?.code).toBe(item.code)
    confirm.mockRejectedValueOnce('取消')
    await state.closeEditor()
    expect(state.selected?.code).toBe(item.code)
    expect(state.draft.body).toBe('未保存正文')
  })
  it('恢复默认必须明确确认，取消不调用接口', async () => {
    const state = mount()
    state.edit({ ...item })
    confirm.mockRejectedValueOnce('取消')
    await state.reset()
    expect(api.resetEmailTemplate).not.toHaveBeenCalled()
  })
  it('保存成功后清除未保存状态', async () => {
    api.saveEmailTemplate.mockResolvedValueOnce(undefined)
    const state = mount()
    state.edit({ ...item })
    state.draft.subject = '新标题'
    await state.save()
    expect(state.dirty).toBe(false)
    expect(state.selected?.subject).toBe('新标题')
  })
  it('未保存时阻止浏览器直接关闭', () => {
    const state = mount()
    state.edit({ ...item })
    state.draft.subject = '未保存'
    const event = new Event('beforeunload', { cancelable: true }) as BeforeUnloadEvent
    state.beforeUnload(event)
    expect(event.defaultPrevented).toBe(true)
  })
  it('总开关加载后按服务端状态显示', async () => {
    const state = mount()
    await Vue.nextTick()
    await Promise.resolve()
    expect(state.emailEnabled).toBe(true)
  })
  it('总开关保存失败时回滚显示并提示', async () => {
    const state = mount()
    api.setEmailMasterEnabled.mockRejectedValueOnce(new Error('失败'))
    state.emailEnabled = false
    await state.toggleMaster(false)
    expect(api.setEmailMasterEnabled).toHaveBeenCalledWith(false)
    expect(state.emailEnabled).toBe(true)
    expect(messages.error).toHaveBeenCalled()
  })
})
