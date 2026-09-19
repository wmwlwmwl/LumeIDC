<script setup lang="ts">
import { useAdminRequest } from '../admin/useAdminTable'
import { ref, computed, onMounted, h } from 'vue'
import { ElMessage, ElTag, ElButton } from 'element-plus'
import { Plus } from '@element-plus/icons-vue'
import type { ColumnOption } from '@/types'
import { fetchAdminCoupons, createAdminCoupon, type AdminCoupon } from '../admin/api'
import { formatMoney } from '@/utils/format'

const list = ref<AdminCoupon[]>([])
const loading = ref(false)
const saving = ref(false)
const dialog = ref(false)
const showSearchBar = ref(true)
const searchForm = ref<{ q: string; status: string }>({ q: '', status: '' })
const form = ref({ code: '', type: 'fixed', value: 10, min_amount: 0, usage_limit: 0, expires_at: '' })

const searchItems = [
  { label: '关键词', key: 'q', type: 'input', placeholder: '搜索优惠码', clearable: true },
  {
    label: '状态',
    key: 'status',
    type: 'select',
    props: {
      placeholder: '全部状态',
      clearable: true,
      options: [
        { label: '有效', value: 'active' },
        { label: '已停用', value: 'disabled' },
        { label: '已过期', value: 'expired' },
        { label: '已用尽', value: 'used' },
      ],
    },
  },
]

const filtered = computed(() => {
  const q = searchForm.value.q.trim().toLowerCase()
  const st = searchForm.value.status
  return list.value.filter((c) => {
    if (q && !c.code.toLowerCase().includes(q)) return false
    if (!st) return true
    const usedUp = c.usage_limit > 0 && c.used >= c.usage_limit
    if (st === 'disabled') return !c.active
    if (st === 'expired') return isExpired(c)
    if (st === 'used') return usedUp
    if (st === 'active') return c.active && !isExpired(c) && !usedUp
    return true
  })
})

// 状态按业务顺序排：有效 → 已停用 → 已过期 → 已用尽（与筛选下拉一致）
function statusRank(c: AdminCoupon): number {
  if (!c.active) return 1
  if (isExpired(c)) return 2
  if (c.usage_limit > 0 && c.used >= c.usage_limit) return 3
  return 0
}

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 64, sortable: true },
  {
    prop: 'code',
    label: '优惠码',
    minWidth: 140,
    sortable: true,
    formatter: (row) =>
      h(
        'span',
        {
          style:
            'padding:2px 7px;color:var(--theme-color-deep);font-family:ui-monospace,Menlo,monospace;font-size:11px;background:var(--theme-color-soft);border-radius:5px',
        },
        row.code,
      ),
  },
  { prop: 'type', label: '规则', width: 140, sortable: true, formatter: (row) => typeLabel(row) },
  {
    prop: 'min_amount',
    label: '最低消费',
    width: 110,
    sortable: true,
    formatter: (row) => (Number(row.min_amount) > 0 ? `￥${formatMoney(row.min_amount)}` : '不限'),
  },
  { prop: 'used', label: '使用情况', width: 180, sortable: true, formatter: (row) => usageCell(row) },
  { prop: 'expires_at', label: '有效期', width: 140, sortable: true, formatter: (row) => expiresCell(row) },
  {
    prop: 'active',
    label: '状态',
    width: 90,
    sortable: true,
    sortMethod: (a: AdminCoupon, b: AdminCoupon) => statusRank(a) - statusRank(b),
    formatter: (row) => statusCell(row),
  },
])

function usageCell(row: AdminCoupon) {
  if (row.usage_limit > 0) {
    const pct = Math.min(100, (row.used / row.usage_limit) * 100)
    return h('div', [
      h(
        'div',
        { style: 'height:5px;overflow:hidden;background:var(--art-gray-100);border-radius:5px;margin-bottom:4px' },
        [
          h('div', {
            style: `height:100%;border-radius:5px;width:${pct}%;background:${
              row.used >= row.usage_limit ? 'var(--art-gray-400)' : 'var(--theme-color)'
            }`,
          }),
        ],
      ),
      h('small', { style: 'color:var(--art-gray-400);font-size:10px' }, `${row.used} / ${row.usage_limit}`),
    ])
  }
  return h(
    'small',
    { style: 'color:var(--art-gray-400);font-size:10px' },
    `已用 ${row.used} · 不限次`,
  )
}

function expiresCell(row: AdminCoupon) {
  const expired = isExpired(row)
  return h('div', [
    h(
      'span',
      { style: `font-size:11px;color:${expired ? 'var(--el-color-danger)' : 'var(--art-gray-600)'}` },
      row.expires_at || '不限',
    ),
    expired
      ? h('small', { style: 'display:block;color:var(--el-color-danger);font-size:11px' }, '已过期')
      : null,
  ])
}

function statusCell(row: AdminCoupon) {
  if (!row.active) return h(ElTag, { type: 'info', size: 'small', effect: 'light' }, () => '已停用')
  if (isExpired(row)) return h(ElTag, { type: 'danger', size: 'small', effect: 'light' }, () => '已过期')
  if (row.usage_limit > 0 && row.used >= row.usage_limit)
    return h(ElTag, { type: 'warning', size: 'small', effect: 'light' }, () => '已用尽')
  return h(ElTag, { type: 'success', size: 'small', effect: 'light' }, () => '有效')
}

const startRequest = useAdminRequest()
async function load() {
  const isCurrent = startRequest()
  if (!isCurrent) return
  loading.value = true
  try {
    const data = await fetchAdminCoupons()
    if (isCurrent()) list.value = data
  } catch (err: unknown) {
    if (isCurrent()) ElMessage.error((err as Error).message || '查询失败')
  } finally {
    if (isCurrent()) loading.value = false
  }
}
onMounted(load)

function openNew() {
  form.value = { code: '', type: 'fixed', value: 10, min_amount: 0, usage_limit: 0, expires_at: '' }
  dialog.value = true
}

async function save() {
  if (!form.value.code.trim()) {
    ElMessage.warning('请输入优惠码')
    return
  }
  saving.value = true
  try {
    await createAdminCoupon({ ...form.value, code: form.value.code.trim() })
    ElMessage.success('已创建')
    dialog.value = false
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '创建失败')
  } finally {
    saving.value = false
  }
}

function typeLabel(c: AdminCoupon): string {
  return c.type === 'fixed' ? `满减 ￥${formatMoney(c.value)}` : `折扣 ${c.value}%`
}

// 过期判断：expires_at 为 'YYYY-MM-DD'，当天仍有效
function isExpired(c: AdminCoupon): boolean {
  if (!c.expires_at) return false
  const today = new Date()
  const ymd = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`
  return c.expires_at < ymd
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
            <el-icon><Plus /></el-icon>新增优惠券
          </ElButton>
        </template>
      </ArtTableHeader>

      <ArtTable :loading="loading" :data="filtered" :columns="columns" />
    </ElCard>

    <el-dialog v-model="dialog" title="新增优惠券" width="480px">
      <el-form label-position="top">
        <el-form-item label="优惠码（唯一）" required><el-input v-model="form.code" placeholder="如 NEW10" /></el-form-item>
        <div class="admin-form-grid">
          <el-form-item label="折扣类型"><el-select v-model="form.type" class="w-full"><el-option label="满减（元）" value="fixed" /><el-option label="折扣（%）" value="percent" /></el-select></el-form-item>
          <el-form-item label="折扣值"><el-input-number v-model="form.value" :min="0" :precision="2" class="w-full" /></el-form-item>
        </div>
        <div class="admin-form-grid">
          <el-form-item label="最低消费（元）"><el-input-number v-model="form.min_amount" :min="0" :precision="2" class="w-full" /></el-form-item>
          <el-form-item label="使用次数（0 不限）"><el-input-number v-model="form.usage_limit" :min="0" class="w-full" /></el-form-item>
        </div>
        <el-form-item label="过期日期"><el-date-picker v-model="form.expires_at" type="date" value-format="YYYY-MM-DD" placeholder="留空不限" class="w-full" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="dialog = false">取消</el-button><el-button type="primary" :loading="saving" @click="save">创建</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.admin-form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
@media (max-width: 640px) {
  .admin-form-grid {
    grid-template-columns: 1fr;
  }
}
</style>
