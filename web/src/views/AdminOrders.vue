<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { ElMessage, ElTag } from 'element-plus'
import type { ColumnOption } from '@/types'
import ArtButtonTable from '../components/core/forms/art-button-table/index.vue'
import { fetchOrders, refundOrder, type OrderItem } from '../admin/api'

const list = ref<OrderItem[]>([])
const page = ref(1)
const total = ref(0)
const profit = ref('0.00')
const loading = ref(false)
const showSearchBar = ref(true)
const per = 25
const searchForm = ref<{ q: string }>({ q: '' })

// 服务端分页：排序交给后端 ORDER BY（字段经白名单映射）
const sortKey = ref('')
const sortOrder = ref<'asc' | 'desc'>('desc')
function handleSortChange({ prop, order }: { prop: string; order: 'ascending' | 'descending' | null }) {
  sortKey.value = order ? prop : ''
  sortOrder.value = order === 'ascending' ? 'asc' : 'desc'
  page.value = 1
  load()
}

const searchItems = [
  { label: '关键词', key: 'q', type: 'input', placeholder: '搜索邮箱 / 订单号', clearable: true },
]

function statusType(status: string): 'success' | 'warning' | 'info' {
  if (status === '已支付') return 'success'
  if (status === '未支付') return 'warning'
  return 'info'
}

const columns = ref<ColumnOption[]>([
  { prop: 'id', label: 'ID', width: 64, sortable: 'custom' },
  { prop: 'email', label: '邮箱', minWidth: 180, sortable: 'custom' },
  {
    prop: 'amount',
    label: '金额',
    width: 110,
    sortable: 'custom',
    formatter: (row) =>
      h('b', { style: 'color: var(--art-gray-800); font-weight: 650' }, `￥${row.amount}`),
  },
  { prop: 'cycle', label: '周期', width: 90 },
  {
    prop: 'service_name',
    label: '关联服务',
    minWidth: 190,
    formatter: (row) =>
      row.service_name
        ? h('div', {}, [
            h('div', {}, row.service_name),
            h(
              'div',
              { style: 'color: var(--art-gray-500); font-size: 11px; line-height: 1.6' },
              [row.service_host, row.service_status].filter(Boolean).join(' · '),
            ),
          ])
        : h('span', { style: 'color: var(--art-gray-400)' }, '未开通'),
  },
  { prop: 'paid', label: '实付 / 手续费', minWidth: 170, sortable: 'custom', formatter: (row) => row.paid || '-' },
  {
    prop: 'profit',
    label: '利润',
    width: 100,
    sortable: 'custom',
    formatter: (row) =>
      h('span', { style: 'color: var(--el-color-success); font-weight: 600' }, `￥${row.profit}`),
  },
  {
    prop: 'status',
    label: '状态',
    width: 100,
    sortable: 'custom',
    formatter: (row) =>
      h(ElTag, { type: statusType(row.status), size: 'small', effect: 'light' }, () => row.status),
  },
  {
    prop: 'operation',
    label: '操作',
    width: 90,
    fixed: 'right',
    formatter: (row) =>
      row.status === '已支付'
        ? h(ArtButtonTable, {
            icon: 'ri:refund-2-line',
            iconClass: 'bg-error/12 text-error',
            onClick: () => openRefund(row),
          })
        : h('span', { style: 'color: var(--art-gray-400); font-size: 11px' }, '-'),
  },
])

const pagination = computed(() => ({ current: page.value, size: per, total: total.value }))

// 退款
const refundDialog = ref(false)
const refundRow = ref<OrderItem | null>(null)
const refundAmount = ref('')
const refundMethod = ref('balance')
const refundReason = ref('')
const refunding = ref(false)

async function load() {
  loading.value = true
  try {
    const res = await fetchOrders(
      searchForm.value.q.trim(),
      page.value,
      sortKey.value || undefined,
      sortKey.value ? sortOrder.value : undefined,
    )
    list.value = res.list
    total.value = res.total
    profit.value = res.profit
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '查询失败')
  } finally {
    loading.value = false
  }
}
onMounted(load)

function handleSearch() {
  page.value = 1
  load()
}

function handleReset() {
  searchForm.value = { q: '' }
  page.value = 1
  load()
}

function handleCurrentChange(p: number) {
  page.value = p
  load()
}

function openRefund(row: OrderItem) {
  refundRow.value = row
  refundAmount.value = row.amount
  refundMethod.value = 'balance'
  refundReason.value = ''
  refundDialog.value = true
}

async function doRefund() {
  if (!refundRow.value) return
  const amount = refundAmount.value.trim()
  if (!/^\d+(\.\d{1,2})?$/.test(amount) || Number(amount) <= 0) {
    ElMessage.warning('请输入有效退款金额（最多两位小数且大于 0）')
    return
  }
  refunding.value = true
  try {
    await refundOrder(refundRow.value.id, amount, refundReason.value, refundMethod.value)
    ElMessage.success('退款已执行')
    refundDialog.value = false
    await load()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '退款失败')
  } finally {
    refunding.value = false
  }
}
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
          <span class="admin-profit-badge"
            >本页利润 <strong>￥{{ profit }}</strong></span
          >
        </template>
      </ArtTableHeader>

      <ArtTable
        :loading="loading"
        :data="list"
        :columns="columns"
        :pagination="pagination"
        @sort-change="handleSortChange"
        @pagination:current-change="handleCurrentChange"
      />
    </ElCard>

    <el-dialog v-model="refundDialog" title="订单退款" width="460px">
      <p class="admin-dialog-warn">
        退款会记录流水且不可撤销。订单 #{{ refundRow?.id }} · {{ refundRow?.email }} · 订单金额 ￥{{ refundRow?.amount }}
      </p>
      <p v-if="refundRow?.service_name" class="admin-dialog-svc">
        关联服务：<b>{{ refundRow.service_name }}</b>
        <span v-if="refundRow.service_host">（{{ refundRow.service_host }}）</span>
        · {{ refundRow.service_status }}
        <br />
        退款只退钱，不会停机或删除该服务；如需处理请到「服务实例」页操作。
      </p>
      <p v-else class="admin-dialog-svc">该订单尚未开通服务，无关联实例。</p>
      <el-form label-position="top">
        <el-form-item label="退款金额"><el-input v-model="refundAmount" placeholder="金额" /></el-form-item>
        <el-form-item label="退款方式"><el-radio-group v-model="refundMethod"><el-radio value="balance">退余额</el-radio><el-radio value="gateway">线下/手动</el-radio></el-radio-group></el-form-item>
        <el-form-item label="退款原因（可选）"><el-input v-model="refundReason" placeholder="如 用户申请" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="refundDialog = false">取消</el-button><el-button type="danger" :loading="refunding" @click="doRefund">执行退款</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.admin-profit-badge {
  color: var(--art-gray-600);
  font-size: 13px;
}
.admin-profit-badge strong {
  color: var(--el-color-success);
}
.admin-dialog-warn {
  margin: 0 0 13px;
  padding: 10px 12px;
  color: var(--el-color-warning);
  font-size: 12px;
  line-height: 1.7;
  background: var(--el-color-warning-light-9);
  border-radius: 8px;
}
.admin-dialog-svc {
  margin: 0 0 13px;
  padding: 10px 12px;
  color: var(--art-gray-600);
  font-size: 12px;
  line-height: 1.8;
  background: var(--el-fill-color-light);
  border-radius: 8px;
}
.admin-dialog-svc b {
  color: var(--art-gray-900);
}
</style>
