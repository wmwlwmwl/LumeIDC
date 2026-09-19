import { http, type ApiResult } from '../http/index'

// 管理后台 JSON 接口（同 URL，SSR 共存）。

export interface AdminCounts {
  users: number
  orders: number
  services: number
}

// 仪表盘趋势点（近 7 天）。
export interface AdminTrendPoint {
  date: string
  users: number
  orders: number
  revenue: number
}

export interface AdminDashboard {
  counts: AdminCounts
  trends: AdminTrendPoint[]
}

export async function fetchDashboard(): Promise<AdminDashboard> {
  const res = await http.get<{ ok: number; counts?: AdminCounts; trends?: AdminTrendPoint[] }>('/')
  return {
    counts: (res.counts || { users: 0, orders: 0, services: 0 }) as unknown as AdminCounts,
    trends: res.trends || [],
  }
}

export interface AdminTicket { id: number; user_id: number; email: string; subject: string; category: string; priority: string; status: string; service_name: string; service_hostname: string; assignee_id?: number; assignee_name?: string; created_at: string; updated_at: string }
export interface AdminTicketMessage { id: number; user_id: number; admin_id: number; content: string; internal?: boolean; created_at: string }
export async function fetchAdminTickets(params: { q?: string; status?: string; priority?: string; category?: string; assignee_id?: number; service_id?: number; queue?: string; page?: number; limit?: number; sort?: string; order?: string } = {}): Promise<{ list: AdminTicket[]; total: number; page: number; limit: number }> { const res = await http.get<{ list?: AdminTicket[]; total?: number; page?: number; limit?: number }>('/tickets', params); return { list: res.list || [], total: res.total || 0, page: res.page || 1, limit: res.limit || 25 } }
export async function fetchAdminTicketStats(): Promise<Record<string, number>> { const res = await http.get<{ stats?: Record<string, number> }>('/tickets/stats'); return res.stats || {} }
export interface AdminTicketAttachment { id: number; message_id: number; name: string; mime: string; size: number; url: string }
export async function fetchAdminTicket(id: number): Promise<{ ticket: AdminTicket & { body: string }; messages: AdminTicketMessage[]; attachments: AdminTicketAttachment[] }> { return (await http.get(`/tickets/${id}`)) as unknown as { ticket: AdminTicket & { body: string }; messages: AdminTicketMessage[]; attachments: AdminTicketAttachment[] } }
export async function replyAdminTicket(id: number, content: string, file?: File): Promise<void> { if (!file) { await http.post(`/tickets/${id}/reply`, { content }); return }; const form = new FormData(); form.append('content', content); form.append('file', file); await http.post(`/tickets/${id}/reply`, form) }
export async function updateAdminTicketStatus(id: number, status: string): Promise<void> { await http.post(`/tickets/${id}/status`, { status }) }
export async function assignAdminTicket(id: number, admin_id: number): Promise<void> { await http.post(`/tickets/${id}/assign`, { admin_id }) }
export async function addAdminTicketNote(id: number, content: string): Promise<void> { await http.post(`/tickets/${id}/internal-note`, { content }) }
export async function uploadAdminTicketAttachment(id: number, file: File): Promise<void> { const form = new FormData(); form.append('file', file); await http.post(`/tickets/${id}/attachments`, form) }
export async function fetchTicketAssignees(): Promise<{ id: number; name: string }[]> { const res = await http.get<{ list?: { id: number; name: string }[] }>('/ticket-assignees'); return res.list || [] }

export async function adminLogin(body: Record<string, string>): Promise<void> {
  await http.post<{ ok: number }>('/login', body, { silent401: true })
}

// 后台登录图形验证码（公共路由 /captcha，scene=admin_login；需跳过 admin 基址前缀）。
export interface AdminCaptcha {
  enabled: boolean
  id?: string
  image?: string
}
export async function fetchAdminCaptcha(): Promise<AdminCaptcha> {
  const res = await http.get<AdminCaptcha>('/captcha', { scene: 'admin_login' }, { skipBase: true })
  return res as unknown as AdminCaptcha
}
export async function adminLogout(): Promise<void> {
  // 后台登出：经 apiBase 请求后台路径下的 /logout（AdminPath 改写为 /admin/logout），
  // 只清管理员通道会话，不影响同一浏览器上的用户登录。
  await http.post('/logout')
}

// ---- 列表 ----
export interface AdminProduct {
  id: number
  name: string
  type: string
  monthly: string
  /** 首期收款额：月价 + 最低配置档的一次性初装费（无初装费时与月价相同） */
  first: string
  visible: boolean
  hidden: boolean
  server: string
  upstream_pid: number
  requires_identity: boolean
  /** 上游停售原因：'' 正常在售；unshelved 上游已下架 */
  upstream_offline_reason: string
}
export async function fetchAdminProducts(): Promise<AdminProduct[]> {
  const res = await http.get<{ ok: number; list?: AdminProduct[] }>('/products')
  return (res.list || []) as unknown as AdminProduct[]
}

export async function deleteAdminProduct(id: number): Promise<void> {
  await http.post(`/products/${id}/delete`, {})
}

// ---- 产品新增/编辑 ----
export interface AdminProductTypeOption {
  id: number
  name: string
  parent_id: number
  hidden: boolean
}
export interface AdminProductServerOption {
  id: number
  name: string
  provider: string
}
export interface ProductFormOption {
  value: string
  label: string
  group?: string
}
export interface ProductFormConfigSub {
  name: string
  value?: string
}
export interface ProductFormConfigOption {
  field: string
  name: string
  option_mode: string
  hidden: boolean
  sub?: ProductFormConfigSub[]
}
export interface ProductFormField {
  key: string
  label: string
  type: string
  hint?: string
  options?: ProductFormOption[]
  options_url?: string
  sync_name?: boolean
  pull_config?: boolean
  transient?: boolean
  config_by_value?: Record<string, ProductFormConfigOption>
}
export interface ProductFormHints {
  markupFree?: boolean
  pidHint?: string
  fieldSuggestions?: { field: string; label: string }[]
}
export interface AdminProductDetail {
  id: number
  name: string
  description: string
  type_id: number
  server_id: number
  upstream_pid: number
  stock: number
  hidden: boolean
  requires_identity: boolean
  profit_type: number
  profit_value: number
  upgrade_whitelist_enabled: boolean
  upgrade_targets: number[]
}
export interface AdminProductFormData {
  ok: number
  types: AdminProductTypeOption[]
  servers: AdminProductServerOption[]
  form_spec: Record<string, ProductFormField[]>
  prices: { monthly: string; quarterly: string; yearly: string }
  config_json: string
  hints: Record<string, ProductFormHints>
  upstream_bound: boolean
  product?: AdminProductDetail
  upgrade_candidates?: { id: number; name: string }[]
}
export async function fetchAdminProductForm(id?: number): Promise<AdminProductFormData> {
  const res = await http.get<AdminProductFormData>(id ? `/products/${id}/edit` : '/products/new')
  return res as unknown as AdminProductFormData
}
export async function saveAdminProduct(id: number | undefined, body: Record<string, unknown>): Promise<void> {
  const res = (await http.post(id ? `/products/${id}/save` : '/products/save', body)) as {
    ok: number
    msg?: string
  }
  if (!res.ok) throw new Error(res.msg || '保存失败')
}

export interface UpstreamOptionGroup {
  name: string
  items: { pid: number; name: string }[]
}
export async function fetchUpstreamOptions(serverId: number): Promise<UpstreamOptionGroup[]> {
  const res = (await http.get('/products/upstream-options', { server_id: serverId })) as {
    ok: number
    groups?: UpstreamOptionGroup[]
    msg?: string
  }
  if (!res.ok) throw new Error(res.msg || '拉取上游商品失败')
  return res.groups || []
}

export interface UpstreamConfigResult {
  json: string
  count: number
  price: { monthly: number; quarterly: number; yearly: number }
  description: string
  stock: number
}
export async function fetchUpstreamConfig(serverId: number, pid: number): Promise<UpstreamConfigResult> {
  const res = (await http.get('/products/upstream-config', { server_id: serverId, pid })) as {
    ok: number
    json?: string
    count?: number
    price?: { monthly: number; quarterly: number; yearly: number }
    description?: string
    stock?: number
    msg?: string
  }
  if (!res.ok) throw new Error(res.msg || '拉取上游配置失败')
  return {
    json: res.json || '[]',
    count: Number(res.count || 0),
    price: res.price || { monthly: 0, quarterly: 0, yearly: 0 },
    description: res.description || '',
    stock: typeof res.stock === 'number' ? res.stock : -1,
  }
}

export interface OrderItem {
  id: number
  email: string
  amount: string
  cycle: string
  status: string
  profit: string
  paid: string
  /** 关联服务（未开通的订单为空串） */
  service_name: string
  service_host: string
  service_status: string
}
export async function fetchOrders(q: string, page: number, sort?: string, order?: string): Promise<{ list: OrderItem[]; total: number; profit: string }> {
  const res = await http.get<{ ok: number; list?: OrderItem[]; total?: number; profit?: string }>('/orders', {
    q,
    page,
    sort,
    order,
  })
  return {
    list: (res.list || []) as unknown as OrderItem[],
    total: Number(res.total || 0),
    profit: String(res.profit || '0.00'),
  }
}

/** 订单退款（method: balance=退余额 / gateway=线下手动）。 */
export async function refundOrder(id: number, amount: string, reason: string, method: string): Promise<void> {
  await http.post(`/orders/${id}/refund`, { amount, reason, method })
}

export interface AdminService {
  id: number
  user_id: number
  user: string
  product: string
  status_text: string
  expires: string
  upstream: string
  prov_err: string
  /** 过渡状态：upgrading=升级处理中（此时 prov_err 有值表示升级待人工处置） */
  transition: string
  /** 上游改价导致的待处理：点「重试」前需二次确认（按新价继续会少赚） */
  price_changed: boolean
  /** 存在"上游结果未知"的隔离履约任务：需管理员对账后恢复（不调上游、不动资金） */
  recovery?: boolean
  /** 隔离任务类型：provision/renew/upgrade（executed 恢复时 provision 需补填上游主机 ID） */
  recovery_kind?: string
  /** 隔离任务的领取版本，提交恢复时回传防并发 */
  recovery_version?: number
  profit: string
  hostname: string
  config_desc: string
  monthly: string
  days_left: number
  renew_monthly: string
  renew_quarterly: string
  renew_yearly: string
}
export interface AdminProductOption {
  id: number
  name: string
}
export async function fetchAdminServices(f: { q?: string; status?: string; product_id?: number }) {
  const res = await http.get<{
    ok: number
    list?: AdminService[]
    products?: AdminProductOption[]
    filter?: { q: string; status: string; product_id: number }
  }>('/services', { q: f.q ?? '', status: f.status ?? '', product_id: f.product_id ?? '' })
  return res as unknown as { list: AdminService[]; products: AdminProductOption[]; filter: { q: string; status: string; product_id: number } }
}

// 编辑服务：换归属用户 / 到期时间 / 固定续费价（空串=清除覆盖恢复跟随产品价；未传字段=不修改）
export async function updateAdminService(id: number, data: Record<string, string>) {
  await http.post(`/services/${id}/edit`, data)
}

// ---- 服务对账恢复（"上游结果未知"隔离任务的人工核对恢复；不调上游、不动资金） ----
/** 对账恢复摘要：后端白名单证据（密码、密钥和完整检查点不返回） */
export interface RecoverySummary {
  service_id: number
  /** 隔离任务 ID 与领取版本（提交时回传防并发） */
  job_id: number
  version: number
  /** 履约类型：provision / renew / upgrade */
  kind: string
  order_id: number
  cycle: string
  provider: string
  server_id: number
  /** 上游账户名（服务器名 / 用户名） */
  account: string
  /** 服务当前绑定的上游主机 ID（0=未绑定） */
  host_id: number
  /** 本地检查点里记录的主机 ID（空=未记录） */
  checkpoint_host: string
  /** 本地检查点里记录的上游账单号（空=未记录） */
  upstream_invoice: string
  invoice_id: number
  invoice_status: number
  order_status: number
  amount: string
  paid_amount: string
  refunded: string
  /** 周期授权条数 */
  grants: number
  /** 未决任务数（含隔离任务自身） */
  pending_jobs: number
  target_product_id: number
  target_pid: number
  /** 是否允许"上游未执行"续跑（false 时该决策禁用） */
  can_resume: boolean
  resume_reason: string
  /** 非空=存在证据冲突或不一致，禁止解除隔离 */
  block_reason: string
}

/** 对账恢复提交体：字段名必须与后端 RecoveryConfirmation 的 JSON tag 精确一致（DisallowUnknownFields） */
export interface RecoveryConfirmation {
  job_id: number
  expected_version: number
  decision: 'confirmed_completed' | 'confirmed_not_executed'
  evidence: string
  verified_host_id: number
  remote_stable: boolean
  billing_verified: boolean
  delivery_verified: boolean
}

/** 读取服务的对账恢复摘要；服务无可核对的隔离任务时后端返回 409 */
export async function fetchServiceRecovery(id: number): Promise<RecoverySummary> {
  const res = await http.get<{ ok: number; recovery?: RecoverySummary }>(`/services/${id}/recovery`)
  return res.recovery as unknown as RecoverySummary
}

/** 提交对账恢复确认；核对未通过或有并发操作时后端返回 409 中文消息 */
export async function confirmServiceRecovery(id: number, body: RecoveryConfirmation): Promise<void> {
  await http.post(`/services/${id}/recovery`, body as unknown as Record<string, unknown>)
}

// ---- 停用申请 ----
export interface AdminCancelRequest {
  id: number
  service_id: number
  user_id: number
  username: string
  email: string
  service_name: string
  product_name: string
  hostname: string
  service_status: string
  type: string
  reason: string
  reason_detail: string
  status: string
  handle_mode: string
  handle_note: string
  created_at: string
  handled_at: string
  expires_at?: string
}

export async function fetchAdminCancelRequests(status = '') {
  return (await http.get<{ ok: number; list: AdminCancelRequest[]; pending: number }>(
    '/service-cancel-requests',
    { status },
  )) as unknown as { ok: number; list: AdminCancelRequest[]; pending: number }
}

export async function handleAdminCancelRequest(
  id: number,
  action: 'local' | 'upstream' | 'reject',
  note: string,
) {
  return (await http.post<{ ok: number; msg?: string }>(
    `/service-cancel-requests/${id}/handle`,
    { action, note },
  )) as unknown as { ok: number; msg?: string }
}

// ---- 后台通知中心（Art 顶栏消息面板） ----
export interface AdminNoticeItem {
  type: string
  title: string
  time: string
  link: string
}
export interface AdminNotifications {
  ok: number
  pending: number
  todos: AdminNoticeItem[]
  messages: AdminNoticeItem[]
  notices: AdminNoticeItem[]
}
export async function fetchAdminNotifications(): Promise<AdminNotifications> {
  const res = await http.get<Partial<AdminNotifications>>('/notifications')
  return {
    ok: res.ok ?? 0,
    pending: res.pending ?? 0,
    todos: res.todos ?? [],
    messages: res.messages ?? [],
    notices: res.notices ?? [],
  }
}

export interface AdminUser {
  id: number
  email: string
  name: string
  phone: string
  status: string
  disabled: boolean
  balance: string
  /** 实名状态：approved / pending / none */
  realname: string
  created_at: string
}
export async function fetchUsers(q: string, page: number, sort?: string, order?: string): Promise<{ list: AdminUser[]; total: number }> {
  const res = await http.get<{ ok: number; list?: AdminUser[]; total?: number }>('/users', { q, page, sort, order })
  return { list: (res.list || []) as unknown as AdminUser[], total: Number(res.total || 0) }
}

/** 管理员启用/禁用用户。disabled=true 为禁用。 */
export async function setUserStatus(id: number, disabled: boolean): Promise<{ msg: string }> {
  const res = await http.post<{ ok: number; msg?: string }>(`/users/${id}/status`, {
    disabled: disabled ? '1' : '0',
  })
  return { msg: res.msg || '操作成功' }
}

/** 管理员给用户充值余额。 */
export async function rechargeUser(id: number, amount: string, note: string): Promise<{ msg: string }> {
  const res = await http.post<{ ok: number; msg?: string }>(`/users/${id}/recharge`, { amount, note })
  return { msg: res.msg || '操作成功' }
}

/** 管理员从用户余额退款（扣减）。 */
export async function refundUser(id: number, amount: string, note: string): Promise<{ msg: string }> {
  const res = await http.post<{ ok: number; msg?: string }>(`/users/${id}/refund`, { amount, note })
  return { msg: res.msg || '操作成功' }
}

export interface AdminType {
  id: number
  name: string
  description: string
  sort: number
  hidden: boolean
  product_count: number
  parent_id?: number
  children?: AdminType[]
}
export async function fetchAdminTypes(): Promise<AdminType[]> {
  const res = await http.get<{ ok: number; list?: AdminType[] }>('/types')
  return (res.list || []) as unknown as AdminType[]
}

export async function saveAdminType(body: {
  id?: number
  name: string
  description: string
  sort: number
  parent_id: number
  hidden: boolean
}): Promise<void> {
  await http.post('/types/save', {
    id: body.id ?? '',
    name: body.name,
    description: body.description,
    sort: String(body.sort),
    parent_id: String(body.parent_id),
    hidden: body.hidden ? '1' : '0',
  })
}
export async function deleteAdminType(id: number): Promise<void> {
  await http.post(`/types/${id}/delete`, {})
}

/** 把该分类下的产品整体移动到另一个二级分类（删除分类前需先清空）。 */
export async function moveTypeProducts(id: number, targetId: number): Promise<{ moved: number; msg?: string }> {
  return (await http.post(`/types/${id}/moveproducts`, { target_id: targetId })) as unknown as {
    moved: number
    msg?: string
  }
}

export interface AdminAnnouncement {
  id: number
  title: string
  category?: string
  summary?: string
  content: string
  cover?: string
  reads?: number
  hidden: boolean
  pinned: boolean
  created_at: string
}
export async function fetchAdminAnnouncements(): Promise<AdminAnnouncement[]> {
  const res = await http.get<{ ok: number; list?: AdminAnnouncement[] }>('/announcements')
  return (res.list || []) as unknown as AdminAnnouncement[]
}

export async function saveAnnouncement(body: {
  id?: number
  title: string
  category?: string
  summary?: string
  content: string
  cover?: string
  hidden: boolean
  pinned: boolean
}): Promise<void> {
  await http.post('/announcements/save', {
    id: body.id ?? '',
    title: body.title,
    category: body.category ?? '',
    summary: body.summary ?? '',
    content: body.content,
    cover: body.cover ?? '',
    hidden: body.hidden ? '1' : '0',
    pinned: body.pinned ? '1' : '0',
  })
}
export async function deleteAnnouncement(id: number): Promise<void> {
  await http.post(`/announcements/${id}/delete`, {})
}

export interface AdminCoupon {
  id: number
  code: string
  type: string
  value: number
  min_amount: number
  expires_at: string
  usage_limit: number
  used: number
  active: boolean
}
export async function fetchAdminCoupons(): Promise<AdminCoupon[]> {
  const res = await http.get<{ ok: number; list?: AdminCoupon[] }>('/coupons')
  return (res.list || []) as unknown as AdminCoupon[]
}

export async function createAdminCoupon(body: {
  code: string
  type: string
  value: number
  min_amount: number
  usage_limit: number
  expires_at: string
}): Promise<void> {
  await http.post('/coupons/save', {
    code: body.code,
    type: body.type,
    value: String(body.value),
    min_amount: String(body.min_amount),
    usage_limit: String(body.usage_limit),
    expires_at: body.expires_at,
  })
}

export interface AdminLogRow {
  id: number
  admin_id: number
  admin_name: string
  action: string
  target_type: string
  target_id: number
  target_email: string
  detail: string
  ip: string
  created_at: string
}
export async function fetchAdminLogs(): Promise<AdminLogRow[]> {
  const res = await http.get<{ ok: number; list?: AdminLogRow[] }>('/logs')
  return (res.list || []) as unknown as AdminLogRow[]
}

export interface AdminRefund {
  id: number
  user_id: number
  order_id: number
  amount: string
  method: string
  reason: string
  admin_id: number
  status: string
  created_at: string
}
export async function fetchAdminRefunds(): Promise<AdminRefund[]> {
  const res = await http.get<{ ok: number; list?: AdminRefund[] }>('/refunds')
  return (res.list || []) as unknown as AdminRefund[]
}

export interface AdminServer {
  id: number
  name: string
  provider: string
  api_url: string
  status: string
  disabled: boolean
}
export async function fetchAdminServers(): Promise<AdminServer[]> {
  const res = await http.get<{ ok: number; list?: AdminServer[] }>('/servers')
  return (res.list || []) as unknown as AdminServer[]
}

export async function deleteAdminServer(id: number): Promise<void> {
  await http.post(`/servers/${id}/delete`, {})
}

/** 供应商凭据字段（由 provider 声明，表单动态渲染 —— 已是结构化数据，无需改契约）。 */
export interface CredentialField {
  name: string
  label: string
  placeholder?: string
  required?: boolean
  secret?: boolean
}
export interface AdminServerFormData {
  ok: number
  providers: { code: string; name: string }[]
  credential_fields: Record<string, CredentialField[]>
  values: Record<string, string>
  server?: {
    id: number
    name: string
    provider: string
    api_url: string
    api_username: string
    disabled: boolean
    profit_type: number
    profit_value: number
    /** "等外部条件"类失败（如余额不足）是否保持自动重试；关闭则转人工复核 */
    retry_later_enabled: boolean
    /** 自动重试间隔（分钟） */
    retry_later_minutes: number
  }
}
export async function fetchAdminServerForm(id?: number): Promise<AdminServerFormData> {
  const res = await http.get<AdminServerFormData>(id ? `/servers/${id}/edit` : '/servers/new')
  return res as unknown as AdminServerFormData
}
export async function saveAdminServer(id: number | undefined, body: Record<string, string>): Promise<void> {
  await http.post(id ? `/servers/${id}/save` : '/servers/save', body)
}

// ---- 上游商品目录 / 导入 ----
export interface CatalogRow {
  pid: number
  name: string
  group: string
  monthly: string
  stock: string
  linked: boolean
}
export async function fetchServerCatalog(
  serverId: number,
  fresh = false,
 ): Promise<{ server: { id: number; name: string; provider?: string }; rows: CatalogRow[]; types: { id: number; name: string; parent_id?: number }[]; error?: string }> {
  const res = await http.get<{
    ok: number
    server?: { id: number; name: string; provider?: string }
    rows?: CatalogRow[]
    types?: { id: number; name: string }[]
    error?: string
  }>(`/servers/${serverId}/catalog`, fresh ? { fresh: '1' } : undefined)
  return {
    server: (res.server || { id: serverId, name: '' }) as { id: number; name: string; provider?: string },
    rows: (res.rows || []) as CatalogRow[],
    types: (res.types || []) as { id: number; name: string }[],
    error: res.error,
  }
}
export async function importCatalogProducts(
  serverId: number,
  body: {
    parent_id: string
    profit_type: string
    profit_value: string
    requires_identity: boolean
    /** 选填：分类描述（写入上游分组对应的本地分类，前台分类页展示；留空则不动） */
    desc?: string
    pids: number[]
  },
): Promise<{ imported: number; msg?: string }> {
  // 后端字段名为 import（多值），JSON 下传数组
  const res = (await http.post(`/servers/${serverId}/import`, {
    parent_id: body.parent_id,
    profit_type: body.profit_type,
    profit_value: body.profit_value,
    requires_identity: body.requires_identity ? '1' : '0',
    desc: body.desc || '',
    import: body.pids,
  })) as { ok: number; imported?: number; msg?: string }
  if (!res.ok) throw new Error(res.msg || '导入失败')
  return { imported: Number(res.imported || 0), msg: res.msg }
}

/** 测试上游连通性（成功时 msg 可能附带账户余额）。 */
export async function testAdminServer(id: number): Promise<{ ok: number; msg?: string }> {
  return (await http.get(`/servers/${id}/test`)) as unknown as { ok: number; msg?: string }
}

export interface AdminGateway {
  id: number
  code: string
  driver: string
  name: string
  api_url: string
  pid?: string
  channel?: string
  payment_mode?: string
  mobile_qrcode?: string
  app_id?: string
  seller_id?: string
  mch_id?: string
  api_v3_key?: string
  cert_serial?: string
  public_key_id?: string
  h5_app_name?: string
  h5_app_url?: string
  fee_percent: string
  enabled: boolean
  sort: number
  /** 密钥类字段不回传，仅告知是否已配置；编辑时留空即沿用旧值 */
  has_key?: boolean
  has_private_key?: boolean
  has_public_key?: boolean
  has_api_v3_key?: boolean
}
export async function fetchAdminGateways(): Promise<AdminGateway[]> {
  const res = await http.get<{ ok: number; list?: AdminGateway[] }>('/gateway')
  return (res.list || []) as unknown as AdminGateway[]
}

/** 新增/更新支付网关（编码为唯一键）。 */
export async function saveAdminGateway(body: Record<string, string>): Promise<void> {
  await http.post('/gateway/save', body)
}
export async function deleteAdminGateway(code: string): Promise<void> {
  await http.post(`/gateway/${encodeURIComponent(code)}/delete`, {})
}

// ---- 通知设置 ----
export async function fetchAdminSettings(): Promise<Record<string, string>> {
  const res = await http.get<{ ok: number; cfg?: Record<string, string> }>('/settings')
  return (res.cfg || {}) as Record<string, string>
}

/** 按分区保存（section 为空表示邮件设置这一默认分区，与后端一致）。 */
export async function saveAdminSettings(section: string, body: Record<string, string>): Promise<ApiResult> {
  // 返回完整结果：HTTP 200 下仍可能业务失败（ok=0），由调用方判断
  return http.post('/settings', { settings_section: section, ...body })
}
export async function testAdminEmail(
  email: string,
  accountIndex?: number,
): Promise<{ ok: boolean; msg?: string }> {
  const body: Record<string, unknown> = { email }
  if (typeof accountIndex === 'number' && accountIndex >= 0) body.account_index = accountIndex
  return (await http.post('/settings/test-email', body)) as unknown as { ok: boolean; msg?: string }
}

// ---- 用户详情 / 编辑 ----
export interface AdminUserDetail {
  user: {
    id: number
    email: string
    name: string
    balance: string
    status: number
    phone: string
    phone_masked: string
    phone_status: string
    email_status: string
    email_verified: boolean
    phone_verified: boolean
    registered_at: string
    last_login_at: string
  }
  realname: {
    status: string
    submitted_at: string
    reviewed_at: string
    submission_id: number
    has_submission: boolean
  }
  stats: { service_count: number; active_count: number; unpaid_count: number; paid_total: string }
  balance_logs: { time?: string; type?: string; amount?: string; note?: string; after?: string }[] | null
  admin_logs: { action: string; detail: string; admin_name?: string; created_at: string }[]
}

export async function fetchAdminUser(id: number): Promise<AdminUserDetail> {
  return (await http.get<AdminUserDetail>(`/users/${id}/edit`)) as unknown as AdminUserDetail
}

// 查看用户实名资料（解密姓名/证件号 + 证件照地址）。调用会被记录为 real_name_viewed。
export interface AdminUserRealname {
  status: string
  name: string
  number: string
  submitted_at: string
  reviewed_at: string
  submission_id: number
  front_url?: string
  back_url?: string
}
export async function fetchUserRealname(id: number): Promise<AdminUserRealname | null> {
  const res = await http.get<{ ok: number; realname?: AdminUserRealname | null }>(
    `/users/${id}/realname`,
  )
  return res.realname || null
}
export async function saveAdminUser(id: number, body: Record<string, string>): Promise<void> {
  await http.post(`/users/${id}/save`, body)
}

// ---- 管理员本人账户（登录名 / 密码 / 两步验证）----
export async function fetchAdminUsername(): Promise<string> {
  const res = (await http.get('/password')) as { ok: number; username?: string }
  return String(res.username || '')
}
/** 修改登录名：需当前密码确认。 */
export async function changeAdminUsername(username: string, password: string): Promise<void> {
  await http.post('/username', { username, password })
}
/** 修改密码（成功后服务端吊销会话，需重新登录）。 */
export async function changeAdminPassword(oldPassword: string, newPassword: string): Promise<void> {
  await http.post('/password', { old_password: oldPassword, new_password: newPassword })
}

// ---- 系统更新（仅 Linux 支持）----
export interface UpdateInfo {
  current_version: string
  version?: string
  published_at?: string
  notes?: string
  available?: boolean
  hint?: string
}
export async function fetchUpdateState(): Promise<{ current_version: string; pending_restart: boolean; disabled: boolean }> {
  return (await http.get('/update')) as unknown as { current_version: string; pending_restart: boolean; disabled: boolean }
}
export async function checkUpdate(): Promise<UpdateInfo> {
  const res = (await http.post('/update/check', {})) as { ok: number; info?: UpdateInfo; msg?: string }
  if (!res.ok) throw new Error(res.msg || '检查更新失败')
  return (res.info || { current_version: '' }) as UpdateInfo
}
export async function applyUpdate(): Promise<string> {
  const res = (await http.post('/update/apply', {})) as { ok: number; msg?: string }
  if (!res.ok) throw new Error(res.msg || '更新失败')
  return res.msg || '更新成功'
}
export async function restartUpdate(): Promise<string> {
  const res = (await http.post('/update/restart', {})) as { ok: number; msg?: string }
  if (!res.ok) throw new Error(res.msg || '重启失败')
  return res.msg || '正在重启'
}

export interface AdminVerificationItem {
  id: number
  user_id: number
  email: string
  phone: string
  status: string
  submitted_at: string
}
export async function fetchAdminVerifications(): Promise<AdminVerificationItem[]> {
  const res = await http.get<{ ok: number; list?: AdminVerificationItem[] }>('/verifications')
  return (res.list || []) as unknown as AdminVerificationItem[]
}

export interface AdminVerificationDetail {
  ok: number
  id: number
  user_id: number
  email: string
  phone: string
  status: string
  raw_status: string
  name: string
  identity_number: string
  submitted_at: string
  reason: string
  front_url: string
  back_url: string
}
export async function fetchAdminVerification(id: number): Promise<AdminVerificationDetail> {
  const res = await http.get<AdminVerificationDetail>(`/verifications/${id}`)
  return res as unknown as AdminVerificationDetail
}

export async function reviewVerification(id: number, approve: boolean, reason: string): Promise<void> {
  await http.post<{ ok: number }>(`/verifications/${id}/${approve ? 'approve' : 'reject'}`, { reason })
}
