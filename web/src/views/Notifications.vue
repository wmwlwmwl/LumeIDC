<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Bell, Check, Delete, Search } from '@element-plus/icons-vue'
import {
  deleteAllNotifications,
  deleteNotification,
  fetchNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  type NotificationItem,
} from '../api/user'
import PublicPageHead from '@/components/public/PublicPageHead.vue'
import { refreshUnreadNotifications } from '@/components/public/useUnreadNotifications'

const list = ref<NotificationItem[]>([])
const categories = ref<{ key: string; label: string }[]>([])
const total = ref(0)
const unreadCount = ref(0)
const page = ref(1)
const pageSize = ref(10)
const category = ref('')
const keyword = ref('')
const loading = ref(false)
const loadError = ref(false)

async function load() {
  loading.value = true
  loadError.value = false
  try {
    const data = await fetchNotifications({
      category: category.value,
      keyword: keyword.value.trim(),
      page: page.value,
      limit: pageSize.value,
    })
    list.value = data.list || []
    categories.value = data.categories || []
    total.value = data.total || 0
    unreadCount.value = data.unread || 0
  } catch (err: unknown) {
    loadError.value = true
    ElMessage.error((err as Error).message || '读取消息失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)
watch([category, keyword], () => {
  page.value = 1
  void load()
})

async function markRead(item: NotificationItem) {
  if (item.read) return
  try {
    await markNotificationRead(item.id)
    item.read = true
    unreadCount.value = Math.max(0, unreadCount.value - 1)
    // 顶栏角标取的是「全部未读」，分类/关键词筛选下不等于本页的未读数，只能重新取。
    void refreshUnreadNotifications()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  }
}

async function markAllRead() {
  try {
    await markAllNotificationsRead()
    list.value.forEach((item) => (item.read = true))
    unreadCount.value = 0
    void refreshUnreadNotifications()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  }
}

async function remove(item: NotificationItem) {
  try {
    await deleteNotification(item.id)
    await load()
    void refreshUnreadNotifications()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}

async function removeAll() {
  const ok = await ElMessageBox.confirm('确认删除全部站内消息？该操作不可恢复。', '删除全部消息', {
    type: 'warning',
    confirmButtonText: '确认删除',
    cancelButtonText: '取消',
  }).catch(() => null)
  if (!ok) return
  try {
    await deleteAllNotifications()
    await load()
    void refreshUnreadNotifications()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}
</script>

<template>
  <div>
    <PublicPageHead title="消息中心" :subtitle="unreadCount ? `${unreadCount} 条未读消息` : '全部已读，一切正常'">
      <template #extra>
        <span class="notif-count" :class="{ 'has-unread': unreadCount > 0 }">
          <el-icon><Bell /></el-icon>{{ unreadCount }}
        </span>
      </template>
    </PublicPageHead>

    <div class="art-card notification-toolbar">
      <div class="notification-filters">
        <button
          v-for="item in categories"
          :key="item.key"
          type="button"
          class="notification-filter"
          :class="{ 'is-active': category === item.key }"
          @click="category = item.key"
        >
          {{ item.label }}
        </button>
      </div>
      <div class="notification-actions">
        <el-input
          v-model="keyword"
          aria-label="搜索消息"
          clearable
          placeholder="搜索消息"
          class="notification-search"
        >
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-button v-if="unreadCount" :icon="Check" @click="markAllRead">全部已读</el-button>
        <el-button v-if="total" :icon="Delete" type="danger" plain @click="removeAll">清空</el-button>
      </div>
    </div>

    <div v-loading="loading" class="notif-list">
      <article v-for="n in list" :key="n.id" class="art-card notif-item" :class="{ 'is-unread': !n.read }">
        <span class="notif-item__dot" />
        <div class="notif-item__body">
          <button type="button" class="notif-item__content" @click="markRead(n)">
            <strong>{{ n.title }}</strong><time>{{ n.time }}</time>
          </button>
          <div class="notif-item__meta">
            <span class="notif-item__category">{{ categories.find((c) => c.key === n.category)?.label || '系统' }}</span>
            <el-button v-if="!n.read" size="small" text type="primary" @click.stop="markRead(n)">标记已读</el-button>
            <el-button size="small" text type="danger" @click.stop="remove(n)">删除</el-button>
          </div>
          <p v-if="n.body">{{ n.body }}</p>
        </div>
      </article>
      <div v-if="loadError" class="notif-state" role="alert">
        <p>消息加载失败，请稍后重试。</p>
        <el-button size="small" @click="load">重新加载</el-button>
      </div>
      <el-empty v-else-if="!list.length && !loading" description="暂无消息" />
    </div>

    <div v-if="total > pageSize" class="notification-pager">
      <el-pagination
        layout="total, prev, pager, next"
        :total="total"
        :page-size="pageSize"
        :current-page="page"
        background
        @current-change="(value: number) => { page = value; load() }"
      />
    </div>
  </div>
</template>

<style scoped>
.notif-count {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 8px 11px;
  color: var(--art-gray-500);
  font-size: 12px;
  font-weight: 600;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
}

.notif-count.has-unread {
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-color: var(--theme-color);
}

.notification-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  margin-bottom: 14px;
  padding: 12px 14px;
}

.notification-filters,
.notification-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.notification-filters {
  flex-wrap: wrap;
}

.notification-filter {
  padding: 7px 14px;
  color: var(--art-gray-600);
  font-size: 13px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: 999px;
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease, border-color 0.16s ease;
}

.notification-filter:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-color: color-mix(in srgb, var(--theme-color) 40%, var(--art-card-border));
}

.notification-filter.is-active {
  color: var(--theme-color-contrast);
  background: var(--theme-color);
  border-color: var(--theme-color);
}

.notification-search {
  width: 220px;
}

.notif-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.notif-item {
  display: flex;
  gap: 11px;
  padding: 15px 17px;
  transition: border-color 0.16s ease, background 0.16s ease;
}

.notif-item:hover {
  border-color: color-mix(in srgb, var(--theme-color) 30%, var(--art-card-border));
}

.notif-item.is-unread {
  border-color: var(--theme-color);
  background: var(--theme-color-soft);
}

.notif-item__dot {
  width: 7px;
  height: 7px;
  margin-top: 6px;
  flex-shrink: 0;
  background: var(--theme-color);
  border-radius: 50%;
}

.notif-item:not(.is-unread) .notif-item__dot {
  background: var(--art-gray-300);
}

.notif-item__body {
  min-width: 0;
  flex: 1;
}

.notif-item__content {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 100%;
  padding: 0;
  text-align: left;
  background: transparent;
  border: 0;
  cursor: pointer;
}

.notif-item__content strong {
  overflow: hidden;
  color: var(--art-gray-800);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.notif-item__content time {
  margin-left: auto;
  flex-shrink: 0;
  color: var(--art-gray-500);
  font-size: 11px;
}

.notif-item__meta {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 6px;
}

.notif-item__category {
  padding: 2px 7px;
  color: var(--theme-color);
  font-size: 11px;
  background: var(--theme-color-soft);
  border-radius: var(--radius-sm);
}

.notif-item__body p {
  margin: 6px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.7;
  white-space: pre-wrap;
}

.notif-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  padding: 44px 20px;
  text-align: center;
}

.notif-state p {
  margin: 0;
  color: var(--art-gray-600);
  font-size: 14px;
}

.notification-pager {
  display: flex;
  justify-content: center;
  margin-top: 20px;
}

@media (max-width: 640px) {
  .notification-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .notification-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .notification-search {
    width: 100%;
  }
}
</style>
