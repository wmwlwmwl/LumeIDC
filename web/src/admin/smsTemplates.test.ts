import { beforeEach, describe, expect, it, vi } from 'vitest'
import { canBindSMS, smsParameterMap, smsTemplateDTO, fetchSMSTemplates, fetchSMSScenes, fetchSMSDeliveries, saveSMSTemplate, deleteSMSTemplate, saveSMSBinding, previewSMSTemplate, unlockSMSTemplate, type SMSTemplate, type SMSScene } from './smsTemplates'
const http = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../http/index', () => ({ http }))
const template: SMSTemplate = { id: 1, name: '验证码', provider: 'stay33', kind: 'otp', template_code: '', content: '{{code}}', parameters: {}, enabled: true }
const scene: SMSScene = { code: 'otp_login', name: '登录验证码', kind: 'otp', required: true, variables: [], template_id: 0, enabled: true }
beforeEach(() => { vi.clearAllMocks() })
describe('短信模板契约', () => {
  it('仅当前通道、类型相同且启用的模板可绑定，PNVS不支持通知', () => {
    expect(canBindSMS(template, scene, 'stay33')).toBe(true)
    expect(canBindSMS(template, scene, 'aliyun_sms')).toBe(false)
    expect(canBindSMS({ ...template, enabled: false }, scene, 'stay33')).toBe(false)
    expect(canBindSMS(template, { ...scene, kind: 'notification' }, 'stay33')).toBe(false)
    expect(canBindSMS({ ...template, provider: 'aliyun', kind: 'notification' }, { ...scene, kind: 'notification' }, 'aliyun')).toBe(false)
  })
  it('映射方向为供应商参数名到业务变量，重复/空值不能静默覆盖', () => {
    expect(smsParameterMap([{ name: ' vendor_code ', variable: 'code' }])).toEqual({ vendor_code: 'code' })
    for (const rows of [[{ name: '', variable: 'code' }], [{ name: 'code', variable: '' }], [{ name: 'a', variable: 'code' }, { name: 'a ', variable: 'purpose' }]]) {
      expect(() => smsParameterMap(rows)).toThrow()
    }
    const result = smsParameterMap([{ name: '__proto__', variable: 'code' }])
    expect(Object.getPrototypeOf(result)).toBe(Object.prototype)
    expect(Object.hasOwn(result, '__proto__')).toBe(true)
  })
  it('列表沿用后台相对路径并限制100条投递记录', async () => {
    http.get.mockResolvedValueOnce({ ok: 1, list: [template] }).mockResolvedValueOnce({ ok: 1, list: [scene] }).mockResolvedValueOnce({ ok: 1, list: Array.from({ length: 110 }, (_, id) => ({ id })) })
    expect(await fetchSMSTemplates()).toEqual([template])
    expect(await fetchSMSScenes()).toEqual([scene])
    expect(await fetchSMSDeliveries()).toHaveLength(100)
    expect(http.get.mock.calls.map(call => call[0])).toEqual(['/sms-templates', '/sms-scenes', '/sms-deliveries'])
  })
  it('保存、删除、预览和解绑请求遵守公共API，不调用真实试发', async () => {
    http.post.mockResolvedValue({ ok: 1, id: 3, preview: { content: '123456' } })
    expect(await saveSMSTemplate(template)).toBe(3)
    await deleteSMSTemplate(3)
    await saveSMSBinding({ code: scene.code, template_id: 0, enabled: true })
    await previewSMSTemplate(template, scene.code)
    await unlockSMSTemplate(3)
    // 保存与预览只提交后端接受的字段：审核、远程编号和锁定状态均为只读响应字段。
    const payload = smsTemplateDTO(template)
    expect(http.post.mock.calls).toEqual([
      ['/sms-templates/save', payload], ['/sms-templates/delete', { id: 3, confirm: true }],
      ['/sms-scenes/save', { code: scene.code, template_id: 0, enabled: true }],
      ['/sms-templates/preview', { template: payload, scene: scene.code }],
      ['/sms-templates/unlock', { id: 3, confirm: true }],
    ])
  })
  it('保存只提交可写字段，不把只读审核与锁定字段回传后端', async () => {
    http.post.mockResolvedValue({ ok: 1, id: 3 })
    await saveSMSTemplate({ ...template, remote_template_id: '100001', audit_status: 'approved', audit_message: '已通过', remote_operation: 'create' })
    const [, body] = http.post.mock.calls[0]
    expect(Object.keys(body).sort()).toEqual(['content', 'enabled', 'id', 'kind', 'name', 'parameters', 'provider', 'range_type', 'remark', 'sign_name', 'template_code'])
  })
  it('HTTP成功但业务失败时不能误报保存成功', async () => {
    http.post.mockResolvedValue({ ok: 0, msg: '失败' })
    await expect(saveSMSTemplate(template)).rejects.toThrow('短信模板操作失败')
  })
})
