import { http } from '../http/index'

// 收银台接口
export interface PayGateway {
  code: string
  name: string
  fee_percent: string
  fee_amount: string
  amount: string
}
export interface PayData {
  ok: number
  invoice: {
    id: string
    no: string
    amount: string
    /** 已用余额抵扣的部分 */
    credit: string
    /** 在线支付剩余（= amount - credit） */
    remaining: string
    status: string
    recharge: boolean
  }
  paid: boolean
  expired: boolean
  balance_pay: boolean
  balance: string
  gateways: PayGateway[]
  csrf: string
  site_name: string
}

export interface PayStatus {
  paid: boolean
  expired: boolean
}

export async function fetchPay(invoiceId: string | number): Promise<PayData> {
  const res = await http.get<Partial<PayData>>(`/pay/${invoiceId}?format=json`)
  if (!res || typeof res !== 'object' || !res.invoice) {
    throw new Error(String((res as { msg?: string } | undefined)?.msg || '支付页面数据格式错误'))
  }
  return {
    ok: Number(res.ok || 0),
    invoice: {
      id: String(res.invoice.id || invoiceId),
      no: String(res.invoice.no || ''),
      amount: String(res.invoice.amount || '0.00'),
      credit: String(res.invoice.credit || '0.00'),
      remaining: String(res.invoice.remaining || res.invoice.amount || '0.00'),
      status: String(res.invoice.status || ''),
      recharge: Boolean(res.invoice.recharge),
    },
    paid: Boolean(res.paid),
    expired: Boolean(res.expired),
    balance_pay: Boolean(res.balance_pay),
    balance: String(res.balance || '0.00'),
    gateways: Array.isArray(res.gateways) ? res.gateways : [],
    csrf: String(res.csrf || ''),
    site_name: String(res.site_name || ''),
  }
}

export async function startPay(
  invoiceId: number,
  gateway: string,
  useBalance = false,
): Promise<{ ok: number; url?: string; paid?: boolean; redirect?: string }> {
  return (await http.post(`/pay/${invoiceId}/start`, {
    gateway,
    use_balance: useBalance ? '1' : '0',
  })) as unknown as { ok: number; url?: string; paid?: boolean; redirect?: string }
}

export async function payByBalance(invoiceId: number): Promise<{ ok: number; msg?: string; redirect?: string }> {
  return (await http.post(`/pay/${invoiceId}/balance`, {})) as unknown as { ok: number; msg?: string; redirect?: string }
}

export async function payStatus(invoiceId: number): Promise<PayStatus> {
  const res = await http.get<PayStatus>(`/pay/${invoiceId}/status`)
  return res as unknown as PayStatus
}
