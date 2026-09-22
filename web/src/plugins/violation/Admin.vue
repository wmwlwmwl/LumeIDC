<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, ElButton, ElTag } from 'element-plus'
import type { ColumnOption } from '@/types'
import ArtStatsCard from '@/components/core/cards/art-stats-card/index.vue'
import RecordForm from './RecordForm.vue'
import { http } from '@/http'
import { useAdminRequest } from '@/admin/useAdminTable'

interface RecordRow {
  id: number
  user_id: number
  user_name?: string
  user_email?: string
  type?: string
  level?: string
  level_label?: string
  description?: string
  evidence_url?: string
  action?: string
  public?: boolean
  starts_at?: string
  expires_at?: string
  created_at?: string
  handled_by?: string
  note?: string
}

interface AnnouncementRow {
  id: number
  title: string
  content?: string
  pinned?: boolean
  hidden?: boolean
  reads?: number
  created_at?: string
  updated_at?: string
}

interface FormOptions {
  types: string[]
  actions: string[]
  levels: Record<string, string>
  defaultPublic: boolean
}

interface Stats {
  total: number
  active: number
  public: number
}

// D-02 修复：列表/统计/公告三个数据源原共享一个代际守卫，并发拉取时后启动者会把
// 先启动者的响应判为过期而静默跳过（如 onSaved 先 loadList 后 loadStats，stats 响应
// 回来时 list 的状态赋值被跳过 → 表格停留在旧数据）。拆为三个独立守卫，互不干扰；
// 同一数据源内部的多次调用仍保留代际语义（只采纳最新一次响应）。
const startListRequest = useAdminRequest()
const startStatsRequest = useAdminRequest()
const startAnnouncementRequest = useAdminRequest()

const activeTab = ref('list')
const showSearchBar = ref(true)

const loading = ref(false)
const list = ref<RecordRow[]>([])
const total = ref(0)
const page = ref(1)
const limit = ref(10)
const searchForm = ref<Record<string, any>>({})
const stats = ref<Stats>({ total: 0, active: 0, public: 0 })
const options = ref<FormOptions>({
  types: [],
  actions: [],
  levels: { light: '轻微', medium: '中度', severe: '严重' },
  defaultPublic: false,
})

const levelOptions = computed(() =>
  Object.entries(options.value.levels).map(([value, label]) => ({ value, label }))
)
const typeOptions = computed(() => options.value.types.map((t) => ({ value: t, label: t })))

const searchItems = computed(() => [
  { label: '关键词', key: 'keyword', type: 'input', placeholder: '用户 / 类型 / 描述' },
  {
    label: '违规等级',
    key: 'level',
    type: 'select',
    props: { placeholder: '全部等级', clearable: true, options: levelOptions.value },
  },
  {
    label: '违规类型',
    key: 'type',
    type: 'select',
    props: { placeholder: '全部类型', clearable: true, options: typeOptions.value },
  },
])

const pagination = computed(() => ({ current: page.value, size: limit.value, total: total.value }))

const LEVEL_TAG: Record<string, 'info' | 'warning' | 'danger'> = {
  light: 'info',
  medium: 'warning',
  severe: 'danger',
}

const columns = ref<ColumnOption<RecordRow>[]>([
  { prop: 'id', label: 'ID', width: 70, sortable: true },
  {
    prop: 'user_name',
    label: '用户',
    minWidth: 150,
    formatter: (row) =>
      h('div', { class: 'flex flex-col leading-tight' }, [
        h('span', { class: 'text-sm' }, row.user_name || `#${row.user_id}`),
        row.user_email ? h('span', { class: 'text-xs text-g-500' }, row.user_email) : null,
      ]),
  },
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
  { prop: 'action', label: '处置措施', minWidth: 110, formatter: (row) => row.action || '—' },
  {
    prop: 'public',
    label: '公示',
    width: 90,
    formatter: (row) =>
      h(
        ElTag,
        { type: row.public ? 'success' : 'info', effect: 'light' },
        row.public ? '公示中' : '未公示'
      ),
  },
  {
    prop: 'starts_at',
    label: '有效期',
    minWidth: 200,
    formatter: (row) => {
      if (!row.starts_at && !row.expires_at) return '长期有效'
      return `${row.starts_at || '立即'} ~ ${row.expires_at || '长期'}`
    },
  },
  { prop: 'handled_by', label: '处理人', minWidth: 100, formatter: (row) => row.handled_by || '—' },
  {
    prop: 'actions',
    label: '操作',
    width: 120,
    fixed: 'right',
    formatter: (row) =>
      h('div', { class: 'flex gap-2' }, [
        h(
          ElButton,
          { size: 'small', link: true, type: 'primary', onClick: () => openEdit(row) },
          '编辑'
        ),
        h(
          ElButton,
          { size: 'small', link: true, type: 'danger', onClick: () => removeRecord(row) },
          '删除'
        ),
      ]),
  },
])

const announcementColumns = ref<ColumnOption<AnnouncementRow>[]>([
  { prop: 'id', label: 'ID', width: 70, sortable: true },
  { prop: 'title', label: '标题', minWidth: 200 },
  {
    prop: 'pinned',
    label: '置顶',
    width: 80,
    formatter: (row) =>
      row.pinned ? h(ElTag, { type: 'warning', effect: 'light' }, '置顶') : h('span', { class: 'text-g-400' }, '—'),
  },
  {
    prop: 'hidden',
    label: '状态',
    width: 90,
    formatter: (row) =>
      h(
        ElTag,
        { type: row.hidden ? 'danger' : 'success', effect: 'light' },
        row.hidden ? '已隐藏' : '显示中'
      ),
  },
  { prop: 'reads', label: '阅读量', width: 90, sortable: true },
  { prop: 'updated_at', label: '更新时间', minWidth: 160, sortable: true },
  {
    prop: 'actions',
    label: '操作',
    width: 120,
    fixed: 'right',
    formatter: (row) =>
      h('div', { class: 'flex gap-2' }, [
        h(
          ElButton,
          { size: 'small', link: true, type: 'primary', onClick: () => openAnnouncementEdit(row) },
          '编辑'
        ),
        h(
          ElButton,
          { size: 'small', link: true, type: 'danger', onClick: () => removeAnnouncement(row) },
          '删除'
        ),
      ]),
  },
])

const announcements = ref<AnnouncementRow[]>([])
const announcementLoading = ref(false)
const announcementDrawer = ref(false)
const announcementSaving = ref(false)
const announcementForm = reactive({ id: 0, title: '', content: '', pinned: false, hidden: false })

const drawerVisible = ref(false)
const editingId = ref<number | undefined>(undefined)

async function loadOptions() {
  try {
    const res = await http.get<{ ok: number; options?: FormOptions }>('/plugin/violation/form')
    if (res.options) options.value = res.options
  } catch {
    /* 选项拉取失败时沿用内置默认等级 */
  }
}

async function loadStats() {
  const isCurrent = startStatsRequest()
  if (!isCurrent) return
  try {
    const res = await http.get<{ ok: number; stats?: Stats }>('/plugin/violation/stats')
    if (!isCurrent()) return
    if (res.stats) stats.value = res.stats
  } catch {
    /* 统计失败不影响列表展示 */
  }
}

async function loadList() {
  const isCurrent = startListRequest()
  if (!isCurrent) return
  loading.value = true
  try {
    const res = await http.get<{ ok: number; list?: RecordRow[]; total?: number }>(
      '/plugin/violation/list',
      { ...searchForm.value, page: page.value, limit: limit.value }
    )
    if (!isCurrent()) return
    list.value = res.list || []
    total.value = res.total || 0
  } catch (err: unknown) {
    if (isCurrent()) ElMessage.error((err as Error).message || '读取失败')
  } finally {
    if (isCurrent()) loading.value = false
  }
}

async function loadAnnouncements() {
  const isCurrent = startAnnouncementRequest()
  if (!isCurrent) return
  announcementLoading.value = true
  try {
    const res = await http.get<{ ok: number; list?: AnnouncementRow[] }>(
      '/plugin/violation/announcements'
    )
    if (!isCurrent()) return
    announcements.value = res.list || []
  } catch (err: unknown) {
    if (isCurrent()) ElMessage.error((err as Error).message || '读取失败')
  } finally {
    if (isCurrent()) announcementLoading.value = false
  }
}

function handleSearch(params: Record<string, any>) {
  searchForm.value = { ...params }
  page.value = 1
  loadList()
}

function handleReset() {
  searchForm.value = {}
  page.value = 1
  loadList()
}

function handlePageChange(p: number) {
  page.value = p
  loadList()
}

function handleSizeChange(s: number) {
  limit.value = s
  page.value = 1
  loadList()
}

function openNew() {
  editingId.value = undefined
  drawerVisible.value = true
}

function openEdit(row: RecordRow) {
  editingId.value = row.id
  drawerVisible.value = true
}

function onSaved() {
  drawerVisible.value = false
  activeTab.value = 'list'
  loadList()
  loadStats()
}

async function removeRecord(row: RecordRow) {
  const ok = await ElMessageBox.confirm(
    `确认删除用户 #${row.user_id} 的违规记录？删除后不可恢复。`,
    '删除确认',
    { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
  ).catch(() => null)
  if (!ok) return
  try {
    const res = await http.post<{ ok: number; msg?: string }>(
      `/plugin/violation/${row.id}/delete`
    )
    if (String(res.ok) !== '1') {
      ElMessage.error(res.msg || '删除失败')
      return
    }
    ElMessage.success('已删除')
    if (list.value.length === 1 && page.value > 1) page.value -= 1
    loadList()
    loadStats()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}

function openAnnouncementNew() {
  Object.assign(announcementForm, { id: 0, title: '', content: '', pinned: false, hidden: false })
  announcementDrawer.value = true
}

function openAnnouncementEdit(row: AnnouncementRow) {
  Object.assign(announcementForm, {
    id: row.id,
    title: row.title || '',
    content: row.content || '',
    pinned: !!row.pinned,
    hidden: !!row.hidden,
  })
  announcementDrawer.value = true
}

async function saveAnnouncement() {
  if (!announcementForm.title.trim()) {
    ElMessage.error('请填写公告标题')
    return
  }
  announcementSaving.value = true
  try {
    const res = await http.post<{ ok: number; msg?: string }>(
      '/plugin/violation/announcements/save',
      {
        id: announcementForm.id,
        title: announcementForm.title.trim(),
        content: announcementForm.content.trim(),
        pinned: announcementForm.pinned,
        hidden: announcementForm.hidden,
      }
    )
    if (String(res.ok) !== '1') {
      ElMessage.error(res.msg || '保存失败')
      return
    }
    ElMessage.success('已保存')
    announcementDrawer.value = false
    loadAnnouncements()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    announcementSaving.value = false
  }
}

async function removeAnnouncement(row: AnnouncementRow) {
  const ok = await ElMessageBox.confirm(`确认删除公告「${row.title}」？`, '删除确认', {
    type: 'warning',
    confirmButtonText: '删除',
    cancelButtonText: '取消',
  }).catch(() => null)
  if (!ok) return
  try {
    const res = await http.post<{ ok: number; msg?: string }>(
      `/plugin/violation/announcements/${row.id}/delete`
    )
    if (String(res.ok) !== '1') {
      ElMessage.error(res.msg || '删除失败')
      return
    }
    ElMessage.success('已删除')
    loadAnnouncements()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}

onMounted(() => {
  loadOptions()
  loadStats()
  loadList()
})
</script>

<template>
  <div class="art-full-height">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>违规管理</h4>
            <p>记录用户违规行为，管理前台违规公示公告。</p>
          </div>
        </div>
      </template>

      <el-tabs v-model="activeTab" class="violation-tabs">
        <el-tab-pane label="违规列表" name="list">
          <ArtSearchBar
            v-show="showSearchBar"
            v-model="searchForm"
            :items="searchItems"
            @search="handleSearch"
            @reset="handleReset"
          />

          <ElRow :gutter="20" class="mb-5">
            <ElCol :xs="24" :sm="8">
              <ArtStatsCard
                icon="ri:error-warning-line"
                icon-style="bg-warning"
                title="记录总数"
                :count="stats.total"
                description="全部违规记录"
              />
            </ElCol>
            <ElCol :xs="24" :sm="8">
              <ArtStatsCard
                icon="ri:shield-check-line"
                icon-style="bg-primary"
                title="生效中"
                :count="stats.active"
                description="当前生效的违规记录"
              />
            </ElCol>
            <ElCol :xs="24" :sm="8">
              <ArtStatsCard
                icon="ri:eye-line"
                icon-style="bg-secondary"
                title="公示中"
                :count="stats.public"
                description="前台公示展示中"
              />
            </ElCol>
          </ElRow>

          <ElCard class="art-table-card" :style="{ marginTop: showSearchBar ? '12px' : '0' }">
            <ArtTableHeader
              v-model:columns="columns"
              v-model:showSearchBar="showSearchBar"
              :loading="loading"
              @refresh="loadList"
            >
              <template #left>
                <el-button type="primary" @click="openNew">
                  <el-icon><Plus /></el-icon>添加违规
                </el-button>
              </template>
            </ArtTableHeader>
            <ArtTable
              :loading="loading"
              :data="list"
              :columns="columns"
              :pagination="pagination"
              :pagination-options="{ pageSizes: [10, 20, 50] }"
              empty-text="暂无违规记录"
              @pagination:current-change="handlePageChange"
              @pagination:size-change="handleSizeChange"
            />
          </ElCard>
        </el-tab-pane>

        <el-tab-pane label="添加违规" name="add">
          <ElCard class="art-card">
            <template #header>
              <div class="art-card-header">
                <div class="title">
                  <h4>添加违规</h4>
                  <p>为用户新增一条违规记录，可立即公示或仅作内部记录。</p>
                </div>
              </div>
            </template>
            <RecordForm v-if="activeTab === 'add'" @saved="onSaved" />
          </ElCard>
        </el-tab-pane>

        <el-tab-pane label="公告设置" name="announcement">
          <ElCard class="art-table-card">
            <ArtTableHeader
              v-model:columns="announcementColumns"
              :loading="announcementLoading"
              @refresh="loadAnnouncements"
            >
              <template #left>
                <el-button type="primary" @click="openAnnouncementNew">
                  <el-icon><Plus /></el-icon>新增公告
                </el-button>
              </template>
            </ArtTableHeader>
            <ArtTable
              :loading="announcementLoading"
              :data="announcements"
              :columns="announcementColumns"
              empty-text="暂无公告"
            />
          </ElCard>
        </el-tab-pane>
      </el-tabs>
    </ElCard>

    <el-drawer v-model="drawerVisible" :title="editingId ? '编辑违规记录' : '添加违规记录'" size="620px">
      <RecordForm v-if="drawerVisible" :record-id="editingId" @saved="onSaved" />
    </el-drawer>

    <el-drawer v-model="announcementDrawer" title="公告" size="620px">
      <el-form v-if="announcementDrawer" label-position="top" :model="announcementForm" @submit.prevent>
        <el-form-item label="公告标题" required>
          <el-input
            v-model="announcementForm.title"
            maxlength="120"
            show-word-limit
            placeholder="前台违规公示页展示的标题"
          />
        </el-form-item>
        <el-form-item label="公告内容" required>
          <el-input
            v-model="announcementForm.content"
            type="textarea"
            :rows="8"
            maxlength="5000"
            show-word-limit
            placeholder="支持换行的纯文本内容"
          />
        </el-form-item>
        <div class="announcement-switches">
          <el-form-item label="置顶">
            <el-switch v-model="announcementForm.pinned" />
          </el-form-item>
          <el-form-item label="隐藏">
            <el-switch v-model="announcementForm.hidden" />
          </el-form-item>
        </div>
        <div class="form-actions">
          <el-button type="primary" :loading="announcementSaving" @click="saveAnnouncement">
            保存
          </el-button>
          <el-button :disabled="announcementSaving" @click="announcementDrawer = false">
            取消
          </el-button>
        </div>
      </el-form>
    </el-drawer>
  </div>
</template>

<style scoped>
.violation-tabs {
  margin-top: 4px;
}

.announcement-switches {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 20px;
}

.form-actions {
  display: flex;
  gap: 12px;
}

@media (max-width: 600px) {
  :deep(.el-drawer) {
    width: 100% !important;
  }
}
</style>
