<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElTag, ElSwitch, ElButton } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import { http } from '@/http'

interface AdminPromotion {
  id: number
  name: string
  type: string
  starts_at: string
  ends_at: string
  enabled: boolean
  limit_per_user: number
  status: string
}

const router = useRouter()
const list = ref<AdminPromotion[]>([])
const loading = ref(false)
const showSearchBar = ref(true)
const searchForm = ref<{ q: string; status: string; type: string }>({ q: '', status: '', type: '' })

const typeMap: Record<string, { label: string; color: string }> = {
  discount: { label: '限时折扣', color: 'danger' },
  flash_sale: { label: '限量抢购', color: 'warning' },
  new_user: { label: '新客专享', color: 'success' },
  full_reduction: { label: '满减', color: '' },
  coupon_giveaway: { label: '优惠券发放', color: 'info' },
}

const statusMap: Record<string, { label: string; color: string }> = {
  upcoming: { label: '未开始', color: 'info' },
  ongoing: { label: '进行中', color: 'success' },
  ended: { label: '已结束', color: 'info' },
}

const searchItems = [
  { label: '活动名称', key: 'q', type: 'input', placeholder: '搜索活动名称', clearable: true },
  {
    label: '类型',
    key: 'type',
    type: 'select',
    props: {
      placeholder: '全部类型',
      clearable: true,
      options: [
        ...Object.entries(typeMap).map(([value, v]) => ({ value, label: v.label })),
      ],
    },
  },
  {
    label: '状态',
    key: 'status',
    type: 'select',
    props: {
      placeholder: '全部状态',
      clearable: true,
      options: [
        { label: '进行中', value: 'ongoing' },
        { label: '未开始', value: 'upcoming' },
        { label: '已结束', value: 'ended' },
      ],
    },
  },
]

const filtered = computed(() => {
  const q = searchForm.value.q.trim().toLowerCase()
  const st = searchForm.value.status
  const tp = searchForm.value.type
  return list.value.filter((p) => {
    if (q && !p.name.toLowerCase().includes(q)) return false
    if (st && p.status !== st) return false
    if (tp && p.type !== tp) return false
    return true
  })
})

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 64, sortable: true },
  { prop: 'name', label: '活动名称', minWidth: 160, sortable: true },
  {
    prop: 'type', label: '类型', width: 110, sortable: true,
    formatter: (row) => {
      const t = typeMap[row.type] || { label: row.type, color: '' }
      return h(ElTag, { type: t.color as any, size: 'small', effect: 'light' }, () => t.label)
    },
  },
  {
    prop: 'status', label: '状态', width: 90,
    formatter: (row) => {
      const s = statusMap[row.status] || { label: row.status, color: 'info' }
      return h(ElTag, { type: s.color as any, size: 'small', effect: 'light' }, () => s.label)
    },
  },
  { prop: 'starts_at', label: '开始时间', width: 150, sortable: true },
  { prop: 'ends_at', label: '结束时间', width: 150, sortable: true },
  { prop: 'limit_per_user', label: '限购', width: 80, formatter: (row) => row.limit_per_user > 0 ? `${row.limit_per_user}台/人` : '不限' },
  {
    prop: 'enabled', label: '启用', width: 80,
    formatter: (row) => h(ElSwitch, {
      modelValue: row.enabled,
      size: 'small',
      onChange: (v: boolean | string | number) => toggle(row, v),
    }),
  },
  {
    prop: 'actions', label: '操作', width: 180, fixed: 'right',
    formatter: (row) => h('div', { class: 'flex gap-2' }, [
      h(ElButton, { size: 'small', link: true, type: 'primary', onClick: () => router.push(`/promotions/${row.id}/edit`) }, () => '编辑'),
      h(ElButton, { size: 'small', link: true, type: 'primary', onClick: () => router.push(`/promotion/${row.id}`) }, () => '预览'),
      h(ElButton, { size: 'small', link: true, type: 'danger', onClick: () => remove(row) }, () => '删除'),
    ]),
  },
])

async function load() {
  loading.value = true
  try {
    const res = await http.get<{ ok: number; list?: AdminPromotion[] }>('/promotions')
    list.value = (res.list || []) as AdminPromotion[]
  } finally {
    loading.value = false
  }
}

async function toggle(row: AdminPromotion, v: boolean | string | number) {
  const enabled = !!v
  await http.post(`/promotions/${row.id}/toggle`, { enabled: enabled ? '1' : '0' })
  row.enabled = enabled
  ElMessage.success(enabled ? '已启用' : '已停用')
}

async function remove(row: AdminPromotion) {
  if (!confirm(`确认删除活动「${row.name}」？`)) return
  await http.post(`/promotions/${row.id}/delete`)
  ElMessage.success('已删除')
  load()
}

onMounted(load)
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
          <ElButton type="primary" :icon="Plus" @click="router.push('/promotions/new')">新增活动</ElButton>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :data="filtered" :columns="columns" row-key="id" />
    </ElCard>
  </div>
</template>
