<script setup lang="ts">
import { ref, computed, onMounted, watch, h } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ElMessage, ElTag, ElButton } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import { http } from '@/http/index'
import { formatDate, formatMoney } from '@/utils/format'
import { copyText } from '@/utils/clipboard'
import PublicPageHead from '@/components/public/PublicPageHead.vue'
import ArtStatsCard from '@/components/core/cards/art-stats-card/index.vue'

const router = useRouter()

interface Invoice {
  id: number
  no: string
  amount: string
  paid_amount: string
  fee_amount: string
  kind: string
  status: string
  due_at?: string
  created_at: string
  /** 关联服务（充值账单为空串，service_id 为 0） */
  service_id: number
  service_name: string
  service_host: string
  service_status: string
}

const list = ref<Invoice[]>([])
const loading = ref(false)
const loadError = ref(false)
const keyword = ref('')
const statusFilter = ref('')
const kindFilter = ref('')
const page = ref(1)
const pageSize = ref(10)
const detailVisible = ref(false)
const detail = ref<Invoice | null>(null)

const unpaidCount = computed(() => list.value.filter((i) => i.status === '未支付').length)
// 以"分"为整数单位累加，避免浮点累加的精度误差
const paidSum = computed(() => {
  const cents = list.value
    .filter((i) => i.status === '已支付')
    .reduce((acc, i) => acc + Math.round((Number(i.paid_amount || i.amount) || 0) * 100), 0)
  return cents / 100
})

const filtered = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  return list.value.filter((i) => {
    if (statusFilter.value && i.status !== statusFilter.value) return false
    if (kindFilter.value && i.kind !== kindFilter.value) return false
    if (q && !i.no.toLowerCase().includes(q)) return false
    return true
  })
})
const total = computed(() => filtered.value.length)
// 列表排序：数据在切片分页前先排序，否则只能排当前页
const sortKey = ref('')
const sortOrder = ref<1 | -1>(1)
const NUMERIC_SORT_KEYS = new Set(['amount', 'paid_amount', 'fee_amount'])
const sorted = computed(() => {
  if (!sortKey.value) return filtered.value
  const k = sortKey.value
  const arr = [...filtered.value]
  arr.sort((a, b) => {
    const va = (a as unknown as Record<string, unknown>)[k]
    const vb = (b as unknown as Record<string, unknown>)[k]
    const cmp = NUMERIC_SORT_KEYS.has(k)
      ? Number(va || 0) - Number(vb || 0)
      : String(va ?? '').localeCompare(String(vb ?? ''), 'zh-Hans-CN')
    return cmp * sortOrder.value
  })
  return arr
})
function handleSortChange({ prop, order }: { prop: string; order: 'ascending' | 'descending' | null }) {
  sortKey.value = order ? prop : ''
  sortOrder.value = order === 'ascending' ? 1 : -1
}
const paged = computed(() =>
  sorted.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
)
const pagination = computed(() => ({ current: page.value, size: pageSize.value, total: total.value }))

watch([statusFilter, kindFilter, keyword], () => {
  page.value = 1
})

const statusType: Record<string, 'warning' | 'success' | 'info' | 'danger'> = {
  未支付: 'warning',
  已支付: 'success',
  作废: 'info',
  已过期: 'danger',
}
function kindLabel(kind: string): string {
  if (kind === 'order') return '订单'
  if (kind === 'recharge') return '充值'
  return kind || '-'
}
function money(v: string): string {
  return `￥${formatMoney(v)}`
}
async function copyNo(no: string) {
  if (await copyText(no)) ElMessage.success('账单号已复制')
  else ElMessage.error('复制失败，请手动复制')
}
function isPayable(row: Invoice): boolean {
  return row.status === '未支付'
}

const quickFilters = [
  { key: '', label: '全部' },
  { key: '未支付', label: '待支付' },
  { key: '已支付', label: '已支付' },
  { key: '已过期', label: '已过期' },
  { key: '作废', label: '作废' },
]

const columns = ref<ColumnOption[]>([
  {
    prop: 'no',
    label: '账单号',
    minWidth: 160,
    sortable: 'custom',
    formatter: (row) =>
      h(
        'div',
        {
          title: '点击复制账单号',
          style: 'display:flex;flex-direction:column;gap:2px;cursor:pointer',
          onClick: () => copyNo(row.no),
        },
        [
          h('strong', { style: 'color:var(--art-gray-800);font-weight:600' }, row.no),
          h('small', { style: 'color:var(--art-gray-500);font-size:11px' }, kindLabel(row.kind)),
        ],
      ),
  },
  {
    prop: 'service_name',
    label: '关联服务',
    minWidth: 170,
    sortable: 'custom',
    formatter: (row) =>
      row.service_id
        ? h('div', {}, [
            h(
              RouterLink,
              {
                to: `/services/${row.service_id}`,
                title: '查看服务详情',
                style: 'color:var(--theme-color);font-weight:500',
              },
              () => row.service_name,
            ),
            h(
              'div',
              { style: 'color:var(--art-gray-500);font-size:11px;line-height:1.6' },
              [row.service_host, row.service_status].filter(Boolean).join(' · '),
            ),
          ])
        : h('span', { style: 'color:var(--art-gray-400)' }, '-'),
  },
  {
    prop: 'amount',
    label: '金额',
    width: 110,
    sortable: 'custom',
    formatter: (row) =>
      h(
        'span',
        { style: 'color:var(--art-gray-800);font-family:ui-monospace,Menlo,monospace;font-size:13px' },
        money(row.amount),
      ),
  },
  {
    prop: 'paid_amount',
    label: '实付',
    width: 110,
    sortable: 'custom',
    formatter: (row) =>
      h(
        'span',
        { style: 'color:var(--art-gray-800);font-family:ui-monospace,Menlo,monospace;font-size:13px' },
        row.status === '已支付' ? money(row.paid_amount || row.amount) : '-',
      ),
  },
  {
    prop: 'fee_amount',
    label: '手续费',
    width: 100,
    sortable: 'custom',
    formatter: (row) =>
      h(
        'span',
        { style: 'color:var(--art-gray-600);font-family:ui-monospace,Menlo,monospace;font-size:13px' },
        Number(row.fee_amount) > 0 ? money(row.fee_amount) : '-',
      ),
  },
  {
    prop: 'status',
    label: '状态',
    width: 96,
    sortable: 'custom',
    formatter: (row) =>
      h(
        ElTag,
        { type: statusType[row.status] || 'info', size: 'small', effect: 'light' },
        () => row.status,
      ),
  },
  {
    prop: 'due_at',
    label: '截止时间',
    width: 150,
    sortable: 'custom',
    formatter: (row) => (row.due_at ? formatDate(row.due_at) : '-'),
  },
  { prop: 'created_at', label: '创建时间', width: 150, sortable: 'custom', formatter: (row) => formatDate(row.created_at) },
  {
    prop: 'operation',
    label: '操作',
    width: 150,
    fixed: 'right',
    formatter: (row) =>
      h('div', [
        h(
          ElButton,
          { size: 'small', text: true, type: 'primary', onClick: () => openDetail(row) },
          () => '查看',
        ),
        isPayable(row)
          ? h(
              ElButton,
              { size: 'small', type: 'primary', plain: true, onClick: () => pay(row) },
              () => '去支付',
            )
          : null,
      ]),
  },
])

async function load() {
  loading.value = true
  loadError.value = false
  try {
    const res = (await http.get('/user/invoices')) as { ok: number; list: Invoice[] }
    list.value = (res.list || []) as Invoice[]
  } catch (err: unknown) {
    loadError.value = true
    ElMessage.error((err as Error).message || '读取账单失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function handleCurrentChange(p: number) {
  page.value = p
}
function pay(row: Invoice) {
  router.push(`/pay/${row.id}`)
}
function openDetail(row: Invoice) {
  detail.value = row
  detailVisible.value = true
}
</script>

<template>
  <div>
    <PublicPageHead title="财务记录" subtitle="账单与支付记录。" />

    <ElRow :gutter="20">
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard
          class="mb-4"
          icon="ri:file-list-3-line"
          icon-style="bg-primary"
          title="账单总数"
          :count="list.length"
          description="全部账单"
        />
      </ElCol>
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard
          class="mb-4"
          icon="ri:time-line"
          icon-style="bg-warning"
          title="待支付"
          :count="unpaidCount"
          description="待处理账单"
        />
      </ElCol>
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard
          class="mb-4"
          icon="ri:wallet-3-line"
          icon-style="bg-secondary"
          title="累计已付"
          :count="paidSum"
          :decimals="2"
          description="单位：元"
        />
      </ElCol>
    </ElRow>

    <div class="art-card inv-quick" role="group" aria-label="账单状态筛选">
      <button
        v-for="t in quickFilters"
        :key="t.key"
        type="button"
        class="inv-tag"
        :class="{ 'is-active': statusFilter === t.key }"
        :aria-pressed="statusFilter === t.key"
        @click="statusFilter = t.key"
      >
        {{ t.label }}
      </button>
    </div>

    <div class="art-card inv-panel">
      <div v-if="loadError" class="inv-state" role="alert">
        <p>账单列表加载失败，请稍后重试。</p>
        <el-button size="small" @click="load">重新加载</el-button>
      </div>

      <div class="inv-toolbar">
        <el-input
          v-model="keyword"
          aria-label="搜索账单号"
          placeholder="搜索账单号"
          clearable
          class="inv-search"
        >
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-select v-model="kindFilter" aria-label="账单类型" class="inv-kind">
          <el-option label="全部类型" value="" />
          <el-option label="订单" value="order" />
          <el-option label="充值" value="recharge" />
        </el-select>
      </div>

      <ArtTable
        :loading="loading"
        :data="paged"
        :columns="columns"
        :pagination="pagination"
        :show-table-header="false"
        :height="560"
        :empty-text="keyword || statusFilter || kindFilter ? '没有匹配的账单' : '暂无账单'"
        @sort-change="handleSortChange"
        @pagination:current-change="handleCurrentChange"
      />
    </div>

    <el-dialog v-model="detailVisible" title="账单详情" width="480px">
      <el-descriptions v-if="detail" :column="1" border>
        <el-descriptions-item label="账单号">{{ detail.no }}</el-descriptions-item>
        <el-descriptions-item label="类型">{{ kindLabel(detail.kind) }}</el-descriptions-item>
        <el-descriptions-item v-if="detail.service_id" label="关联服务">
          <RouterLink
            :to="`/services/${detail.service_id}`"
            :title="'查看服务详情'"
            style="color:var(--theme-color)"
          >
            {{ detail.service_name }}
          </RouterLink>
          <span v-if="detail.service_host">（{{ detail.service_host }}）</span>
          · {{ detail.service_status }}
        </el-descriptions-item>
        <el-descriptions-item label="状态">
          <el-tag :type="statusType[detail.status] || 'info'" size="small" effect="light">{{ detail.status }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="账单金额">{{ money(detail.amount) }}</el-descriptions-item>
        <el-descriptions-item label="实付金额">{{ detail.status === '已支付' ? money(detail.paid_amount || detail.amount) : '-' }}</el-descriptions-item>
        <el-descriptions-item label="手续费">{{ money(detail.fee_amount) }}</el-descriptions-item>
        <el-descriptions-item label="截止时间">{{ detail.due_at ? formatDate(detail.due_at) : '-' }}</el-descriptions-item>
        <el-descriptions-item label="创建时间">{{ formatDate(detail.created_at) }}</el-descriptions-item>
      </el-descriptions>
      <template #footer>
        <el-button @click="detailVisible = false">关闭</el-button>
        <el-button v-if="detail && isPayable(detail)" type="primary" @click="detail && pay(detail); detailVisible = false">去支付</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.inv-quick {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 14px;
  padding: 12px 14px;
}

.inv-tag {
  padding: 7px 16px;
  color: var(--art-gray-600);
  font-size: 13px;
  background: var(--default-box-color);
  border: 1px solid var(--art-card-border);
  border-radius: 999px;
  cursor: pointer;
  transition: color 0.16s ease, background 0.16s ease, border-color 0.16s ease;
}

.inv-tag:hover {
  color: var(--theme-color);
  background: var(--theme-color-soft);
  border-color: color-mix(in srgb, var(--theme-color) 40%, var(--art-card-border));
}

.inv-tag.is-active {
  color: var(--theme-color-contrast);
  background: var(--theme-color);
  border-color: var(--theme-color);
}

.inv-panel {
  padding: 16px;
}

.inv-toolbar {
  display: flex;
  gap: 10px;
  margin-bottom: 12px;
}

.inv-search {
  max-width: 300px;
}

.inv-kind {
  width: 140px;
}

.inv-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
  padding: 12px 16px;
  color: var(--el-color-danger);
  font-size: 13px;
  background: var(--el-color-danger-light-9);
  border-radius: var(--radius-md);
}

.inv-state p {
  margin: 0;
}

@media (max-width: 720px) {
  .inv-toolbar {
    flex-direction: column;
  }

  .inv-search,
  .inv-kind {
    max-width: 100%;
    width: 100%;
  }
}
</style>
