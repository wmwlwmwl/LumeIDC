<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { ElTag } from 'element-plus'
import type { ColumnOption } from '@/types'
import { adminActionLabel as actionLabel, targetTypeLabel } from '../utils/admin-labels'
import { fetchAdminLogs } from '../admin/api'
import { useAdminFrontTable } from '../admin/useAdminTable'

const router = useRouter()
const showSearchBar = ref(true)
const { list, loading, page, per, searchForm, load, handleSearch, handleReset, handleCurrentChange } =
  useAdminFrontTable(fetchAdminLogs, { keyword: '' })
onMounted(load)

const searchItems = [
  { label: '关键词', key: 'keyword', type: 'input', placeholder: '搜索操作 / 对象 / IP / 详情', clearable: true },
]

// 后端一次返回最近 300 条，筛选与分页在客户端做（数据量小、避免重复请求）
const filtered = computed(() => {
  const q = searchForm.value.keyword.trim().toLowerCase()
  if (!q) return list.value
  return list.value.filter((l) =>
    [l.action, l.target_type, l.target_email, l.admin_name, l.detail, l.ip, String(l.target_id)]
      .filter(Boolean)
      .some((v) => String(v).toLowerCase().includes(q)),
  )
})

const pagination = computed(() => ({ current: page.value, size: per, total: filtered.value.length }))
// 列表排序：数据在切片分页前先排序，否则只能排当前页
const sortKey = ref('')
const sortOrder = ref<1 | -1>(1)
const NUMERIC_SORT_KEYS = new Set(['admin_id'])
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


const columns = ref<ColumnOption[]>([
  { prop: 'created_at', label: '时间', width: 170, sortable: 'custom' },
  {
    prop: 'admin_id',
    label: '管理员',
    width: 120,
    sortable: 'custom',
    formatter: (row) =>
      h(
        'span',
        { style: row.admin_id ? '' : 'color:var(--art-gray-400)' },
        row.admin_name || (row.admin_id ? `管理员 #${row.admin_id}` : '系统'),
      ),
  },
  {
    prop: 'action',
    label: '操作',
    minWidth: 150,
    sortable: 'custom',
    formatter: (row) =>
      h(ElTag, { size: 'small', type: 'info', effect: 'light' }, () => actionLabel(row.action)),
  },
  {
    prop: 'target_type',
    label: '对象',
    minWidth: 170,
    sortable: 'custom',
    formatter: (row) => {
      const label = targetTypeLabel(row.target_type)
      const tail =
        row.target_type === 'user' && row.target_email
          ? row.target_email
          : `#${row.target_id || '-'}`
      const text = `${label} ${tail}`
      if (row.target_type === 'user' && row.target_id) {
        return h(
          'a',
          {
            class: 'log-target',
            onClick: () => router.push(`/users/${row.target_id}/edit`),
          },
          text,
        )
      }
      return h('span', {}, text)
    },
  },
  {
    prop: 'detail',
    label: '详情',
    minWidth: 200,
    sortable: 'custom',
    formatter: (row) =>
      h(
        'span',
        { style: row.detail ? '' : 'color:var(--art-gray-400)' },
        row.detail || '-',
      ),
  },
  { prop: 'ip', label: 'IP', width: 140, sortable: 'custom' },
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

    <ElCard class="art-table-card" :style="{ marginTop: showSearchBar ? '12px' : '0' }">
      <ArtTableHeader
        v-model:columns="columns"
        v-model:showSearchBar="showSearchBar"
        :loading="loading"
        @refresh="load"
      >
        <template #left>
          <span class="admin-count">共 {{ filtered.length }} 条记录</span>
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :data="paged"
        :columns="columns"
        :pagination="pagination"
        :empty-text="searchForm.keyword ? '没有匹配的日志' : '暂无日志'"
        @sort-change="handleSortChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>
  </div>
</template>

<style scoped>
.admin-count {
  color: var(--art-gray-500);
  font-size: 12px;
}
.log-target {
  color: var(--theme-color);
  cursor: pointer;
}
.log-target:hover {
  text-decoration: underline;
}
</style>
