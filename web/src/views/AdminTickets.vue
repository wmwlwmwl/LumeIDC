<script setup lang="ts">
import { computed, onMounted, ref, h } from 'vue'
import { ElMessage, ElTag } from 'element-plus'
import { Check, User } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import ArtStatsCard from '../components/core/cards/art-stats-card/index.vue'
import {
  addAdminTicketNote,
  assignAdminTicket,
  fetchAdminTicket,
  fetchAdminTickets,
  fetchAdminTicketStats,
  fetchTicketAssignees,
  replyAdminTicket,
  updateAdminTicketStatus,
  type AdminTicket,
  type AdminTicketMessage,
  type AdminTicketAttachment,
  uploadAdminTicketAttachment,
} from '../admin/api'

import { useAdminRequest } from '../admin/useAdminTable'

type TicketDetail = AdminTicket & { body: string; assignee_id?: number; assignee_name?: string }
const list = ref<AdminTicket[]>([])
const selected = ref<TicketDetail | null>(null)
const messages = ref<AdminTicketMessage[]>([])
const attachments = ref<AdminTicketAttachment[]>([])
const assignees = ref<{ id: number; name: string }[]>([])
const loading = ref(false)
const drawer = ref(false)
const showSearchBar = ref(true)
const searchForm = ref<{
  q: string
  status: string
  priority: string
  category: string
  queue: string
  service_id?: number
  assignee_id?: number
}>({ q: '', status: '', priority: '', category: '', queue: '' })
const page = ref(1)
const total = ref(0)

// 服务端分页：排序交给后端 ORDER BY（字段经白名单映射）
const sortKey = ref('')
const sortOrder = ref<'asc' | 'desc'>('desc')
function handleSortChange({ prop, order }: { prop: string; order: 'ascending' | 'descending' | null }) {
  sortKey.value = order ? prop : ''
  sortOrder.value = order === 'ascending' ? 'asc' : 'desc'
  page.value = 1
  load()
}
const stats = ref<Record<string, number>>({})
const reply = ref('')
const replyFile = ref<File | null>(null)
const note = ref('')

const counts = computed(() => ({
  pending: stats.value.pending || 0,
  processing: stats.value.processing || 0,
  unassigned: stats.value.unassigned || 0,
  overdue: stats.value.overdue || 0,
}))

const searchItems = computed(() => [
  { label: '关键词', key: 'q', type: 'input', placeholder: '搜索主题、内容或用户邮箱', clearable: true },
  {
    label: '状态',
    key: 'status',
    type: 'select',
    props: {
      placeholder: '全部状态',
      clearable: true,
      options: [
        { label: '待处理', value: 'pending' },
        { label: '处理中', value: 'processing' },
        { label: '已关闭', value: 'closed' },
      ],
    },
  },
  {
    label: '优先级',
    key: 'priority',
    type: 'select',
    props: {
      placeholder: '全部优先级',
      clearable: true,
      options: [
        { label: '普通', value: 'normal' },
        { label: '紧急', value: 'high' },
        { label: '非常紧急', value: 'urgent' },
      ],
    },
  },
  {
    label: '分类',
    key: 'category',
    type: 'select',
    props: {
      placeholder: '全部分类',
      clearable: true,
      options: [
        { label: '技术支持', value: 'technical' },
        { label: '财务账单', value: 'billing' },
        { label: '账户安全', value: 'account' },
        { label: '售前咨询', value: 'pre-sale' },
      ],
    },
  },
  {
    label: '队列',
    key: 'queue',
    type: 'select',
    props: {
      placeholder: '全部队列',
      clearable: true,
      options: [
        { label: '我的工单', value: 'mine' },
        { label: '未分配', value: 'unassigned' },
      ],
    },
  },
  {
    label: '处理人',
    key: 'assignee_id',
    type: 'select',
    props: {
      placeholder: '全部处理人',
      clearable: true,
      options: assignees.value.map((agent) => ({ label: agent.name, value: agent.id })),
    },
  },
  { label: '服务 ID', key: 'service_id', type: 'number', props: { min: 1, controlsPosition: 'right', placeholder: '服务 ID' } },
])

function statusText(value: string) {
  return value === 'closed' ? '已关闭' : value === 'processing' ? '处理中' : '待处理'
}
function categoryText(value: string) {
  return value === 'billing'
    ? '财务账单'
    : value === 'account'
      ? '账户安全'
      : value === 'pre-sale'
        ? '售前咨询'
        : '技术支持'
}
function priorityText(value: string) {
  return value === 'urgent' ? '非常紧急' : value === 'high' ? '紧急' : '普通'
}
function priorityColor(value: string) {
  return value === 'urgent'
    ? 'color:var(--el-color-danger);font-weight:650'
    : value === 'high'
      ? 'color:var(--el-color-warning);font-weight:650'
      : 'color:var(--art-gray-600)'
}

const columns = ref<ColumnOption[]>([
  {
    prop: 'subject',
    label: '工单',
    minWidth: 280,
    sortable: 'custom',
    formatter: (row) =>
      h('div', { style: 'display:flex;align-items:center;gap:10px' }, [
        h('span', { style: 'color:var(--theme-color);font-size:11px;font-weight:700' }, `#${row.id}`),
        h('div', { style: 'display:grid;gap:5px;min-width:0' }, [
          h(
            'strong',
            { style: 'overflow:hidden;color:var(--art-gray-800);text-overflow:ellipsis;white-space:nowrap' },
            row.subject,
          ),
          h(
            'small',
            { style: 'overflow:hidden;color:var(--art-gray-400);text-overflow:ellipsis;white-space:nowrap' },
            row.email,
          ),
        ]),
      ]),
  },
  {
    prop: 'category',
    label: '分类',
    width: 110,
    sortable: 'custom',
    formatter: (row) =>
      h('span', { style: 'color:var(--art-gray-500);font-size:12px' }, categoryText(row.category)),
  },
  {
    prop: 'service_name',
    label: '服务',
    minWidth: 150,
    formatter: (row) => row.service_name || row.service_hostname || '未关联',
  },
  {
    prop: 'priority',
    label: '优先级',
    width: 100,
    sortable: 'custom',
    formatter: (row) =>
      h('span', { style: `font-size:12px;${priorityColor(row.priority)}` }, priorityText(row.priority)),
  },
  {
    prop: 'status',
    label: '状态',
    width: 100,
    sortable: 'custom',
    formatter: (row) =>
      h(
        ElTag,
        {
          type: row.status === 'closed' ? 'info' : row.status === 'pending' ? 'warning' : 'success',
          effect: 'light',
        },
        () => statusText(row.status),
      ),
  },
  {
    prop: 'assignee_name',
    label: '处理人',
    width: 120,
    formatter: (row) =>
      h(
        'span',
        { style: 'display:inline-flex;align-items:center;gap:4px;color:var(--art-gray-500);font-size:12px' },
        [h(User), row.assignee_name || '未分配'],
      ),
  },
  { prop: 'updated_at', label: '更新时间', width: 160, sortable: 'custom' },
])

const pagination = computed(() => ({ current: page.value, size: 25, total: total.value }))

const startRequest = useAdminRequest()
async function load() {
  const isCurrent = startRequest()
  if (!isCurrent) return
  loading.value = true
  try {
    const data = await fetchAdminTickets({
      q: searchForm.value.q.trim(),
      status: searchForm.value.status,
      priority: searchForm.value.priority,
      category: searchForm.value.category,
      assignee_id: searchForm.value.assignee_id,
      service_id: searchForm.value.service_id,
      queue: searchForm.value.queue,
      page: page.value,
      limit: 25,
      sort: sortKey.value || undefined,
      order: sortKey.value ? sortOrder.value : undefined,
    })
    if (!isCurrent()) return
    list.value = data.list
    total.value = data.total
    const dataStats = await fetchAdminTicketStats()
    if (isCurrent()) stats.value = dataStats
  } catch (err: unknown) {
    if (isCurrent()) ElMessage.error((err as Error).message || '读取工单失败')
  } finally {
    if (isCurrent()) loading.value = false
  }
}
async function loadAssignees() {
  try {
    assignees.value = await fetchTicketAssignees()
  } catch {
    assignees.value = []
  }
}
onMounted(async () => {
  await Promise.all([load(), loadAssignees()])
})

async function open(ticket: AdminTicket) {
  try {
    const data = await fetchAdminTicket(ticket.id)
    selected.value = data.ticket
    messages.value = data.messages || []
    attachments.value = data.attachments || []
    reply.value = ''
    replyFile.value = null
    note.value = ''
    drawer.value = true
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取工单失败')
  }
}
async function send() {
  if (!selected.value || (!reply.value.trim() && !replyFile.value)) return
  try {
    await replyAdminTicket(selected.value.id, reply.value.trim(), replyFile.value || undefined)
    reply.value = ''
    replyFile.value = null
    await open(selected.value)
    await load()
    ElMessage.success('回复已发送')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '回复失败')
  }
}
async function saveNote() {
  if (!selected.value || !note.value.trim()) return
  try {
    await addAdminTicketNote(selected.value.id, note.value.trim())
    note.value = ''
    await open(selected.value)
    ElMessage.success('内部备注已保存')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  }
}
async function changeStatus(value: string) {
  if (!selected.value) return
  try {
    await updateAdminTicketStatus(selected.value.id, value)
    selected.value.status = value
    await load()
    ElMessage.success('状态已更新')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '更新失败')
  }
}
async function changeAssignee(value: number) {
  if (!selected.value) return
  try {
    await assignAdminTicket(selected.value.id, value)
    selected.value.assignee_id = value
    ElMessage.success('分配成功')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '分配失败')
  }
}
async function uploadAttachment(event: Event) {
  if (!selected.value) return
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    await uploadAdminTicketAttachment(selected.value.id, file)
    await open(selected.value)
    ElMessage.success('附件已上传')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '上传失败')
  } finally {
    input.value = ''
  }
}
function chooseReplyAttachment(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (file) replyFile.value = file
}
function handleSearch() {
  page.value = 1
  void load()
}
function handleReset() {
  searchForm.value = { q: '', status: '', priority: '', category: '', queue: '' }
  page.value = 1
  void load()
}
function handlePageChange(value: number) {
  page.value = value
  void load()
}
function toggleUnassigned() {
  searchForm.value.queue = searchForm.value.queue === 'unassigned' ? '' : 'unassigned'
  page.value = 1
  void load()
}
</script>

<template>
  <div class="art-full-height">
    <ElRow :gutter="20">
      <ElCol :xs="12" :sm="12" :md="6">
        <ArtStatsCard class="mb-5" icon="ri:inbox-unarchive-line" icon-style="bg-warning" title="待处理" :count="counts.pending" description="需要客服接手" />
      </ElCol>
      <ElCol :xs="12" :sm="12" :md="6">
        <ArtStatsCard class="mb-5" icon="ri:loader-4-line" icon-style="bg-primary" title="处理中" :count="counts.processing" description="正在跟进" />
      </ElCol>
      <ElCol :xs="12" :sm="12" :md="6">
        <ArtStatsCard
          class="mb-5 cursor-pointer"
          :box-style="searchForm.queue === 'unassigned' ? 'ring-2 ring-[var(--theme-color)]' : ''"
          icon="ri:user-received-line"
          icon-style="bg-secondary"
          title="未分配"
          :count="counts.unassigned"
          description="等待领取（点击筛选）"
          @click="toggleUnassigned"
        />
      </ElCol>
      <ElCol :xs="12" :sm="12" :md="6">
        <ArtStatsCard class="mb-5" icon="ri:alarm-warning-line" icon-style="bg-danger" title="超时提醒" :count="counts.overdue" description="超过 24 小时未更新" />
      </ElCol>
    </ElRow>

    <ArtSearchBar
      v-show="showSearchBar"
      v-model="searchForm"
      :items="searchItems"
      @search="handleSearch"
      @reset="handleReset"
    />

    <ElCard class="art-table-card" :style="{ marginTop: showSearchBar ? '12px' : '0' }">
      <ArtTableHeader
        v-model:columns="columns"
        v-model:showSearchBar="showSearchBar"
        :loading="loading"
        @refresh="load"
      >
        <template #left>
          <span class="text-xs text-g-500">点击工单行查看完整会话</span>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :data="list"
        :columns="columns"
        :pagination="pagination"
        :pagination-options="{ pageSizes: [10, 20, 25, 50, 100] }"
        empty-text="当前筛选下暂无工单"
        @sort-change="handleSortChange"
        @row-click="open"
        @pagination:current-change="handlePageChange"
      />
    </ElCard>

    <el-drawer v-model="drawer" :title="selected ? `工单 #${selected.id}` : '工单详情'" size="620px" class="ticket-drawer">
      <template v-if="selected">
        <div class="drawer-subject">
          <span class="eyebrow">{{ categoryText(selected.category) }}</span>
          <h2>{{ selected.subject }}</h2>
          <p>{{ selected.email }} · {{ selected.service_name || selected.service_hostname || '未关联服务' }}</p>
        </div>
        <div class="drawer-controls">
          <div>
            <small>工单状态</small>
            <el-select :model-value="selected.status" @change="changeStatus">
              <el-option label="待处理" value="pending" />
              <el-option label="处理中" value="processing" />
              <el-option label="已关闭" value="closed" />
            </el-select>
          </div>
          <div>
            <small>分配客服</small>
            <el-select :model-value="selected.assignee_id || 0" placeholder="选择客服" @change="changeAssignee">
              <el-option label="取消分配" :value="0" />
              <el-option v-for="agent in assignees" :key="agent.id" :label="agent.name" :value="agent.id" />
            </el-select>
          </div>
        </div>
        <div class="original-message">
          <span class="message-label">客户描述</span>
          <p>{{ selected.body }}</p>
        </div>
        <div v-if="attachments.length" class="drawer-attachments">
          <span class="message-label">工单附件</span>
          <a v-for="file in attachments" :key="file.id" :href="file.url" target="_blank" rel="noreferrer">{{ file.name }}</a>
        </div>
        <div class="conversation">
          <div
            v-for="message in messages"
            :key="message.id"
            class="conversation-message"
            :class="{ internal: message.internal, agent: message.admin_id }"
          >
            <div class="message-top">
              <strong>{{ message.internal ? '内部备注' : message.admin_id ? '客服回复' : '客户回复' }}</strong>
              <time>{{ message.created_at }}</time>
            </div>
            <p>{{ message.content }}</p>
          </div>
          <div v-if="!messages.length" class="drawer-empty">暂无后续回复</div>
        </div>
        <div class="composer">
          <div class="composer-label"><span>内部备注</span><small>仅客服可见</small></div>
          <el-input v-model="note" type="textarea" :rows="3" placeholder="记录处理过程、交接信息或内部判断" />
          <el-button class="composer-action" @click="saveNote">保存备注</el-button>
          <div class="composer-label reply-label"><span>回复客户</span><small>客户会收到站内通知</small></div>
          <el-input v-if="selected.status !== 'closed'" v-model="reply" type="textarea" :rows="4" placeholder="输入正式回复内容" />
          <label v-if="selected.status !== 'closed'" class="admin-upload">
            {{ replyFile ? replyFile.name : '添加回复附件' }}
            <input type="file" @change="chooseReplyAttachment" />
          </label>
        </div>
      </template>
      <template #footer>
        <el-button @click="drawer = false">关闭</el-button>
        <el-button v-if="selected && selected.status !== 'closed'" type="primary" :icon="Check" @click="send">发送回复</el-button>
      </template>
    </el-drawer>
  </div>
</template>

<style scoped>
.eyebrow { color: var(--theme-color); font-size: 10px; font-weight: 700; letter-spacing: 0.1em; text-transform: uppercase; }
.drawer-subject { padding-bottom: 18px; border-bottom: 1px solid var(--art-card-border); }
.drawer-subject h2 { margin: 6px 0; color: var(--art-gray-900); font-size: 20px; line-height: 1.35; }
.drawer-subject p { margin: 0; color: var(--art-gray-500); font-size: 12px; }
.drawer-controls { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; padding: 16px 0; }
.drawer-controls div { display: grid; gap: 6px; }
.drawer-controls small, .composer-label { color: var(--art-gray-500); font-size: 11px; }
.drawer-controls .el-select { width: 100%; }
.original-message { padding: 14px; background: var(--art-gray-50); border-radius: 8px; }
.message-label { color: var(--art-gray-400); font-size: 10px; }
.original-message p { margin: 8px 0 0; color: var(--art-gray-700); line-height: 1.75; white-space: pre-wrap; }
.drawer-attachments { display: flex; flex-wrap: wrap; gap: 7px; padding: 12px 0; }
.drawer-attachments .message-label { width: 100%; }
.drawer-attachments a, .admin-upload { padding: 5px 8px; color: var(--theme-color); font-size: 11px; background: var(--theme-color-soft); border-radius: 5px; text-decoration: none; cursor: pointer; }
.admin-upload { display: inline-flex; margin-top: 9px; }
.admin-upload input { display: none; }
.conversation { display: flex; flex-direction: column; gap: 9px; margin: 18px 0; }
.conversation-message { padding: 12px 14px; background: var(--theme-color-soft); border-radius: 8px; }
.conversation-message.agent { background: color-mix(in srgb, var(--el-color-success) 10%, transparent); }
.conversation-message.internal { background: color-mix(in srgb, var(--el-color-warning) 12%, transparent); }
.message-top { display: flex; justify-content: space-between; gap: 10px; }
.message-top strong { color: var(--art-gray-700); font-size: 11px; }
.message-top time { color: var(--art-gray-400); font-size: 10px; }
.conversation-message p { margin: 6px 0 0; color: var(--art-gray-600); line-height: 1.65; white-space: pre-wrap; }
.drawer-empty { padding: 18px; color: var(--art-gray-400); font-size: 12px; text-align: center; border: 1px dashed var(--art-card-border); border-radius: 8px; }
.composer { padding-top: 15px; border-top: 1px solid var(--art-card-border); }
.composer-label { display: flex; align-items: baseline; gap: 6px; margin-bottom: 6px; }
.composer-label span { color: var(--art-gray-700); font-size: 12px; font-weight: 600; }
.composer-action { margin: 10px 0 18px; }
.reply-label { margin-top: 4px; }

@media (max-width: 600px) {
  .drawer-controls { grid-template-columns: 1fr; }
  .drawer-subject h2 { font-size: 18px; overflow-wrap: anywhere; }
}
</style>
