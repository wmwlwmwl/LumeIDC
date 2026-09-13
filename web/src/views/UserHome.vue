<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { http } from '../http/index'
import { useSession } from '../http/session'
import { formatDate, formatMoney } from '@/utils/format'
import PublicPageHead from '@/components/public/PublicPageHead.vue'
import ArtStatsCard from '@/components/core/cards/art-stats-card/index.vue'

const session = useSession()
interface Anchor {
  id?: number
  title: string
  content?: string
  pinned?: boolean
  created_at: string
}
interface Stats {
  service_count: number
  active_count: number
  paid_total: string
  unpaid_count: number
}
const stats = ref<Stats | null>(null)
const announcements = ref<Anchor[]>([])
const loading = ref(false)
const loadError = ref(false)

async function load() {
  loading.value = true
  loadError.value = false
  try {
    const res = (await http.get('/user')) as {
      stats?: Stats
      balance?: string
      announcements?: Anchor[]
    }
    stats.value = res.stats || null
    if (session.user && res.balance !== undefined) {
      session.user = { ...session.user, balance: res.balance }
    }
    announcements.value = (res.announcements || []) as Anchor[]
  } catch (err: unknown) {
    loadError.value = true
    ElMessage.error((err as Error).message || '读取概览失败')
  } finally {
    loading.value = false
  }
}

onMounted(load)

// Art 统计卡数据（与后台看板同一组件）
const statItems = computed(() => [
  {
    title: '服务总数',
    count: stats.value?.service_count ?? 0,
    icon: 'ri:server-line',
    iconStyle: 'bg-primary',
    decimals: 0,
    description: '全部服务实例',
  },
  {
    title: '激活服务',
    count: stats.value?.active_count ?? 0,
    icon: 'ri:play-circle-line',
    iconStyle: 'bg-secondary',
    decimals: 0,
    description: '运行中',
  },
  {
    title: '累计消费',
    count: Number(stats.value?.paid_total ?? 0),
    icon: 'ri:wallet-3-line',
    iconStyle: 'bg-warning',
    decimals: 2,
    description: '单位：元',
  },
  {
    title: '待支付',
    count: stats.value?.unpaid_count ?? 0,
    icon: 'ri:file-list-3-line',
    iconStyle: 'bg-danger',
    decimals: 0,
    description: '待支付账单',
  },
])
</script>

<template>
  <div v-loading="loading">
    <PublicPageHead title="账户概览" subtitle="你的资源与服务运营数据一览。" />

    <div v-if="loadError" class="user-error" role="alert">
      <span>概览数据加载失败。</span>
      <el-button size="small" @click="load">重新加载</el-button>
    </div>

    <ElRow :gutter="20">
      <ElCol v-for="s in statItems" :key="s.title" :xs="24" :sm="12" :md="6">
        <ArtStatsCard
          class="mb-5"
          :icon="s.icon"
          :icon-style="s.iconStyle"
          :title="s.title"
          :count="s.count"
          :decimals="s.decimals"
          :description="s.description"
        />
      </ElCol>
    </ElRow>

    <div v-if="announcements.length" class="art-card p-5">
      <div class="art-card-header">
        <div class="title">
          <h4>最新公告</h4>
          <p>站点通知与更新</p>
        </div>
        <RouterLink to="/notices" class="user-more">全部公告</RouterLink>
      </div>

      <div class="user-news">
        <article v-for="a in announcements" :key="a.title" class="user-news__item">
          <div class="user-news__head">
            <span class="user-news__pill" :class="{ 'is-pinned': a.pinned }">
              {{ a.pinned ? '置顶' : '通知' }}
            </span>
            <RouterLink v-if="a.id" :to="`/notices/${a.id}`" class="user-news__title">
              {{ a.title }}
            </RouterLink>
            <strong v-else>{{ a.title }}</strong>
            <time :datetime="a.created_at">{{ formatDate(a.created_at) }}</time>
          </div>
          <p v-if="a.content">{{ a.content }}</p>
        </article>
      </div>
    </div>
  </div>
</template>

<style scoped>
.user-error {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
  padding: 12px 16px;
  color: var(--el-color-danger);
  font-size: 13px;
  background: var(--el-color-danger-light-9);
  border-radius: var(--radius-md);
}

.user-more {
  flex-shrink: 0;
  color: var(--theme-color);
  font-size: 13px;
}

.user-news {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 16px;
}

.user-news__item {
  padding: 12px 14px;
  background: var(--art-gray-50);
  border: 1px solid var(--art-card-border);
  border-radius: var(--radius-md);
}

.user-news__head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.user-news__head strong,
.user-news__title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  color: var(--art-gray-800);
  font-size: 13px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-news__title:hover {
  color: var(--theme-color);
}

.user-news__head time {
  margin-left: auto;
  flex-shrink: 0;
  color: var(--art-gray-500);
  font-size: 11px;
}

.user-news__item p {
  margin: 8px 0 0;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.7;
}

.user-news__pill {
  padding: 2px 7px;
  flex-shrink: 0;
  color: var(--theme-color);
  font-size: 11px;
  background: var(--theme-color-soft);
  border-radius: var(--radius-sm);
}

.user-news__pill.is-pinned {
  color: var(--el-color-warning);
  background: var(--el-color-warning-light-9);
}
</style>
