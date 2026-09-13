<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import type { ColumnOption } from '@/types'
import ArtStatsCard from '../components/core/cards/art-stats-card/index.vue'
import { fetchAdminRefunds } from '../admin/api'
import { useAdminFrontTable } from '../admin/useAdminTable'

const showSearchBar = ref(true)
const { list, loading, page, per, searchForm, load, handleSearch, handleReset, handleCurrentChange } =
  useAdminFrontTable(fetchAdminRefunds, { keyword: '', method: '' })

const searchItems = [
  { label: '关键词', key: 'keyword', type: 'input', placeholder: '搜索订单号 / 原因', clearable: true },
  {
    label: '方式',
    key: 'method',
    type: 'select',
    props: {
      placeholder: '全部方式',
      clearable: true,
      options: [
        { label: '退余额', value: 'balance' },
        { label: '线下/手动', value: 'gateway' },
      ],
    },
  },
]

onMounted(load)

// 汇总：笔数与金额合计（以"分"为整数单位累加，避免浮点精度误差）
const totalAmount = computed(() => {
  const cents = list.value.reduce((acc, r) => acc + Math.round((Number(r.amount) || 0) * 100), 0)
  return (cents / 100).toFixed(2)
})

const filtered = computed(() => {
  const q = searchForm.value.keyword.trim().toLowerCase()
  return list.value.filter((r) => {
    if (searchForm.value.method && r.method !== searchForm.value.method) return false
    if (!q) return true
    return [r.reason, String(r.order_id), String(r.user_id), r.status]
      .filter(Boolean)
      .some((v) => String(v).toLowerCase().includes(q))
  })
})

const pagination = computed(() => ({ current: page.value, size: per, total: filtered.value.length }))
// 列表排序：数据在切片分页前先排序，否则只能排当前页
const sortKey = ref('')
const sortOrder = ref<1 | -1>(1)
const NUMERIC_SORT_KEYS = new Set(['id', 'user_id', 'amount', 'admin_id'])
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
const paged = computed(() => sorted.value.slice((page.value - 1) * per, page.value * per))

const METHOD_LABELS: Record<string, string> = { balance: '退余额', gateway: '线下/手动' }
function methodLabel(m: string): string {
  return METHOD_LABELS[m] || m || '-'
}

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 70, sortable: 'custom' },
  { prop: 'created_at', label: '时间', width: 170, sortable: 'custom' },
  {
    prop: 'user_id',
    label: '用户 / 订单',
    minWidth: 150,
    sortable: 'custom',
    formatter: (row) =>
      h('div', [
        h('span', { style: 'display:block;color:var(--art-gray-600);font-size:12px' }, `用户 #${row.user_id}`),
        h('small', { style: 'display:block;color:var(--art-gray-400);font-size:10px' }, `订单 #${row.order_id}`),
      ]),
  },
  {
    prop: 'amount',
    label: '金额',
    width: 110,
    sortable: 'custom',
    formatter: (row) =>
      h('strong', { style: 'color:var(--el-color-danger);font-weight:650' }, `￥${row.amount}`),
  },
  { prop: 'method', label: '方式', width: 110, sortable: 'custom', formatter: (row) => methodLabel(row.method) },
  {
    prop: 'reason',
    label: '原因',
    minWidth: 180,
    sortable: 'custom',
    formatter: (row) =>
      h(
        'span',
        { style: row.reason ? '' : 'color:var(--art-gray-400)' },
        row.reason || '-',
      ),
  },
  { prop: 'admin_id', label: '管理员', width: 90, sortable: 'custom', formatter: (row) => (row.admin_id ? `#${row.admin_id}` : '-') },
])
</script>

<template>
  <div class="art-full-height">
    <ArtSearchBar
      v-show="showSearchBar"
      v-model="searchForm"
      :items="searchItems"
      @search="handleSearch"
      @reset="handleReset"
    />

    <ElRow :gutter="20" :style="{ marginTop: showSearchBar ? '12px' : '0' }">
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard class="mb-5" icon="ri:refund-2-line" icon-style="bg-primary" title="退款笔数" :count="list.length" description="全部退款记录" />
      </ElCol>
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard class="mb-5" icon="ri:money-cny-circle-line" icon-style="bg-danger" title="退款金额合计" :count="Number(totalAmount)" :decimals="2" separator="," description="单位：元" />
      </ElCol>
      <ElCol :xs="24" :sm="8">
        <ArtStatsCard class="mb-5" icon="ri:filter-3-line" icon-style="bg-warning" title="筛选结果" :count="filtered.length" description="当前筛选命中" />
      </ElCol>
    </ElRow>

    <ElCard class="art-table-card" style="margin-top: 12px">
      <ArtTableHeader
        v-model:columns="columns"
        v-model:showSearchBar="showSearchBar"
        :loading="loading"
        @refresh="load"
      />
      <ArtTable
        :loading="loading"
        :data="paged"
        :columns="columns"
        :pagination="pagination"
        @sort-change="handleSortChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>
  </div>
</template>
