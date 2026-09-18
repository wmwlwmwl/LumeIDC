/**
 * 支付网关驱动注册表。
 * 新增网关驱动：新增 Record 条目 + AdminGateways.vue 模板里补一个下拉选项。
 */

export interface GatewayDriverMeta {
  label: string
  /** 驱动实际需要的配置字段（与 SSR 表单 fields 映射一致） */
  fields: string[]
  /** 列表标签样式 */
  style: string
  /** 「支付模式」选择框下方的提示（可选）。用于说明各模式的前置条件 */
  paymentModeHint?: string
  /** 「支付模式」的默认取值（可选）。新建网关或切换驱动时使用，须与后端该驱动的默认行为一致 */
  paymentModeDefault?: string
}

export const GATEWAY_DRIVERS: Record<string, GatewayDriverMeta> = {
  epay: {
    label: '易支付',
    fields: ['api_url', 'pid', 'key', 'channel', 'payment_mode'],
    style: 'color:var(--el-color-success);background:var(--el-color-success-light-9)',
    // 与后端一致：空值为跳转模式（submit.php 是所有易支付分支的通用基线）
    paymentModeDefault: 'redirect',
  },
  alipay: {
    label: '支付宝',
    fields: ['api_url', 'app_id', 'private_key', 'public_key', 'payment_mode', 'mobile_qrcode'],
    style: 'color:var(--el-color-primary);background:var(--el-color-primary-light-9)',
    // 与后端一致：空值为扫码模式（仅签约当面付也能用，风险最低）
    paymentModeDefault: 'qrcode',
    paymentModeHint:
      '扫码模式：电脑出二维码，手机自动跳转「手机网站支付」；跳转模式：电脑、手机都跳转支付宝收银台。跳转模式与手机端跳转都需签约对应产品，未签约时收银台会报 ISV 权限不足——只签约了「当面付」的话，请用扫码模式并开启下方「手机端也扫码」。',
  },
  mock: {
    label: '模拟支付（测试）',
    fields: [],
    style: 'color:var(--art-gray-600);background:var(--art-gray-100)',
  },
}

export function gatewayDriverList() {
  return Object.entries(GATEWAY_DRIVERS).map(([value, meta]) => ({ value, label: meta.label }))
}

export function gatewayDriverMeta(driver: string): GatewayDriverMeta | undefined {
  return GATEWAY_DRIVERS[driver]
}
