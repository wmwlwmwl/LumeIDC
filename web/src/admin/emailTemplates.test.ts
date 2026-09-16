import { describe, expect, it, vi } from 'vitest'
import { emailPreviewDocument, saveEmailTemplate, setEmailMasterEnabled, testEmailTemplate } from './emailTemplates'
import { http } from '../http/index'

vi.mock('../http/index', () => ({ http: { post: vi.fn(), get: vi.fn() } }))

describe('邮件模板接口和安全预览', () => {
  it('保留后端渲染文本与排版，移除导航、脚本和外部资源能力', () => {
    const html = emailPreviewDocument(`<!doctype html><html><head><meta http-equiv="refresh" content="0;url=https://example.com"><base href="https://example.com"><style>@import 'https://example.com/a.css';</style></head><body><h1>测试邮件</h1><p style="color:red" onclick="alert(1)">后端渲染正文 &lt;示例&gt;</p><a href="https://example.com" target="_top" ping="https://example.com">链接文字</a><form action="https://example.com"><input name="secret"></form><iframe src="https://example.com"></iframe><img src="https://example.com/a.png"><script>alert(1)</script><svg><a href="https://example.com">图形</a></svg></body></html>`)
    const doc = new DOMParser().parseFromString(html, 'text/html')
    expect(doc.querySelector('h1')?.textContent).toBe('测试邮件')
    expect(doc.querySelector('p')?.textContent).toBe('后端渲染正文 <示例>')
    expect(doc.querySelector('p')?.getAttribute('style')).toBe('color:red')
    expect(doc.querySelector('script, form, input, iframe, img, svg, base')).toBeNull()
    expect(doc.querySelector('[href], [src], [onclick], [target], [ping]')).toBeNull()
    expect(doc.querySelector('meta[http-equiv="refresh"]')).toBeNull()
    const csp = doc.querySelector('meta[http-equiv="Content-Security-Policy"]')?.getAttribute('content')
    for (const rule of ["default-src 'none'", "connect-src 'none'", "frame-src 'none'", "form-action 'none'", "base-uri 'none'", "script-src 'none'"]) expect(csp).toContain(rule)
  })
  it('不在浏览器中重新渲染模板变量', () => {
    expect(emailPreviewDocument('<p>{{site_name}}</p>')).toContain('{{site_name}}')
  })
  it('业务失败不应被当成保存成功', async () => {
    vi.mocked(http.post).mockResolvedValueOnce({ ok: 0, msg: '保存失败' })
    await expect(saveEmailTemplate({ code: 'auth_code', subject: '标题', body: '正文', enabled: true })).rejects.toThrow('保存失败')
  })
  it('测试发送提交当前草稿而非已保存版本', async () => {
    vi.mocked(http.post).mockResolvedValueOnce({ ok: 1 })
    const draft = { code: 'ticket_reply', subject: '未保存标题', body: '未保存正文', enabled: false }
    await testEmailTemplate('test@example.com', draft)
    expect(http.post).toHaveBeenLastCalledWith('/email-templates/test', { ...draft, to: 'test@example.com' })
  })
  it('总开关关闭失败应抛出业务错误', async () => {
    vi.mocked(http.post).mockResolvedValueOnce({ ok: 0, msg: '保存邮件通知总开关失败，请稍后重试' })
    await expect(setEmailMasterEnabled(false)).rejects.toThrow('保存邮件通知总开关失败')
  })
})
