<script setup lang="ts">
import { ref, onMounted, h } from 'vue'
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

const typeMap: Record<string, { label: string; color: string }> = {
  discount: { label: '限时折扣', color: 'danger' },
  flash_sale: { label: '限量抢购', color: 'warning' },
  new_user: { label: '新客专享', color: 'success' },
  full_reduction: { label: '满减', color: '' },
  coupon_giveaway: { label: '优惠券发放', color: 'info' },
  bogo: { label: '买N送M', color: '' },
  group_buy: { label: '拼团', color: '' },
}

const statusMap: Record<string, { label: string; color: string }> = {
  upcoming: { label: '未开始', color: 'info' },
  ongoing: { label: '进行中', color: 'success' },
  ended: { label: '已结束', color: 'info' },
}

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 64, sortable: true },
  { prop: 'name', label: '活动名称', minWidth: 160, sortable: true },
  {
    prop: 'type', label: '类型', width: 110, sortable: true,
    formatter: (row) => {
      const t = typeMap[row.type] || { label: row.type, color: '' }
      return h(ElTag, { type: t.color as any, size: 'small' }, () => t.label)
    },
  },
  {
    prop: 'status', label: '状态', width: 90,
    formatter: (row) => {
      const s = statusMap[row.status] || { label: row.status, color: 'info' }
      return h(ElTag, { type: s.color as any, size: 'small' }, () => s.label)
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
  <div class="art-page">
    <div class="art-page-header">
      <h2 class="art-page-title">营销活动</h2>
      <el-button type="primary" :icon="Plus" @click="router.push('/promotions/new')">新增活动</el-button>
    </div>
    <art-table :columns="columns" :data="list" :loading="loading" row-key="id" />
  </div>
</template>
