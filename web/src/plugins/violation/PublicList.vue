<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { ElMessage, ElTag, ElButton } from 'element-plus'
import { Bell, Document } from '@element-plus/icons-vue'
import { useWindowSize } from '@vueuse/core'
import type { ColumnOption } from '@/types'
import PublicPageHead from '@/components/public/PublicPageHead.vue'
import { http } from '@/http'

interface AnnouncementItem {
  id: number
  title: string
  content?: string
  pinned?: boolean
  hidden?: boolean
  reads?: number
  created_at?: string
  updated_at?: string
  kind: 'announcement'
}

interface RecordItem {
  id: number
  user_id: number
  user_name?: string
  type?: string
  level?: string
  level_label?: string
  description?: string
  evidence_url?: string
  action?: string
  starts_at?: string
  expires_at?: string
  created_at?: string
  kind: 'record'
}

type FeedItem = AnnouncementItem | RecordItem

const { width } = useWindowSize()
const isMobile = computed(() => width.value < 768)

const loading = ref(false)
const list = ref<FeedItem[]>([])
const total = ref(0)
const page = ref(1)
const limit = ref(10)

const announcements = computed(() => list.value.filter((i) => i.kind === 'announcement') as AnnouncementItem[])
const records = computed(() => list.value.filter((i) => i.kind === 'record') as RecordItem[])

// 前台页面父容器无确定高度，ArtTable 不能用默认的 height:100%（会塌缩把行裁掉），
// 按行数计算表格高度：每行约 44px + 表头 46px，限制在 120~460px
const fitH = (n: number) => Math.min(460, Math.max(120, n * 44 + 46))

const announcementDialog = ref(false)
const recordDialog = ref(false)
const currentAnnouncement = ref<AnnouncementItem | null>(null)
const currentRecord = ref<RecordItem | null>(null)
const detailLoading = ref(false)

const LEVEL_TAG: Record<string, 'info' | 'warning' | 'danger'> = {
  light: 'info',
  medium: 'warning',
  severe: 'danger',
}

const columns = ref<ColumnOption<RecordItem>[]>([
  { prop: 'id', label: 'ID', width: 70 },
  { prop: 'user_name', label: '用户', minWidth: 120, formatter: (row) => row.user_name || `用户#${row.user_id}` },
  { prop: 'type', label: '违规类型', minWidth: 110, formatter: (row) => row.type || '—' },
  {
    prop: 'level',
    label: '等级',
    width: 90,
    formatter: (row) =>
      h(
        ElTag,
        { type: LEVEL_TAG[row.level || ''] || 'info', effect: 'light' },
        row.level_label || row.level || '—'
      ),
  },
  { prop: 'action', label: '处置措施', minWidth: 100, formatter: (row) => row.action || '—' },
  {
    prop: 'starts_at',
    label: '有效期',
    minWidth: 180,
    formatter: (row) => {
      if (!row.starts_at && !row.expires_at) return '长期有效'
      return `${row.starts_at || '立即'} ~ ${row.expires_at || '长期'}`
    },
  },
  { prop: 'created_at', label: '公示时间', minWidth: 150 },
  {
    prop: 'actions',
    label: '操作',
    width: 90,
    fixed: 'right',
    formatter: (row) =>
      h(
        ElButton,
        { size: 'small', link: true, type: 'primary', onClick: () => openRecord(row) },
        '详情'
      ),
  },
])

async function load() {
  loading.value = true
  try {
    const res = await http.get<{ ok: number; list?: FeedItem[]; total?: number }>(
      '/plugin/violation/feed',
      { page: page.value, limit: limit.value }
    )
    list.value = res.list || []
    total.value = res.total || 0
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取失败')
  } finally {
    loading.value = false
  }
}

function handlePageChange(p: number) {
  page.value = p
  load()
}

async function openAnnouncement(row: AnnouncementItem) {
  currentAnnouncement.value = row
  announcementDialog.value = true
  detailLoading.value = true
  try {
    const res = await http.get<{ ok: number; item?: AnnouncementItem }>(
      `/plugin/violation/announcement/${row.id}`
    )
    if (res.item) {
      currentAnnouncement.value = { ...row, ...res.item }
      row.reads = res.item.reads ?? (row.reads || 0) + 1
    }
  } catch {
    /* 详情拉取失败时沿用列表内容 */
  } finally {
    detailLoading.value = false
  }
}

function openRecord(row: RecordItem) {
  currentRecord.value = row
  recordDialog.value = true
}

onMounted(load)
</script>

<template>
  <div>
    <PublicPageHead title="违规公示" subtitle="平台公示的用户违规记录与公告，请遵守服务条款。" />

    <div v-loading="loading" class="announcement-list">
      <div
        v-for="item in announcements"
        :key="`announcement-${item.id}`"
        class="art-card announcement-item"
        :class="{ 'is-pinned': item.pinned }"
        role="button"
        tabindex="0"
        @click="openAnnouncement(item)"
        @keyup.enter="openAnnouncement(item)"
      >
        <div class="announcement-item__head">
          <span class="announcement-item__icon"><el-icon><Bell /></el-icon></span>
          <strong class="announcement-item__title">{{ item.title }}</strong>
          <el-tag v-if="item.pinned" type="warning" effect="light" size="small">置顶</el-tag>
        </div>
        <p class="announcement-item__content">{{ item.content }}</p>
        <small class="announcement-item__meta">
          发布于 {{ item.created_at }} · 阅读 {{ item.reads || 0 }}
        </small>
      </div>
    </div>

    <section class="records">
      <h3 class="section-title">违规记录</h3>

      <ArtTable
        v-if="!isMobile"
        :loading="loading"
        :data="records"
        :columns="columns"
        :height="fitH(records.length)"
        empty-height="120px"
        empty-text="暂无公示的违规记录"
      />

      <div v-else v-loading="loading" class="record-cards">
        <div v-for="item in records" :key="`record-${item.id}`" class="art-card record-card">
          <div class="record-card__head">
            <span class="record-card__icon"><el-icon><Document /></el-icon></span>
            <div class="record-card__title">
              <strong>{{ item.type || '违规记录' }}</strong>
              <small>#{{ item.id }} · {{ item.user_name || `用户#${item.user_id}` }}</small>
            </div>
            <el-tag :type="LEVEL_TAG[item.level || ''] || 'info'" effect="light" size="small">
              {{ item.level_label || item.level }}
            </el-tag>
          </div>
          <p class="record-card__desc">{{ item.description }}</p>
          <div class="record-card__meta">
            <span>{{ item.action || '未处置' }}</span>
            <span>{{ item.starts_at || '立即' }} ~ {{ item.expires_at || '长期' }}</span>
          </div>
          <el-button size="small" link type="primary" @click="openRecord(item)">查看详情</el-button>
        </div>
        <el-empty v-if="!loading && !records.length" description="暂无公示的违规记录" />
      </div>

      <div v-if="total > limit" class="pager">
        <el-pagination
          background
          layout="total, prev, pager, next"
          :total="total"
          :page-size="limit"
          :current-page="page"
          @current-change="handlePageChange"
        />
      </div>
    </section>

    <el-dialog v-model="announcementDialog" :title="currentAnnouncement?.title || '公告'" width="600px">
      <div v-loading="detailLoading" class="detail-content">{{ currentAnnouncement?.content }}</div>
      <template #footer>
        <el-button @click="announcementDialog = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="recordDialog" title="违规记录详情" width="600px">
      <el-descriptions v-if="currentRecord" :column="1" border>
        <el-descriptions-item label="用户">
          {{ currentRecord.user_name || `用户#${currentRecord.user_id}` }}
        </el-descriptions-item>
        <el-descriptions-item label="违规类型">{{ currentRecord.type || '—' }}</el-descriptions-item>
        <el-descriptions-item label="违规等级">
          {{ currentRecord.level_label || currentRecord.level || '—' }}
        </el-descriptions-item>
        <el-descriptions-item label="处置措施">{{ currentRecord.action || '—' }}</el-descriptions-item>
        <el-descriptions-item label="有效期">
          {{ currentRecord.starts_at || '立即' }} ~ {{ currentRecord.expires_at || '长期' }}
        </el-descriptions-item>
        <el-descriptions-item label="违规描述">{{ currentRecord.description }}</el-descriptions-item>
        <el-descriptions-item v-if="currentRecord.evidence_url" label="凭证">
          <el-link :href="currentRecord.evidence_url" target="_blank" rel="noopener" type="primary">
            {{ currentRecord.evidence_url }}
          </el-link>
        </el-descriptions-item>
      </el-descriptions>
      <template #footer>
        <el-button @click="recordDialog = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.announcement-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  margin-bottom: 22px;
}

.announcement-item {
  padding: 16px 18px;
  cursor: pointer;
  border-radius: var(--radius-lg);
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.announcement-item:hover {
  border-color: color-mix(in srgb, var(--theme-color) 32%, var(--art-card-border));
  box-shadow: 0 12px 28px color-mix(in srgb, var(--art-gray-900) 8%, transparent);
}

.announcement-item.is-pinned {
  border-color: color-mix(in srgb, var(--el-color-warning) 45%, var(--art-card-border));
}

.announcement-item__head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.announcement-item__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 30px;
  height: 30px;
  flex-shrink: 0;
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-radius: var(--radius-md);
}

.announcement-item__title {
  min-width: 0;
  overflow: hidden;
  color: var(--art-gray-800);
  font-size: 14px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.announcement-item__content {
  margin: 8px 0 6px;
  overflow: hidden;
  color: var(--art-gray-500);
  font-size: 12px;
  line-height: 1.6;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
}

.announcement-item__meta {
  color: var(--art-gray-400);
  font-size: 11px;
}

.section-title {
  margin: 0 0 12px;
  color: var(--art-gray-800);
  font-size: 15px;
  font-weight: 600;
}

.record-cards {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.record-card {
  padding: 16px;
  border-radius: var(--radius-lg);
}

.record-card__head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.record-card__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  flex-shrink: 0;
  color: var(--el-color-danger);
  background: var(--el-color-danger-light-9);
  border-radius: var(--radius-md);
}

.record-card__title {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
}

.record-card__title strong {
  overflow: hidden;
  color: var(--art-gray-800);
  font-size: 14px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.record-card__title small {
  color: var(--art-gray-500);
  font-size: 11px;
}

.record-card__desc {
  margin: 10px 0;
  color: var(--art-gray-600);
  font-size: 12px;
  line-height: 1.6;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  -webkit-box-orient: vertical;
}

.record-card__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
  margin-bottom: 6px;
  color: var(--art-gray-500);
  font-size: 11px;
}

.detail-content {
  color: var(--art-gray-700);
  font-size: 13px;
  line-height: 1.8;
  white-space: pre-wrap;
  word-break: break-word;
}

.pager {
  display: flex;
  justify-content: center;
  margin-top: 18px;
}

@media (max-width: 640px) {
  :deep(.el-dialog) {
    width: calc(100% - 24px) !important;
    min-width: 0;
  }
}
</style>
