<script setup lang="ts">
import { useAdminRequest } from '../admin/useAdminTable'
import { ref, computed, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElButton } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import ArtButtonTable from '../components/core/forms/art-button-table/index.vue'
import { fetchAdminVerifications, type AdminVerificationItem } from '../admin/api'

const router = useRouter()
const list = ref<AdminVerificationItem[]>([])
const loading = ref(false)
const showSearchBar = ref(true)
const searchForm = ref<{ q: string }>({ q: '' })
const searchItems = [
  { label: '关键词', key: 'q', type: 'input', placeholder: '搜索邮箱 / 手机号 / 用户 ID', clearable: true },
]
const filtered = computed(() => {
  const q = searchForm.value.q.trim().toLowerCase()
  if (!q) return list.value
  return list.value.filter((v) =>
    `${v.email ?? ''} ${v.phone ?? ''} ${v.user_id ?? ''}`.toLowerCase().includes(q),
  )
})

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 70, sortable: true },
  { prop: 'user_id', label: '用户 ID', width: 110, sortable: true },
  { prop: 'email', label: '邮箱', minWidth: 180, sortable: true },
  {
    prop: 'phone',
    label: '手机号',
    width: 150,
    sortable: true,
    formatter: (row) => h('span', { style: 'white-space:nowrap' }, row.phone || '-'),
  },
  { prop: 'submitted_at', label: '提交时间', width: 160, sortable: true },
  {
    prop: 'operation',
    label: '操作',
    width: 100,
    fixed: 'right',
    formatter: (row) =>
      h(ArtButtonTable, {
        type: 'view',
        onClick: () => router.push(`/verifications/${row.id}`),
      }),
  },
])

const startRequest = useAdminRequest()
async function load() {
  const isCurrent = startRequest()
  if (!isCurrent) return
  loading.value = true
  try {
    const data = await fetchAdminVerifications()
    if (isCurrent()) list.value = data
  } catch (err: unknown) {
    if (isCurrent()) ElMessage.error((err as Error).message || '查询失败')
  } finally {
    if (isCurrent()) loading.value = false
  }
}
onMounted(load)
</script>

<template>
  <div class="art-full-height">
    <ArtSearchBar v-show="showSearchBar" v-model="searchForm" :items="searchItems" />

    <ElCard class="art-table-card" :style="{ marginTop: showSearchBar ? '12px' : '0' }">
      <ArtTableHeader
        v-model:columns="columns"
        v-model:showSearchBar="showSearchBar"
        :loading="loading"
        @refresh="load"
      >
        <template #left>
          <ElButton :loading="loading" @click="load">
            <el-icon><Refresh /></el-icon>刷新
          </ElButton>
          <span v-if="list.length" class="admin-pending">{{ list.length }} 条待处理</span>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :data="filtered" :columns="columns" empty-text="暂无待审核申请" />
    </ElCard>
  </div>
</template>

<style scoped>
.admin-pending {
  margin-left: 10px;
  padding: 5px 10px;
  color: var(--el-color-warning);
  font-size: 11px;
  font-weight: 600;
  background: var(--el-color-warning-light-9);
  border-radius: 7px;
}
</style>
