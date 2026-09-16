import { http, type ApiResult } from '../http/index'

export interface EmailTemplateVariable { key: string; label: string; example: string }
export interface EmailTemplateDraft { code: string; subject: string; body: string; enabled: boolean }
export interface EmailTemplate extends EmailTemplateDraft {
  name: string
  category: string
  required: boolean
  custom: boolean
  variables: EmailTemplateVariable[]
}
export interface EmailTemplatePreview { subject: string; body: string }

function checked<T extends ApiResult>(result: T): T {
  if (!result.ok) throw new Error(result.msg || '邮件模板操作失败，请重试')
  return result
}
export async function fetchEmailTemplates(): Promise<{ list: EmailTemplate[]; emailEnabled: boolean }> {
  const res = checked(await http.get<ApiResult & { list: EmailTemplate[]; email_enabled: boolean }>('/email-templates'))
  return { list: res.list || [], emailEnabled: res.email_enabled === true }
}
// 总开关：关闭后业务邮件全部不发，验证码与短信不受影响。
export async function setEmailMasterEnabled(enabled: boolean) {
  return checked(await http.post<ApiResult>('/email-templates/master', { enabled }))
}
export async function saveEmailTemplate(draft: EmailTemplateDraft) {
  checked(await http.post('/email-templates/save', { ...draft }))
}
export async function resetEmailTemplate(code: string) {
  checked(await http.post('/email-templates/reset', { code, confirm: true }))
}
export async function previewEmailTemplate(draft: EmailTemplateDraft): Promise<EmailTemplatePreview> {
  return checked(await http.post<ApiResult & { preview: EmailTemplatePreview }>('/email-templates/preview', { ...draft })).preview
}
export async function testEmailTemplate(to: string, draft: EmailTemplateDraft) {
  checked(await http.post('/email-templates/test', { ...draft, to }))
}

// 仅隔离后端已渲染的 HTML，不解析或替换模板变量。惰性 template 不加载资源。
// CSP 的 navigate-to 并非所有浏览器支持，因此同时只复制无导航能力的标签与属性。
export function emailPreviewDocument(body: string): string {
  const source = document.createElement('template')
  source.innerHTML = body
  const output = document.createElement('div')
  const tags = new Set(['DIV', 'P', 'SPAN', 'A', 'BR', 'HR', 'B', 'STRONG', 'I', 'EM', 'U', 'S', 'SMALL', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'TABLE', 'THEAD', 'TBODY', 'TFOOT', 'TR', 'TD', 'TH', 'UL', 'OL', 'LI', 'BLOCKQUOTE', 'PRE', 'CODE'])
  const attrs = new Set(['style', 'colspan', 'rowspan', 'width', 'height', 'align', 'valign', 'cellpadding', 'cellspacing', 'border', 'dir', 'lang'])
  function copy(node: Node, parent: Node) {
    if (node.nodeType === Node.TEXT_NODE) {
      parent.appendChild(document.createTextNode(node.textContent || ''))
    } else if (node instanceof HTMLElement && tags.has(node.tagName)) {
      const element = document.createElement(node.tagName.toLowerCase())
      for (const attr of Array.from(node.attributes)) {
        if (attrs.has(attr.name)) element.setAttribute(attr.name, attr.value)
      }
      for (const child of Array.from(node.childNodes)) copy(child, element)
      parent.appendChild(element)
    }
  }
  for (const node of Array.from(source.content.childNodes)) copy(node, output)
  return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; media-src 'none'; connect-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; navigate-to 'none'"><meta name="referrer" content="no-referrer"><style>body{overflow-wrap:anywhere;margin:16px}table{max-width:100%}pre{white-space:pre-wrap}</style></head><body>${output.innerHTML}</body></html>`
}
