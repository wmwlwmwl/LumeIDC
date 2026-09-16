import { http } from '../http/index'
import type { ConfigOption } from './store'

// 用户控制台 JSON 接口封装（同 URL，SSR 共存）。

export interface ServiceLite {
  id: number
  name: string
  status: number
  status_text: string
  hostname: string
  ip: string
  os: string
  expires_at: string
  days_left: number
  expiring_soon: boolean
  product_id: number
  monthly: string
  config_desc: string
  show_monthly: boolean
  show_q: boolean
  show_y: boolean
  default_cycle: 'monthly' | 'quarterly' | 'yearly'
  /** 过渡状态：renew_pending=续费人工处理中（此时不允许再次续费） */
  transition: string
  renew_prices?: { monthly: string; quarterly: string; yearly: string }
}

export interface SvcConfig {
  name: string
  value: string
  price: number
}

export interface ServiceDetail {
  id: number
  name: string
  status: number
  status_text: string
  hostname: string
  product_id: number
  remark: string
  expires_at: string
  created_at: string
  cycle: string
  amount: string
  configs: SvcConfig[]
  config_desc: string
  provider: string
  /** 过渡状态：renew_pending=续费人工处理中（此时不允许再次续费） */
  transition: string
}

export interface DetailData {
  ok: number
  csrf: string
  svc: ServiceDetail
  host?: {
    username?: string
    password?: string
    panel_url?: string
    status?: string
    os?: string
    ip?: string
    additional_ips?: string[]
    bw_limit?: string
    bw_usage?: string
    datacenter?: string
    os_version?: string
    port?: string
    web_quota?: string
    db_name?: string
    db_quota?: string
    db_used?: string
    ftp?: boolean
    domain?: string
    flow_limit?: string
    speed_limit?: string
    create_time?: string
  }
  show_monthly: boolean
  show_q: boolean
  show_y: boolean
  default_cycle: 'monthly' | 'quarterly' | 'yearly'
  renew_prices?: { monthly: string; quarterly: string; yearly: string }
  can_upgrade: boolean
  /** 上游模块方块 key 列表（nat_acl/nat_web/security_groups/setting/snapshot…），空 = 无面板 */
  modules: string[]
}

export async function fetchServices(): Promise<ServiceLite[]> {
  const res = await http.get<{ ok: number; list: ServiceLite[] }>('/services')
  return (res.list || []) as unknown as ServiceLite[]
}

export async function fetchServiceDetail(id: string | number): Promise<DetailData> {
  const res = await http.get<DetailData>(`/services/${id}`)
  return res as unknown as DetailData
}

export async function refreshServiceHost(id: number): Promise<ActionResult> {
  return (await http.post<ActionResult>(`/services/${id}/refresh`, {})) as unknown as ActionResult
}

export interface ActionResult {
  ok: number
  msg?: string
  invoice_id?: number
  redirect?: string
  code?: string
  /** true=已当场核销（0 元单），无需再付款 */
  paid?: boolean
}

export async function consoleAction(id: number, body: Record<string, unknown>): Promise<ActionResult> {
  const res = await http.post<ActionResult>(`/services/${id}/console`, body)
  return res as unknown as ActionResult
}

export async function renewService(id: number, cycle: string): Promise<ActionResult> {
  return (await http.post<ActionResult>(`/services/${id}/renew`, { cycle })) as unknown as ActionResult
}

export async function renameService(id: number, name: string, remark: string): Promise<ActionResult> {
  return (await http.post<ActionResult>(`/services/${id}/name`, { name, remark })) as unknown as ActionResult
}

export interface CancelRequestInfo {
  id: number
  type: string
  reason: string
  reason_detail: string
  created_at: string
}
export interface CancelRequestState {
  ok: number
  reasons: string[]
  request: CancelRequestInfo | null
}
export async function fetchCancelRequestInfo(id: number | string): Promise<CancelRequestState> {
  const res = await http.get<CancelRequestState>(`/services/${id}/cancel-request`)
  return res as unknown as CancelRequestState
}
export async function submitCancelRequest(
  id: number | string,
  body: { type: string; reason: string; reason_detail: string },
): Promise<ActionResult> {
  return (await http.post<ActionResult>(`/services/${id}/cancel-request`, body)) as unknown as ActionResult
}
export async function withdrawCancelRequest(id: number | string): Promise<ActionResult> {
  return (await http.post<ActionResult>(`/services/${id}/cancel-request/withdraw`, {})) as unknown as ActionResult
}

// ---- 升降级 ----
export interface UpgradeTarget {
  product_id: number
  upstream_pid: number
  name: string
}
export interface UpgradeForm {
  ok: number
  svc: { id: number; name: string; status: number; status_text: string }
  current_monthly: string
  targets: UpgradeTarget[]
  target?: {
    id: number
    name: string
    monthly: string
    base: Record<string, number>
    options: ConfigOption[]
    profit_type: number
    profit_value: number
  }
  show_q?: boolean
  show_y?: boolean
}

export async function fetchUpgradeForm(id: number, targetId?: number): Promise<UpgradeForm> {
  const res = await http.get<UpgradeForm>(`/services/${id}/upgrade`, targetId ? { target: targetId } : undefined)
  return res as unknown as UpgradeForm
}

export async function upgradeService(
  id: number,
  targetProductId: number,
  cycle: string,
  selection: Record<string, string>,
): Promise<ActionResult> {
  const body: Record<string, unknown> = { target_product_id: targetProductId, cycle }
  for (const [k, v] of Object.entries(selection)) {
    if (v !== '') body[`cfg_${k}`] = v
  }
  return (await http.post<ActionResult>(`/services/${id}/upgrade`, body)) as unknown as ActionResult
}

// ---- 监控：流量用量 / 每日流量曲线 ----
export interface UsageInfo {
  traffic_used: number
  traffic_limit: number
}
export interface TrafficDay {
  time: string
  in: number
  out: number
}

export async function fetchUsage(id: number): Promise<UsageInfo> {
  const res = await http.get<{ ok: number; usage?: UsageInfo }>(`/services/${id}/usage`)
  return (res.usage || { traffic_used: 0, traffic_limit: 0 }) as unknown as UsageInfo
}
export async function fetchTraffic(id: number): Promise<TrafficDay[]> {
  const res = await http.get<{ ok: number; days?: TrafficDay[] }>(`/services/${id}/traffic`)
  return (res.days || []) as unknown as TrafficDay[]
}

// 资源监控时序（cpu / memory / disk / flow）
export interface ChartPoint {
  time: string
  value: number
}
export interface ChartLine {
  label: string
  points: ChartPoint[]
}
export interface ChartSeries {
  type: string
  unit: string
  lines: ChartLine[]
}
export async function fetchChart(id: number, type: string, range: string): Promise<ChartSeries> {
  const res = await http.get<{ ok: number; series?: ChartSeries }>(`/services/${id}/chart`, { type, range })
  return (res.series || { type, unit: '', lines: [] }) as unknown as ChartSeries
}

// ---- 控制台操作（重装 / 重置密码 / 救援） ----
export interface OSOption {
  id: string
  name: string
  group?: string
}

export async function fetchReinstallOptions(id: number): Promise<OSOption[]> {
  const res = await http.get<{ ok: number; os?: OSOption[] }>(`/services/${id}/reinstall-options`)
  return (res.os || []) as unknown as OSOption[]
}

export async function fetchRescueState(id: number): Promise<boolean> {
  const res = await http.get<{ ok: number; rescue?: boolean }>(`/services/${id}/rescue-state`)
  return !!res.rescue
}

export async function reinstallService(id: number, os: string): Promise<ActionResult> {
  return (await http.post<ActionResult>(`/services/${id}/console`, { do: 'reinstall', os })) as unknown as ActionResult
}
export async function resetServicePassword(id: number, password: string): Promise<ActionResult> {
  return (await http.post<ActionResult>(`/services/${id}/console`, { do: 'crack_pass', password })) as unknown as ActionResult
}
export async function rescueService(id: number, system: string, tempPass: string): Promise<ActionResult> {
  return (await http.post<ActionResult>(`/services/${id}/console`, { do: 'rescue', system, temp_pass: tempPass })) as unknown as ActionResult
}
export async function exitRescueService(id: number): Promise<ActionResult> {
  return (await http.post<ActionResult>(`/services/${id}/console`, { do: 'exit_rescue' })) as unknown as ActionResult
}
// ---- 功能面板方块（上游模块结构化数据；字段名对齐上游表单，勿改） ----
export interface NatRule { id: number; name: string; external: string; internal: string; protocol: string }
export interface NatWebEntry { id: number; domain: string; external: string; internal: string }
export interface SecurityGroup { id: number; name: string; description: string }
export interface SecurityRule { id: number; description: string; action: string; direction: string; protocol: string; port_range: string; ip: string }
export interface PanelSelect { value: string; name: string; selected: boolean }
export interface SettingData { iso: PanelSelect[]; boot: PanelSelect[] }
export interface SnapshotItem { id: number; name: string; type: string; status: number; remarks: string; create_time: string }
export interface SnapshotInfo { snap_num: number; backup_num: number; list: SnapshotItem[]; disk: { id: number; name: string; type: string; size: number; dev: string }[] }
export interface ServiceBlocks {
  nat_acl?: NatRule[]
  nat_web?: NatWebEntry[]
  security_groups?: SecurityGroup[]
  setting?: SettingData
}

export async function fetchServiceBlocks(id: number | string): Promise<ServiceBlocks> {
  const res = await http.get<{ ok: number; blocks?: ServiceBlocks; msg?: string }>(`/services/${id}/blocks`)
  if (res.ok !== 1) throw new Error(res.msg || '面板数据拉取失败')
  return res.blocks || {}
}

// 方块/快照操作：后端 r.ParseForm 只解析表单体，须用 FormData（不能用 JSON）
export async function blockAction(id: number | string, fn: string, fields: Record<string, string | number>): Promise<ActionResult> {
  const fd = new FormData()
  Object.entries(fields).forEach(([k, v]) => fd.set(k, String(v)))
  return (await http.post<ActionResult>(`/services/${id}/block/${fn}`, fd)) as unknown as ActionResult
}

export async function fetchBlockRules(id: number | string, gid: number): Promise<SecurityRule[]> {
  const res = await http.get<{ ok: number; rules?: SecurityRule[]; msg?: string }>(`/services/${id}/block-rules`, { gid })
  if (res.ok !== 1) throw new Error(res.msg || '规则拉取失败')
  return res.rules || []
}

export async function fetchSnapshotInfo(id: number | string): Promise<SnapshotInfo> {
  const res = await http.get<{ ok: number; info?: SnapshotInfo; msg?: string }>(`/services/${id}/snapshot`)
  if (res.ok !== 1) throw new Error(res.msg || '快照信息拉取失败')
  return (res.info || { snap_num: 0, backup_num: 0, list: [], disk: [] }) as SnapshotInfo
}

export async function snapshotAction(id: number | string, fn: string, fields: Record<string, string | number>): Promise<ActionResult> {
  const fd = new FormData()
  Object.entries(fields).forEach(([k, v]) => fd.set(k, String(v)))
  return (await http.post<ActionResult>(`/services/${id}/snapshot/${fn}`, fd)) as unknown as ActionResult
}

// ---- 账户设置 / 实名 ----
export interface ProfileData {
  ok: number
  email: string
  email_verified: boolean
  name: string
  phone: string
  phone_masked: string
  phone_verified: boolean
  has_phone: boolean
}
export interface VerificationData {
  ok: number
  manual_enabled: boolean
  status: string
  status_text: string
  reason?: string
  masked_id: string
  can_submit: boolean
  plugin_provider?: string
  automatic_status?: string
  automatic_status_text?: string
  automatic_url?: string
  automatic_id?: number
}
export interface NotificationItem {
  id: number
  title: string
  body: string
  category: string
  read: boolean
  time: string
}

export interface NotificationListResult {
  list: NotificationItem[]
  total: number
  unread: number
  page: number
  limit: number
  categories: { key: string; label: string }[]
}

export interface PowerInfo {
  status: 'on' | 'off' | 'operating' | 'fault' | string
  desc?: string
}

/** 当前 VNC 会话密码（上游每次申请会话都会刷新，实时取用）。 */
export async function fetchVncPass(id: number | string): Promise<string> {
  const res = await http.get<{ ok: number; password?: string; msg?: string }>(`/services/${id}/vnc-pass`)
  if (res.ok !== 1) throw new Error(res.msg || '该实例暂不支持 VNC 控制台')
  return res.password || ''
}

export async function fetchPower(id: number): Promise<PowerInfo> {
  const res = await http.get<{ ok: number; power?: PowerInfo }>(`/services/${id}/power`)
  return (res.power || { status: 'fault' }) as PowerInfo
}

export interface ServiceInvoice {
  id: number
  no: string
  amount: string
  paid_amount: string
  fee_amount: string
  kind: string
  status: string
  due_at?: string
  created_at: string
}

export async function fetchProfile(): Promise<ProfileData> {
  const res = await http.get<ProfileData>('/user/profile')
  return res as unknown as ProfileData
}
export async function fetchVerification(): Promise<VerificationData> {
  const res = await http.get<VerificationData>('/user/verification')
  return res as unknown as VerificationData
}
/** 启动自动实名（插件）流程，返回认证页 URL。 */
export async function startVerificationPlugin(body: {
  provider: string
  legal_name: string
  identity_number: string
}): Promise<{ submission_id: number; url: string }> {
  const res = await http.post<{ ok: number; submission_id: number; url: string }>(
    '/user/verification/plugin/start',
    body,
  )
  return { submission_id: res.submission_id, url: res.url }
}
/** 查询自动实名（插件）结果。 */
export async function pollVerificationPlugin(
  submissionId: number,
): Promise<{ status: string; message?: string }> {
  const res = await http.post<{ ok: number; status: string; message?: string }>(
    '/user/verification/plugin/poll',
    { submission_id: String(submissionId) },
  )
  return { status: res.status, message: res.message }
}
export async function fetchNotifications(params: { category?: string; keyword?: string; page?: number; limit?: number } = {}): Promise<NotificationListResult> {
  const res = await http.get<NotificationListResult>('/notifications', params)
  return res as unknown as NotificationListResult
}
export async function updateProfile(body: { name: string; email: string; current_password?: string }): Promise<ProfileData> {
  const res = await http.post<ProfileData>('/user/profile', body)
  return res as unknown as ProfileData
}
export async function fetchUnreadNotificationCount(): Promise<number> {
  const res = await http.get<{ unread?: number }>('/notifications/unread-count')
  return Number((res as unknown as { unread?: number }).unread || 0)
}
export async function markNotificationRead(id: number): Promise<void> {
  await http.post(`/notifications/${id}/read`, {})
}
export async function markAllNotificationsRead(): Promise<void> {
  await http.post('/notifications/read-all', {})
}
export async function deleteNotification(id: number): Promise<void> {
  await http.post(`/notifications/${id}/delete`, {})
}
export async function deleteAllNotifications(): Promise<void> {
  await http.post('/notifications/delete-all', {})
}

export interface TicketItem {
  id: number
  subject: string
  body: string
  priority: string
  category: string
  status: string
  service_id: number
  service_name: string
  service_hostname: string
  created_at: string
  updated_at: string
}
export async function fetchTickets(params: { q?: string; status?: string; priority?: string; category?: string; page?: number; limit?: number } = {}): Promise<{ list: TicketItem[]; total: number; page: number; limit: number }> {
  const res = await http.get<{ list?: TicketItem[]; total?: number; page?: number; limit?: number }>('/tickets', params)
  return { list: res.list || [], total: res.total || 0, page: res.page || 1, limit: res.limit || 10 }
}
export async function createTicket(body: { subject: string; body: string; priority: string; category: string; service_id?: number }): Promise<void> {
  await http.post('/tickets', body)
}
export interface TicketMessage { id: number; admin_id: number; content: string; created_at: string }
export interface TicketAttachment { id: number; message_id: number; name: string; mime: string; size: number; url: string }
export async function fetchTicketDetail(id: number): Promise<{ ticket: TicketItem; messages: TicketMessage[]; attachments: TicketAttachment[] }> {
  return (await http.get(`/tickets/${id}`)) as unknown as { ticket: TicketItem; messages: TicketMessage[]; attachments: TicketAttachment[] }
}
export async function replyTicket(id: number, content: string, file?: File): Promise<void> { if (!file) { await http.post(`/tickets/${id}/reply`, { content }); return }; const form = new FormData(); form.append('content', content); form.append('file', file); await http.post(`/tickets/${id}/reply`, form) }
export async function uploadTicketAttachment(id: number, file: File): Promise<void> { const form = new FormData(); form.append('file', file); await http.post(`/tickets/${id}/attachments`, form) }
export async function closeTicket(id: number, reason: string): Promise<void> { await http.post(`/tickets/${id}/close`, { reason }) }
export async function reopenTicket(id: number): Promise<void> { await http.post(`/tickets/${id}/reopen`, {}) }
export async function fetchServiceInvoices(id: number | string): Promise<ServiceInvoice[]> {
  const res = await http.get<{ ok: number; list: ServiceInvoice[] }>(`/services/${id}/invoices`)
  return (res.list || []) as unknown as ServiceInvoice[]
}
export async function changePassword(oldP: string, newP: string): Promise<ActionResult> {
  return (await http.post<ActionResult>('/user/password', { old: oldP, new: newP })) as unknown as ActionResult
}
