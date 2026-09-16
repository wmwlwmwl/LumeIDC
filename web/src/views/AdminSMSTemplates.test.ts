import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import * as Vue from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import source from './AdminSMSTemplates.vue?raw'
import * as smsModule from '../admin/smsTemplates'
import type { SMSProviderDescriptor, SMSTemplate, SMSScene, SMSBinding } from '../admin/smsTemplates'

const confirm = vi.fn()
const routeGuard = vi.fn()
// 真实模块只用于纯函数与常量，涉及网络的方法统一替换为桩。
const api = {
  ...smsModule,
  fetchSMSTemplates: vi.fn(), fetchSMSScenes: vi.fn(), fetchSMSDeliveries: vi.fn(), saveSMSTemplate: vi.fn(),
  deleteSMSTemplate: vi.fn(), saveSMSBinding: vi.fn(), previewSMSTemplate: vi.fn(),
  fetchSMSProviders: vi.fn(), remoteSMSTemplate: vi.fn(), unlockSMSTemplate: vi.fn(),
}
const providerDescriptor: SMSProviderDescriptor = {
  key: 'stay33', name: 'Stay33', config_fields: ['sms_username'],
  capabilities: { ranges: ['cn'], otp: true, notification: true, template_crud: false, audit_sync: false },
}
const messages = { error: vi.fn(), success: vi.fn(), warning: vi.fn() }
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'sms-templates', genDefaultAs: 'Component' })
const template = compileTemplate({ source: descriptor.template!.content, filename: 'AdminSMSTemplates.vue', id: 'sms-templates', compilerOptions: { bindingMetadata: script.bindings } })
const compiled = ts.transpileModule(script.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const component = new Function('require', 'exports', `${compiled}\nreturn Component`)((id: string) => ({
  vue: Vue, 'vue-router': { onBeforeRouteLeave: routeGuard }, 'element-plus': { ElMessage: messages, ElMessageBox: { confirm } },
  '../admin/api': { fetchAdminSettings: vi.fn().mockResolvedValue({ sms_provider: 'stay33' }) }, '../admin/smsTemplates': api,
})[id], {}) as Vue.Component
const item: SMSTemplate = { id: 1, name: '验证码', provider: 'stay33', kind: 'otp', template_code: '', content: '{{code}}', parameters: {}, enabled: true }
const scene: SMSScene = { code: 'otp_login', name: '登录验证码', kind: 'otp', required: true, variables: [{ key: 'code', label: '验证码', example: '123456' }], template_id: 1, enabled: true }
type State = {
  draft: SMSTemplate | null; templates: SMSTemplate[]; scenes: SMSScene[]; bindings: SMSBinding[]; dirty: boolean; busy: string; tab: string;
  edit: (value: SMSTemplate) => void; select: (value: SMSTemplate) => Promise<void>; closeEditor: () => Promise<void>;
  save: () => Promise<void>; remove: () => Promise<void>; beforeUnload: (event: BeforeUnloadEvent) => void;
  changeTab: (value: string) => Promise<void>; saveBinding: (binding: SMSBinding, unbind?: boolean) => Promise<void>;
  changeProvider: (value: string) => Promise<void>; create: () => Promise<void>;
  remote: (action: 'create' | 'update' | 'query' | 'delete') => Promise<void>; unlock: () => Promise<void>;
}
let app: Vue.App | undefined
async function mount() {
  app = Vue.createApp({ ...component, render: () => null })
  const vm = app.mount(document.createElement('div'))
  await new Promise(resolve => setTimeout(resolve, 0))
  return (vm.$ as unknown as { setupState: State }).setupState
}
beforeEach(() => {
  vi.clearAllMocks()
  api.fetchSMSTemplates.mockResolvedValue([{ ...item }])
  api.fetchSMSScenes.mockResolvedValue([{ ...scene }])
  api.fetchSMSDeliveries.mockResolvedValue([])
  api.fetchSMSProviders.mockResolvedValue([{ ...providerDescriptor }])
  confirm.mockResolvedValue(true)
})
afterEach(() => { app?.unmount(); app = undefined })

describe('短信模板交互与草稿保护', () => {
  it('可编译，仅文本预览且变量按钮覆盖全局固定高度', () => {
    expect(template.errors).toEqual([])
    expect(source).not.toContain('v-html')
    expect(source).not.toContain('innerHTML')
    expect(source).toContain('height: auto !important')
    expect(source).toContain('{{ previewResult.content }}')
  })
  it('保存失败保留草稿，保存成功更新本地列表和基线', async () => {
    const state = await mount()
    state.edit(item)
    state.draft!.content = '验证码{{code}}'
    api.saveSMSTemplate.mockRejectedValueOnce(new Error('失败'))
    await state.save()
    expect(state.draft!.content).toBe('验证码{{code}}')
    expect(state.dirty).toBe(true)
    expect(state.templates[0].content).toBe('{{code}}')
    api.saveSMSTemplate.mockResolvedValueOnce(1)
    await state.save()
    expect(state.dirty).toBe(false)
    expect(state.templates[0].content).toBe('验证码{{code}}')
  })
  it('模板切换、分栏、关闭和路由均保护未保存内容', async () => {
    const state = await mount()
    state.edit(item)
    state.draft!.name = '草稿'
    confirm.mockRejectedValue('取消')
    await state.select({ ...item, id: 2 })
    await state.changeTab('scenes')
    await state.closeEditor()
    expect(state.draft!.id).toBe(1)
    expect(state.draft!.name).toBe('草稿')
    expect(state.tab).toBe('templates')
    expect(await routeGuard.mock.calls[0][0]()).toBe(false)
    const event = new Event('beforeunload', { cancelable: true }) as BeforeUnloadEvent
    state.beforeUnload(event)
    expect(event.defaultPrevented).toBe(true)
  })
  it('新建空草稿也保护，取消服务商切换不清空内容', async () => {
    const state = await mount()
    await state.create()
    expect(state.dirty).toBe(true)
    state.draft!.content = '草稿{{code}}'
    confirm.mockRejectedValueOnce('取消')
    await state.changeProvider('aliyun_sms')
    expect(state.draft!.provider).toBe('stay33')
    expect(state.draft!.content).toBe('草稿{{code}}')
  })
  it('正在绑定不能删除，未绑定删除仍须确认', async () => {
    const state = await mount()
    state.edit(item)
    await state.remove()
    expect(api.deleteSMSTemplate).not.toHaveBeenCalled()
    state.scenes = []
    confirm.mockRejectedValueOnce('取消')
    await state.remove()
    expect(api.deleteSMSTemplate).not.toHaveBeenCalled()
  })
  it('OTP解绑明确警告并回退true，通知解绑关闭；失败保留草稿', async () => {
    const state = await mount()
    await state.saveBinding(state.bindings[0], true)
    expect(confirm.mock.calls[0][0]).toContain('回退通知设置中的旧通道验证码配置')
    expect(api.saveSMSBinding).toHaveBeenLastCalledWith({ code: scene.code, template_id: 0, enabled: true })
    const notice = { ...scene, code: 'ticket_reply', kind: 'notification' as const, required: false }
    state.scenes = [notice]
    state.bindings = [{ code: notice.code, template_id: 1, enabled: true }]
    await state.saveBinding(state.bindings[0], true)
    expect(api.saveSMSBinding).toHaveBeenLastCalledWith({ code: notice.code, template_id: 0, enabled: false })
    state.bindings[0].template_id = 2
    state.bindings[0].enabled = true
    api.saveSMSBinding.mockRejectedValueOnce(new Error('失败'))
    await state.saveBinding(state.bindings[0])
    expect(state.bindings[0].template_id).toBe(2)
    expect(state.dirty).toBe(true)
  })
  it('远程锁定模板禁止保存、删除和重复远程操作，确认后可解锁', async () => {
    const state = await mount()
    state.edit({ ...item, remote_operation: 'create' })
    await state.save()
    expect(api.saveSMSTemplate).not.toHaveBeenCalled()
    await state.remove()
    expect(api.deleteSMSTemplate).not.toHaveBeenCalled()
    await state.remote('query')
    expect(api.remoteSMSTemplate).not.toHaveBeenCalled()
    confirm.mockRejectedValueOnce('取消')
    await state.unlock()
    expect(api.unlockSMSTemplate).not.toHaveBeenCalled()
    api.unlockSMSTemplate.mockResolvedValueOnce(undefined)
    confirm.mockResolvedValueOnce(true)
    await state.unlock()
    expect(api.unlockSMSTemplate).toHaveBeenCalledWith(1)
  })
  it('未保存绑定切换分栏时确认，取消解绑不调用接口', async () => {
    const state = await mount()
    state.bindings[0].template_id = 2
    expect(state.dirty).toBe(true)
    confirm.mockRejectedValue('取消')
    await state.changeTab('deliveries')
    await state.saveBinding(state.bindings[0], true)
    expect(state.tab).toBe('templates')
    expect(state.bindings[0].template_id).toBe(2)
    expect(api.saveSMSBinding).not.toHaveBeenCalled()
  })
})
