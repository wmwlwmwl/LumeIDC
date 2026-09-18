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
}

export const GATEWAY_DRIVERS: Record<string, GatewayDriverMeta> = {
  epay: {
    label: '易支付',
    fields: ['api_url', 'pid', 'key', 'channel'],
    style: 'color:var(--el-color-success);background:var(--el-color-success-light-9)',
  },
  alipay_f2f: {
    label: '支付宝当面付',
    fields: ['api_url', 'app_id', 'private_key', 'public_key'],
    style: 'color:var(--el-color-primary);background:var(--el-color-primary-light-9)',
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
