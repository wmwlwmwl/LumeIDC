import { http, type ApiResult } from '../http/index'
import type { EmailTemplateVariable } from './emailTemplates'

export type SMSRange = 'cn' | 'global' | 'marketing'
export type SMSRemoteAction = 'create' | 'update' | 'query' | 'delete'
export interface SMSProviderDescriptor {
  key: string; name: string; config_fields: string[]
  capabilities: { ranges: SMSRange[]; otp: boolean; notification: boolean; template_crud: boolean; audit_sync: boolean }
}
export interface SMSTemplate {
  id: number; name: string; provider: string; kind: 'otp' | 'notification'
  template_code: string; content: string; parameters: Record<string, string>; enabled: boolean
  range_type?: SMSRange; remark?: string; sign_name?: string
  remote_template_id?: string; audit_status?: string; audit_message?: string; audit_updated_at?: string; template_type?: string
  remote_operation?: string
}
export interface SMSBinding { code: string; template_id: number; enabled: boolean }
export interface SMSScene extends SMSBinding {
  name: string; kind: 'otp' | 'notification'; required: boolean; variables: EmailTemplateVariable[]
}
export interface SMSPreview { content: string; template_code: string; parameters: Record<string, string>; provider: string; range_type?: SMSRange; sign_name?: string }
export interface SMSDelivery {
  id: number; scene: string; recipient: string; status: string; message: string; provider: string
  provider_message_id: string; request_id: string; error_code: string; created_at: string
}
export const smsProviders: Record<string, string> = {
  aliyun: '阿里云号码认证（PNVS）', aliyun_sms: '阿里云短信', qcloudsms: '腾讯云短信', submail: '赛邮',
  smsbao: '短信宝', idcsmart: '智简魔方', idcsmartpro: '智简魔方国内营销', stay33: 'Stay33',
}
export const smsRanges: Record<SMSRange, string> = { cn: '国内', global: '国际', marketing: '国内营销' }
export const smsAuditStatuses: Record<string, string> = {
  pending: '供应商审核中', approved: '供应商审核通过', rejected: '供应商审核未通过', unknown: '审核状态未知', not_supported: '不支持审核同步',
}
export const smsRemoteActions: Record<SMSRemoteAction, string> = { create: '远程创建', update: '远程修改', query: '查询审核', delete: '远程删除' }
export const smsStatuses: Record<string, string> = {
  sent: '服务商已受理', unknown: '待核查（不自动重发）', failed: '失败', skipped: '已跳过', pending: '待处理', running: '发送中',
}
function checked<T extends ApiResult>(result: T): T {
  if (!result.ok) throw new Error('短信模板操作失败，请重试')
  return result
}
export async function fetchSMSProviders(): Promise<SMSProviderDescriptor[]> {
  return checked(await http.get<ApiResult & { list: SMSProviderDescriptor[] }>('/sms-providers')).list || []
}
export async function fetchSMSTemplates(): Promise<SMSTemplate[]> {
  return checked(await http.get<ApiResult & { list: SMSTemplate[] }>('/sms-templates')).list || []
}
export async function fetchSMSScenes(): Promise<SMSScene[]> {
  return checked(await http.get<ApiResult & { list: SMSScene[] }>('/sms-scenes')).list || []
}
export async function fetchSMSDeliveries(): Promise<SMSDelivery[]> {
  return (checked(await http.get<ApiResult & { list: SMSDelivery[] }>('/sms-deliveries')).list || []).slice(0, 100)
}
export function smsTemplateDTO(template: SMSTemplate) {
  const { id, name, provider, kind, template_code, content, parameters, enabled, range_type = 'cn', remark = '', sign_name = '' } = template
  return { id, name, provider, kind, template_code, content, parameters: { ...parameters }, enabled, range_type, remark, sign_name }
}
export async function saveSMSTemplate(template: SMSTemplate): Promise<number> {
  return checked(await http.post<ApiResult & { id: number }>('/sms-templates/save', smsTemplateDTO(template))).id
}
export async function remoteSMSTemplate(id: number, action: SMSRemoteAction) {
  checked(await http.post('/sms-templates/remote', { id, action, confirm: true }))
}
// 管理员核对供应商控制台后手动解锁；后端不会自动重放任何远程操作。
export async function unlockSMSTemplate(id: number) {
  checked(await http.post('/sms-templates/unlock', { id, confirm: true }))
}
export async function deleteSMSTemplate(id: number) {
  checked(await http.post('/sms-templates/delete', { id, confirm: true }))
}
export async function saveSMSBinding(binding: SMSBinding) {
  checked(await http.post('/sms-scenes/save', { ...binding }))
}
export async function previewSMSTemplate(template: SMSTemplate, scene: string): Promise<SMSPreview> {
  return checked(await http.post<ApiResult & { preview: SMSPreview }>('/sms-templates/preview', { template: smsTemplateDTO(template), scene })).preview
}
export function smsSupports(template: SMSTemplate, descriptor?: SMSProviderDescriptor): boolean {
  const range = template.range_type || 'cn'
  return !!descriptor && descriptor.key === template.provider && descriptor.capabilities.ranges.includes(range) &&
    descriptor.capabilities[template.kind] && !(range === 'marketing' && template.kind === 'otp')
}
export function smsRemoteSupported(template: SMSTemplate, descriptor?: SMSProviderDescriptor): boolean {
  return smsSupports(template, descriptor) && !!descriptor?.capabilities.template_crud &&
    !(template.provider === 'submail' && template.range_type === 'global')
}
export function canBindSMS(template: SMSTemplate, scene: SMSScene, provider: string, descriptor?: SMSProviderDescriptor): boolean {
  return template.enabled && template.provider === provider && template.kind === scene.kind &&
    !(template.range_type === 'marketing' && scene.kind === 'otp') &&
    (template.provider !== 'aliyun' || scene.kind === 'otp') && (!descriptor || smsSupports(template, descriptor))
}
export function smsParameterMap(rows: { name: string; variable: string }[], provider?: string): Record<string, string> {
  const entries = rows.map(row => [row.name.trim(), row.variable] as const)
  if (entries.length > 20 || entries.some(([name, variable]) => !/^[A-Za-z0-9_]{1,64}$/.test(name) || !variable) || new Set(entries.map(([name]) => name)).size !== entries.length) {
    throw new Error('请填写有效且不重复的供应商参数名，并选择业务变量（最多20项）')
  }
  if (provider === 'qcloudsms') {
    const numbers = entries.map(([name]) => /^(?:param)?([1-9]\d*)$/.exec(name)?.[1])
    if (numbers.some(number => !number || Number(number) > entries.length) || new Set(numbers).size !== entries.length) {
      throw new Error('腾讯云参数须从1连续编号，可用1、2或param1、param2，不能混用重复编号')
    }
  }
  return Object.fromEntries(entries)
}
