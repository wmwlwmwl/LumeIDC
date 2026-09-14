<script setup lang="ts">
import { useAdminRequest } from '../admin/useAdminTable'
import { ref, computed, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, ElTag, ElButton } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import ArtButtonTable from '../components/core/forms/art-button-table/index.vue'
import { fetchAdminProducts, deleteAdminProduct, type AdminProduct } from '../admin/api'

const router = useRouter()
const list = ref<AdminProduct[]>([])
const loading = ref(false)
const showSearchBar = ref(true)
const searchForm = ref<{ keyword: string; visibility: string }>({ keyword: '', visibility: '' })

const searchItems = [
  { label: '关键词', key: 'keyword', type: 'input', placeholder: '搜索产品 / 分类 / 上游', clearable: true },
  {
    label: '状态',
    key: 'visibility',
    type: 'select',
    props: {
      placeholder: '全部状态',
      clearable: true,
      options: [
        { label: '显示中', value: 'visible' },
        { label: '已隐藏', value: 'hidden' },
      ],
    },
  },
]

const filtered = computed(() => {
  const q = searchForm.value.keyword.trim().toLowerCase()
  return list.value.filter((p) => {
    if (searchForm.value.visibility === 'visible' && p.hidden) return false
    if (searchForm.value.visibility === 'hidden' && !p.hidden) return false
    if (!q) return true
    // 与旧 SSR 页一致：产品名、分类、上游服务器名/PID 均可命中
    return [p.name, p.type, p.server, p.upstream_pid ? String(p.upstream_pid) : '']
      .some((v) => (v || '').toLowerCase().includes(q))
  })
})

// 月价/首期为字符串（可能为 '-'），字典序会 9>10，需按数值比较
const byNumber =
  (key: 'monthly' | 'first') =>
  (a: AdminProduct, b: AdminProduct): number =>
    Number(a[key] || 0) - Number(b[key] || 0)

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 64, sortable: true },
  { prop: 'name', label: '名称', minWidth: 160, sortable: true },
  { prop: 'type', label: '分类', width: 160, sortable: true },
  {
    prop: 'monthly',
    label: '月价',
    width: 100,
    sortable: true,
    sortMethod: byNumber('monthly'),
    formatter: (row) =>
      h(
        'b',
        { style: 'color: var(--art-gray-800); font-weight: 650' },
        `￥${row.monthly === '-' ? '-' : row.monthly}`,
      ),
  },
  {
    prop: 'first',
    label: '首期',
    width: 100,
    sortable: true,
    sortMethod: byNumber('first'),
    formatter: (row) =>
      h(
        'span',
        { style: 'color: var(--art-gray-600)' },
        `￥${row.first === '-' ? '-' : row.first}`,
      ),
  },
  {
    prop: 'server',
    label: '上游',
    minWidth: 190,
    sortable: true,
    formatter: (row) => {
      if (!row.server) return h('span', { style: 'color:var(--art-gray-400)' }, '本地产品')
      const parts = [
        h(
          'span',
          {
            style:
              'padding:2px 7px;color:var(--art-gray-700);font-family:ui-monospace,Menlo,monospace;font-size:11px;background:var(--art-gray-100);border-radius:5px',
          },
          `${row.server} / PID ${row.upstream_pid}`,
        ),
      ]
      if (row.upstream_offline_reason === 'unshelved')
        parts.push(h(ElTag, { type: 'danger', size: 'small', effect: 'light' }, () => '已下架'))
      return h('span', { style: 'display:inline-flex;align-items:center;gap:6px' }, parts)
    },
  },
  {
    prop: 'requires_identity',
    label: '实名',
    width: 70,
    sortable: true,
    formatter: (row) => (row.requires_identity ? '是' : '否'),
  },
  {
    prop: 'hidden',
    label: '状态',
    width: 80,
    sortable: true,
    formatter: (row) =>
      h(ElTag, { type: row.hidden ? 'info' : 'success', size: 'small', effect: 'light' }, () =>
        row.hidden ? '隐藏' : '显示',
      ),
  },
  {
    prop: 'operation',
    label: '操作',
    width: 130,
    fixed: 'right',
    formatter: (row) =>
      h('div', [
        h(ArtButtonTable, { type: 'edit', onClick: () => openEdit(row) }),
        h(ArtButtonTable, { type: 'delete', onClick: () => del(row) }),
      ]),
  },
])

const startRequest = useAdminRequest()
async function load() {
  const isCurrent = startRequest()
  if (!isCurrent) return
  loading.value = true
  try {
    const data = await fetchAdminProducts()
    if (isCurrent()) list.value = data
  } catch (err: unknown) {
    if (isCurrent()) ElMessage.error((err as Error).message || '查询失败')
  } finally {
    if (isCurrent()) loading.value = false
  }
}
onMounted(load)

function handleReset() {
  searchForm.value = { keyword: '', visibility: '' }
}

function openEdit(row: AdminProduct) {
  router.push(`/products/${row.id}/edit`)
}
function openNew() {
  router.push('/products/new')
}

async function del(row: AdminProduct) {
  const ok = await ElMessageBox.confirm(
    `确认删除产品「${row.name}」？已售出的服务不会随之删除，但产品配置将不可恢复。`,
    '删除产品',
    { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
  ).catch(() => null)
  if (!ok) return
  try {
    await deleteAdminProduct(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}
</script>

<template>
  <div class="art-full-height">
    <ArtSearchBar
      v-show="showSearchBar"
      v-model="searchForm"
      :items="searchItems"
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
          <ElButton type="primary" @click="openNew">
            <el-icon><Plus /></el-icon>新增产品
          </ElButton>
          <span class="admin-count">{{ filtered.length }} / {{ list.length }} 个产品</span>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :data="filtered" :columns="columns" />
    </ElCard>
  </div>
</template>

<style scoped>
.admin-count {
  margin-left: 10px;
  color: var(--art-gray-500);
  font-size: 12px;
}
</style>
