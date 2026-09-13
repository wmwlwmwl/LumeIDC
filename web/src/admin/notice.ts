import { ref } from 'vue'
import { fetchAdminNotifications, type AdminNotifications } from './api'

/**
 * 后台通知中心共享状态：顶栏角标与通知面板共用同一份数据，避免重复请求。
 * 打开面板时刷新，另有 60s 轮询保持角标最新。
 */
export const adminNotifications = ref<AdminNotifications>({
  ok: 0,
  pending: 0,
  todos: [],
  messages: [],
  notices: [],
})

let started = false

export async function loadAdminNotifications(): Promise<void> {
  try {
    adminNotifications.value = await fetchAdminNotifications()
  } catch {
    /* 静默：通知中心不可用不应打扰操作 */
  }
}

export function startAdminNoticePolling(): void {
  if (started) return
  started = true
  void loadAdminNotifications()
  setInterval(loadAdminNotifications, 60000)
}
