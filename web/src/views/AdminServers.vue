<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, ElTag, ElButton } from 'element-plus'
import { Plus, Connection } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import ArtButtonTable from '../components/core/forms/art-button-table/index.vue'
import { fetchAdminServers, deleteAdminServer, testAdminServer, type AdminServer } from '../admin/api'

const router = useRouter()
const list = ref<AdminServer[]>([])
const loading = ref(false)
const testing = ref(0)
const showSearchBar = ref(true)
const searchForm = ref<{ q: string; provider: string; status: string }>({
  q: '',
  provider: '',
  status: '',
})

const searchItems = [
  { label: '关键词', key: 'q', type: 'input', placeholder: '搜索名称 / API 地址', clearable: true },
  { label: '类型', key: 'provider', type: 'input', placeholder: '如 zjmf / easypanel', clearable: true },
  {
    label: '状态',
    key: 'status',
    type: 'select',
    props: {
      placeholder: '全部状态',
      clearable: true,
      options: [
        { label: '正常', value: 'active' },
        { label: '禁用', value: 'disabled' },
      ],
    },
  },
]

const filtered = computed(() => {
  const q = searchForm.value.q.trim().toLowerCase()
  const { provider, status } = searchForm.value
  return list.value.filter((s) => {
    if (q && !`${s.name} ${s.api_url}`.toLowerCase().includes(q)) return false
    if (provider && !String(s.provider || '').toLowerCase().includes(provider.toLowerCase()))
      return false
    if (status && (status === 'disabled') !== !!s.disabled) return false
    return true
  })
})

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 64, sortable: true },
  {
    prop: 'name',
    label: '名称',
    minWidth: 170,
    sortable: true,
    formatter: (row) =>
      h('div', { style: 'display:flex;align-items:center;gap:9px' }, [
        h(
          'span',
          {
            style:
              'width:28px;height:28px;display:inline-flex;align-items:center;justify-content:center;flex-shrink:0;color:var(--theme-color-deep);background:var(--theme-color-soft);border-radius:7px',
          },
          [h(Connection)],
        ),
        h('b', { style: 'color:var(--art-gray-800);font-weight:600' }, row.name),
      ]),
  },
  { prop: 'provider', label: '类型', width: 120, sortable: true },
  {
    prop: 'api_url',
    label: 'API 地址',
    minWidth: 220,
    sortable: true,
    formatter: (row) =>
      h(
        'span',
        { style: 'color:var(--art-gray-600);font-family:ui-monospace,Menlo,monospace;font-size:11px' },
        row.api_url,
      ),
  },
  {
    prop: 'status',
    label: '状态',
    width: 80,
    sortable: true,
    // 状态按业务顺序排：正常在前，禁用在后
    sortMethod: (a: AdminServer, b: AdminServer) => (a.disabled ? 1 : 0) - (b.disabled ? 1 : 0),
    formatter: (row) =>
      h(
        ElTag,
        { type: row.disabled ? 'info' : 'success', size: 'small', effect: 'light' },
        () => row.status,
      ),
  },
  {
    prop: 'operation',
    label: '操作',
    width: 250,
    fixed: 'right',
    formatter: (row) =>
      h('div', [
        h(ArtButtonTable, {
          icon: 'ri:signal-tower-line',
          iconClass: 'bg-info/12 text-info',
          onClick: () => test(row),
        }),
        h(ArtButtonTable, { type: 'edit', onClick: () => openEdit(row) }),
        h(ArtButtonTable, {
          icon: 'ri:download-2-line',
          iconClass: 'bg-theme/12 text-theme',
          onClick: () => openCatalog(row),
        }),
        h(ArtButtonTable, { type: 'delete', onClick: () => del(row) }),
      ]),
  },
])

async function load() {
  loading.value = true
  try {
    list.value = await fetchAdminServers()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '查询失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openNew() {
  router.push('/servers/new')
}
function openEdit(row: AdminServer) {
  router.push(`/servers/${row.id}/edit`)
}
function openCatalog(row: AdminServer) {
  router.push(`/servers/${row.id}/catalog`)
}

async function del(row: AdminServer) {
  const ok = await ElMessageBox.confirm(
    `确认删除上游服务器「${row.name}」？其下产品将失去上游绑定，操作不可恢复。`,
    '删除服务器',
    { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
  ).catch(() => null)
  if (!ok) return
  try {
    await deleteAdminServer(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}

// 测试连通性：成功时可带账户余额（后端 msg）
async function test(row: AdminServer) {
  testing.value = row.id
  try {
    const res = await testAdminServer(row.id)
    if (Number(res.ok) === 1) ElMessage.success(res.msg || '连接成功')
    else ElMessage.error(res.msg || '连接失败')
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '连接失败')
  } finally {
    testing.value = 0
  }
}
</script>

<template>
  <div class="art-full-height">
    <ArtSearchBar
      v-show="showSearchBar"
      v-model="searchForm"
      :items="searchItems"
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
            <el-icon><Plus /></el-icon>新增服务器
          </ElButton>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :data="filtered" :columns="columns" />
    </ElCard>
  </div>
</template>
