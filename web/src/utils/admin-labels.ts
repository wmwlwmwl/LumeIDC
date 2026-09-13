/**
 * 后台枚举值中文标签。
 *
 * 后端余额流水 type 与管理员审计 action 都是英文常量，这里集中映射为中文，
 * 未收录的值原样展示（便于发现新增但未翻译的枚举）。
 */

/** 余额流水类型。 */
export const BALANCE_TYPE_LABELS: Record<string, string> = {
  recharge: '充值',
  consume: '消费',
  admin: '管理员调整',
  refund: '退款',
}

/** 管理员审计操作。 */
export const ADMIN_ACTION_LABELS: Record<string, string> = {
  // 余额 / 用户
  balance_recharge: '余额充值',
  balance_refund: '余额退款',
  user_update: '更新用户',
  user_enable: '启用用户',
  user_disable: '禁用用户',
  admin_username_changed: '修改管理员登录名',
  profile_updated: '更新资料',
  email_changed: '更换邮箱',
  phone_changed: '更换手机号',
  phone_otp_requested: '请求手机验证码',
  phone_verified: '手机号验证',
  // 实名
  real_name_viewed: '查看实名资料',
  real_name_photo_viewed: '查看实名证件照',
  real_name_submitted: '提交实名资料',
  real_name_approved: '实名审核通过',
  real_name_rejected: '实名审核驳回',
  real_name_rejected_user: '实名申请被驳回',
  real_name_submission: '实名申请',
  // 产品 / 分类
  product_create: '新建产品',
  product_update: '更新产品',
  product_delete: '删除产品',
  type_create: '新建分类',
  type_update: '更新分类',
  type_delete: '删除分类',
  type_move_products: '移动分类产品',
  // 服务
  service_suspend: '停机服务',
  service_unsuspend: '解除停机',
  service_terminate: '删除服务',
  service_retry: '重试开通',
  service_refund_pending: '开通前退款',
  service_retry_upgrade: '重试升级',
  service_refund_upgrade: '升级退款',
  service_retry_renew: '重试续费',
  service_refund_renew: '续费退款',
  // 订单
  refund: '订单退款',
  // 系统
  system_update: '系统更新',
}

/** 审计日志的操作对象类型。 */
export const TARGET_TYPE_LABELS: Record<string, string> = {
  user: '用户',
  product: '产品',
  type: '分类',
  service: '服务',
  order: '订单',
  real_name_submission: '实名申请',
  admin: '管理员',
  system: '系统',
}

export function balanceTypeLabel(type: string): string {
  return BALANCE_TYPE_LABELS[type] || type || '-'
}

export function targetTypeLabel(type: string): string {
  return TARGET_TYPE_LABELS[type] || type || '-'
}

export function adminActionLabel(action: string): string {
  return ADMIN_ACTION_LABELS[action] || action || '-'
}
