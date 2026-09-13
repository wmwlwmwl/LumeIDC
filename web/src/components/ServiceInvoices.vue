<script setup lang="ts">
import { computed, h } from 'vue'
import { Document } from '@element-plus/icons-vue'
import { ElTag, ElButton } from 'element-plus'
import type { ColumnOption } from '@/types'
import type { ServiceInvoice } from '../api/user'
import { formatDate, formatMoney } from '@/utils/format'

const props = defineProps<{
  loading: boolean
  invoices: ServiceInvoice[]
}>()

const emit = defineEmits<{
  pay: [id: number]
}>()

const columns = computed<ColumnOption<ServiceInvoice>[]>(() => [
  {
    prop: 'no',
    label: '账单号',
    minWidth: 150,
    formatter: (row) =>
      h('span', { style: 'color:var(--art-gray-800);font-family:ui-monospace,SFMono-Regular,Consolas,monospace' }, row.no),
  },
  {
    label: '类型',
    width: 90,
    formatter: (row) => (row.kind === 'order' ? '订单' : row.kind === 'recharge' ? '充值' : row.kind || '-'),
  },
  {
    label: '金额',
    width: 110,
    formatter: (row) => h('span', { style: 'color:var(--art-gray-900);font-weight:600' }, `￥${formatMoney(row.amount)}`),
  },
  {
    label: '状态',
    width: 90,
    formatter: (row) =>
      h(
        ElTag,
        { type: row.status === '已支付' ? 'success' : row.status === '未支付' ? 'warning' : 'info', size: 'small', effect: 'light' },
        () => row.status,
      ),
  },
  { label: '截止时间', width: 150, formatter: (row) => (row.due_at ? formatDate(row.due_at) : '-') },
  { label: '创建时间', width: 150, formatter: (row) => formatDate(row.created_at) },
  {
    label: '操作',
    width: 90,
    formatter: (row) =>
      row.status === '未支付'
        ? h(ElButton, { size: 'small', text: true, type: 'primary', onClick: () => emit('pay', row.id) }, () => '去支付')
        : h('span', { class: 'sd-invoice-muted' }, '-'),
  },
])

const fitHeight = computed(() => Math.min(420, Math.max(120, props.invoices.length * 50 + 46)))
</script>

<template>
  <div class="service-invoices">
    <div class="invoice-heading">
      <div class="invoice-heading__title">
        <span class="invoice-heading__mark"><el-icon><Document /></el-icon></span>
        <div>
          <h2>账单记录</h2>
          <p>该服务关联的订单、续费与升降级账单</p>
        </div>
      </div>
      <span class="invoice-count">共 {{ invoices.length }} 笔</span>
    </div>

    <ArtTable
      :loading="loading"
      :data="invoices"
      :columns="columns"
      :show-table-header="false"
      :height="fitHeight"
      :empty-height="'160px'"
      empty-text="该服务暂无账单记录"
      size="small"
    />
  </div>
</template>

<style scoped>
.service-invoices { min-width: 0; padding: 22px 24px 24px; }
.invoice-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 18px; }
.invoice-heading__title { display: flex; align-items: center; gap: 11px; min-width: 0; }
.invoice-heading__mark { display: inline-flex; align-items: center; justify-content: center; width: 34px; height: 34px; color: var(--theme-color); background: var(--theme-color-soft); border-radius: var(--radius-md); }
.invoice-heading h2 { margin: 0; color: var(--art-gray-900); font-size: 15px; font-weight: 600; }
.invoice-heading p { margin: 4px 0 0; color: var(--art-gray-500); font-size: 11px; }
.invoice-count { flex: 0 0 auto; padding: 5px 10px; color: var(--art-gray-500); font-size: 11px; background: var(--art-gray-50); border: 1px solid var(--art-card-border); border-radius: var(--radius-sm); }
.sd-invoice-muted { color: var(--art-gray-500); }
@media (max-width: 640px) { .service-invoices { padding: 16px 14px 18px; } .invoice-heading { align-items: flex-start; } .invoice-heading p { max-width: 220px; line-height: 1.5; } }
</style>
