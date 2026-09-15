import { ref } from 'vue'
import { useSession } from '@/http/session'
import { fetchUnreadNotificationCount } from '@/api/user'

/**
 * 前台站内消息未读数（全局唯一）。
 *
 * 顶栏角标与消息中心各存一份 ref 时，消息中心标记已读后角标仍停在首次
 * 加载的旧值（见 /notifications：页面显示 6、角标还是 7），故收敛到这里。
 */
const unread = ref(0)

/** 未读数（角标直接渲染它）。 */
export const unreadNotifications = unread

/**
 * 从服务端取未读数。
 *
 * 服务端 /notifications/unread-count 返回的是「全部未读」，与消息中心按
 * 分类/关键词筛选出的未读数不是同一个值，所以角标只能以此接口为准。
 * 未登录（含会话掉线）或请求失败一律按 0 处理，避免残留旧角标。
 */
export async function refreshUnreadNotifications(): Promise<void> {
  if (!useSession().user) {
    unread.value = 0
    return
  }
  try {
    unread.value = await fetchUnreadNotificationCount()
  } catch {
    unread.value = 0
  }
}
