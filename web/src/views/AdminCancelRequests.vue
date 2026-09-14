<script setup lang="ts">
import { useAdminRequest } from '../admin/useAdminTable'
import { ref, computed, onMounted, h } from 'vue'
import { ElMessage, ElMessageBox, ElTag } from 'element-plus'
import type { ColumnOption } from '@/types'
import ArtStatsCard from '../components/core/cards/art-stats-card/index.vue'
import ArtButtonTable from '../components/core/forms/art-button-table/index.vue'
import {
  fetchAdminCancelRequests,
  handleAdminCancelRequest,
  type AdminCancelRequest,
} from '../admin/api'

const list = ref<AdminCancelRequest[]>([])
const loading = ref(false)
const pending = ref(0)
const status = ref('pending')
const busyId = ref<number | null>(null)

const TYPE_LABELS: Record<string, string> = { Immediate: '立即停用', Endofbilling: '到期停用' }
const STATUS_LABELS: Record<string, string> = {
  pending: '待处理',
  approved: '已通过',
  rejected: '已驳回',
  withdrawn: '已撤回',
}
const STATUS_TYPES: Record<string, 'warning' | 'success' | 'danger' | 'info'> = {
  pending: 'warning',
  approved: 'success',
  rejected: 'danger',
  withdrawn: 'info',
}
const MODE_LABELS: Record<string, string> = { local: '本地删除', upstream: '连上游删除' }

const statusOptions = [
  { label: '待处理', value: 'pending' },
  { label: '已通过', value: 'approved' },
  { label: '已驳回', value: 'rejected' },
  { label: '已撤回', value: 'withdrawn' },
  { label: '全部', value: '' },
]

const startRequest = useAdminRequest()
async function load() {
  const isCurrent = startRequest()
  if (!isCurrent) return
  loading.value = true
  try {
    const res = await fetchAdminCancelRequests(status.value)
    if (!isCurrent()) return
    list.value = res.list
    pending.value = res.pending
  } catch (err: unknown) {
    if (isCurrent()) ElMessage.error((err as Error).message || '查询失败')
  } finally {
    if (isCurrent()) loading.value = false
  }
}
onMounted(load)

async function run(row: AdminCancelRequest, action: 'local' | 'upstream' | 'reject', note = '') {
  busyId.value = row.id
  try {
    const res = await handleAdminCancelRequest(row.id, action, note)
    if (String(res.ok) === '1') ElMessage.success(res.msg || '操作成功')
    else ElMessage.error(res.msg || '操作失败')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  } finally {
    busyId.value = null
  }
}

async function doDelete(row: AdminCancelRequest, upstream: boolean) {
  const text = upstream
    ? `确认【连上游删除】服务 #${row.service_id}（${row.service_name}）？将同步销毁上游实例，不可恢复。`
    : `确认【本地删除】服务 #${row.service_id}（${row.service_name}）？仅本站标记删除，上游实例保留。`
  const ok = await ElMessageBox.confirm(text, upstream ? '连上游删除' : '本地删除', {
    type: 'warning',
    confirmButtonText: '执行',
    cancelButtonText: '取消',
  }).catch(() => null)
  if (!ok) return
  await run(row, upstream ? 'upstream' : 'local')
}

async function doReject(row: AdminCancelRequest) {
  const res = await ElMessageBox.prompt('可填写驳回原因，将随站内信发送给用户。', '驳回停用申请', {
    inputPlaceholder: '例如：服务仍在有效期内，建议继续使用',
    confirmButtonText: '确认驳回',
    cancelButtonText: '取消',
    type: 'info',
  }).catch(() => null)
  if (!res) return
  await run(row, 'reject', res.value || '')
}

// 状态按业务顺序排：待处理 → 已通过 → 已驳回 → 已撤回
const STATUS_RANK: Record<string, number> = { pending: 0, approved: 1, rejected: 2, withdrawn: 3 }

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 70, sortable: true },
  {
    prop: 'username',
    label: '用户',
    minWidth: 150,
    sortable: true,
    formatter: (row) =>
      h('div', [
        h('span', { style: 'display:block;color:var(--art-gray-700)' }, row.username || `#${row.user_id}`),
        h('small', { style: 'display:block;color:var(--art-gray-400);font-size:10px' }, row.email || ''),
      ]),
  },
  {
    prop: 'service_name',
    label: '服务',
    minWidth: 160,
    sortable: true,
    formatter: (row) =>
      h('div', [
        h('span', { style: 'display:block;color:var(--art-gray-700)' }, row.service_name || `#${row.service_id}`),
        h('small', { style: 'display:block;color:var(--art-gray-400);font-size:10px' }, row.hostname || ''),
      ]),
  },
  { prop: 'product_name', label: '产品', minWidth: 130, sortable: true, formatter: (row) => row.product_name || '-' },
  {
    prop: 'type',
    label: '类型',
    width: 100,
    sortable: true,
    formatter: (row) => TYPE_LABELS[row.type] || row.type,
  },
  {
    prop: 'reason',
    label: '原因',
    minWidth: 180,
    sortable: true,
    formatter: (row) =>
      h('div', [
        h('span', {}, row.reason || '-'),
        row.reason_detail
          ? h('small', { style: 'display:block;color:var(--art-gray-400);font-size:10px' }, row.reason_detail)
          : null,
      ]),
  },
  { prop: 'expires_at', label: '到期', width: 110, sortable: true, formatter: (row) => row.expires_at || '-' },
  {
    prop: 'status',
    label: '状态',
    width: 90,
    sortable: true,
    sortMethod: (a: AdminCancelRequest, b: AdminCancelRequest) =>
      (STATUS_RANK[a.status] ?? 9) - (STATUS_RANK[b.status] ?? 9),
    formatter: (row) =>
      h(ElTag, { type: STATUS_TYPES[row.status] || 'info', size: 'small' }, () => STATUS_LABELS[row.status] || row.status),
  },
  { prop: 'created_at', label: '申请时间', width: 160, sortable: true },
  {
    prop: 'operation',
    label: '操作',
    width: 168,
    fixed: 'right',
    formatter: (row) => {
      if (row.status !== 'pending') {
        const label = row.handle_mode ? MODE_LABELS[row.handle_mode] || row.handle_mode : ''
        return h(
          'span',
          { style: 'color:var(--art-gray-500);font-size:12px' },
          [label, row.handle_note].filter(Boolean).join(' · ') || '-',
        )
      }
      const disabled = busyId.value === row.id
      const btn = (icon: string, cls: string, title: string, onClick: () => void) =>
        h('span', { title, style: 'display:inline-flex' }, [
          h(ArtButtonTable, { icon, iconClass: cls, onClick: () => !disabled && onClick() }),
        ])
      return h(
        'div',
        { style: `display:flex;align-items:center${disabled ? ';opacity:.45;pointer-events:none' : ''}` },
        [
          btn('ri:database-2-line', 'bg-warning/12 text-warning', '本地删除（仅本站，不动上游）', () => doDelete(row, false)),
          btn('ri:cloud-off-line', 'bg-danger/12 text-danger', '连上游删除（同步销毁上游）', () => doDelete(row, true)),
          btn('ri:close-circle-line', 'bg-info/12 text-info', '驳回申请', () => doReject(row)),
        ],
      )
    },
  },
])

const handledCount = computed(() => list.value.filter((r) => r.status !== 'pending').length)
</script>

<template>
  <div class="art-full-height">
    <ElRow :gutter="20">
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard class="mb-5" icon="ri:time-line" icon-style="bg-warning" title="待处理申请" :count="pending" description="等待管理员处理" />
      </ElCol>
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard class="mb-5" icon="ri:list-check-2" icon-style="bg-primary" title="当前列表" :count="list.length" description="按筛选条件" />
      </ElCol>
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard class="mb-5" icon="ri:check-double-line" icon-style="bg-success" title="已处理" :count="handledCount" description="当前列表内" />
      </ElCol>
    </ElRow>

    <ElCard class="art-table-card" style="margin-top: 12px">
      <ArtTableHeader v-model:columns="columns" :loading="loading" @refresh="load">
        <template #left>
          <div style="display: flex; align-items: center; gap: 10px">
            <span class="admin-count">停用申请</span>
            <ElSelect v-model="status" size="small" style="width: 120px" @change="load">
              <ElOption v-for="o in statusOptions" :key="o.value" :label="o.label" :value="o.value" />
            </ElSelect>
          </div>
        </template>
      </ArtTableHeader>
      <ArtTable :loading="loading" :data="list" :columns="columns" />
    </ElCard>
  </div>
</template>

<style scoped>
.admin-count {
  color: var(--art-gray-600);
  font-size: 13px;
}
</style>
