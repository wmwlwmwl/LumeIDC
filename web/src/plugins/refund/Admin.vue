<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox, ElButton, ElTag } from 'element-plus'
import type { ColumnOption } from '@/types'
import ArtStatsCard from '@/components/core/cards/art-stats-card/index.vue'
import { http } from '@/http'
import { useAdminRequest } from '@/admin/useAdminTable'

interface RefundRow {
  id: number
  order_id: number
  user_id: number
  user_name?: string
  user_email?: string
  amount: string
  reason?: string
  detail?: string
  method?: string
  method_label?: string
  status: string
  status_label?: string
  handle_note?: string
  refund_id?: number
  order_amount?: string
  order_cycle?: string
  order_paid_at?: string
  handled_at?: string
  created_at?: string
  updated_at?: string
  handled_by?: number
}

interface Stats {
  pending: number
  approved: number
  rejected: number
}

interface ProductRule {
  id: number
  product_id: number
  product_name?: string
  refund_requirement: string
  window_type: string
  window_value: number
  refund_rule: string
  refund_type: string
  review_mode: string
  auto_approve_max: number
  gateway_fee_rate: number
  gateway_fee_min: number
  post_refund_action: string
}

const STATUS_TAG: Record<string, 'info' | 'warning' | 'success' | 'danger'> = {
  pending: 'warning',
  approved: 'success',
  rejected: 'danger',
  withdrawn: 'info',
}

const CYCLE_LABELS: Record<string, string> = {
  monthly: '按月',
  quarterly: '按季',
  yearly: '按年',
}

const statusOptions = [
  { value: 'pending', label: '待审核' },
  { value: 'approved', label: '已通过' },
  { value: 'rejected', label: '已驳回' },
  { value: 'withdrawn', label: '已撤回' },
]

const REQUIREMENT_LABELS: Record<string, string> = {
  unlimited: '不限制',
  first_order: '首次订购',
  first_order_of_product: '该商品首次订单',
}

const RULE_LABELS: Record<string, string> = {
  daily: '按天退款',
  full: '全额退款',
}

const TYPE_LABELS: Record<string, string> = {
  balance: '仅余额退款',
  balance_gateway: '余额+原路退款',
  gateway_record: '原路退款仅记录',
}

const REVIEW_LABELS: Record<string, string> = {
  manual: '需要审核',
  auto: '全自动',
}

const ACTION_LABELS: Record<string, string> = {
  none: '无操作',
  suspend: '暂停产品',
  terminate: '终止产品',
}

const activeTab = ref<'requests' | 'rules'>('requests')

// ---- 退款申请列表 ----
const startListRequest = useAdminRequest()
const startStatsRequest = useAdminRequest()

const showSearchBar = ref(true)
const loading = ref(false)
const list = ref<RefundRow[]>([])
const total = ref(0)
const page = ref(1)
const limit = ref(10)
const searchForm = ref<Record<string, any>>({})
const stats = ref<Stats>({ pending: 0, approved: 0, rejected: 0 })

const drawerVisible = ref(false)
const current = ref<RefundRow | null>(null)
const handleNote = ref('')
const processing = ref(false)

const searchItems = computed(() => [
  { label: '关键词', key: 'keyword', type: 'input', placeholder: '订单号 / 用户 / 原因' },
  {
    label: '状态',
    key: 'status',
    type: 'select',
    props: { placeholder: '全部状态', clearable: true, options: statusOptions },
  },
])

const pagination = computed(() => ({ current: page.value, size: limit.value, total: total.value }))

const columns = ref<ColumnOption<RefundRow>[]>([
  { prop: 'id', label: 'ID', width: 70, sortable: true },
  {
    prop: 'user_name',
    label: '用户',
    minWidth: 140,
    formatter: (row) =>
      h('div', { class: 'flex flex-col leading-tight' }, [
        h('span', { class: 'text-sm' }, row.user_name || `#${row.user_id}`),
        row.user_email ? h('span', { class: 'text-xs text-g-500' }, row.user_email) : null,
      ]),
  },
  {
    prop: 'order_id',
    label: '订单',
    width: 90,
    formatter: (row) => `#${row.order_id}`,
  },
  {
    prop: 'amount',
    label: '退款金额',
    width: 110,
    sortable: true,
    formatter: (row) => `¥${row.amount}`,
  },
  {
    prop: 'method',
    label: '方式',
    width: 100,
    formatter: (row) => row.method_label || row.method || '—',
  },
  {
    prop: 'reason',
    label: '原因',
    minWidth: 120,
    formatter: (row) => row.reason || '—',
  },
  {
    prop: 'status',
    label: '状态',
    width: 100,
    formatter: (row) =>
      h(
        ElTag,
        { type: STATUS_TAG[row.status] || 'info', effect: 'light' },
        row.status_label || row.status
      ),
  },
  { prop: 'created_at', label: '申请时间', width: 160, sortable: true },
  {
    prop: 'actions',
    label: '操作',
    width: 110,
    fixed: 'right',
    formatter: (row) =>
      h(
        ElButton,
        {
          size: 'small',
          link: true,
          type: 'primary',
          onClick: () => openReview(row),
        },
        row.status === 'pending' ? '审核' : '详情'
      ),
  },
])

async function loadStats() {
  const isCurrent = startStatsRequest()
  if (!isCurrent) return
  try {
    const res = await http.get<{ ok: number; stats?: Stats }>('/plugin/refund/stats')
    if (!isCurrent()) return
    if (res.stats) stats.value = res.stats
  } catch {
    /* 统计失败不影响列表展示 */
  }
}

async function loadList() {
  const isCurrent = startListRequest()
  if (!isCurrent) return
  loading.value = true
  try {
    const res = await http.get<{ ok: number; list?: RefundRow[]; total?: number }>(
      '/plugin/refund/list',
      { ...searchForm.value, page: page.value, limit: limit.value }
    )
    if (!isCurrent()) return
    list.value = res.list || []
    total.value = res.total || 0
  } catch (err: unknown) {
    if (isCurrent()) ElMessage.error((err as Error).message || '读取失败')
  } finally {
    if (isCurrent()) loading.value = false
  }
}

function handleSearch(params: Record<string, any>) {
  searchForm.value = { ...params }
  page.value = 1
  loadList()
}

function handleReset() {
  searchForm.value = {}
  page.value = 1
  loadList()
}

function handlePageChange(p: number) {
  page.value = p
  loadList()
}

function handleSizeChange(s: number) {
  limit.value = s
  page.value = 1
  loadList()
}

function openReview(row: RefundRow) {
  current.value = row
  handleNote.value = ''
  drawerVisible.value = true
}

/** 配置表单由插件壳渲染在本页顶部，此处引导管理员回到顶部编辑退款/通知设置。 */
function scrollToConfig() {
  document.getElementById('app-main')?.scrollTo({ top: 0, behavior: 'smooth' })
}

async function approve() {
  if (!current.value) return
  const row = current.value
  const ok = await ElMessageBox.confirm(
    `确认通过订单 #${row.order_id} 的退款申请（${row.amount} 元）？通过后将由系统自动执行退款。`,
    '通过确认',
    { type: 'warning', confirmButtonText: '通过并退款', cancelButtonText: '取消' }
  ).catch(() => null)
  if (!ok) return
  processing.value = true
  try {
    const res = await http.post<{ ok: number; msg?: string }>(`/plugin/refund/${row.id}/approve`)
    if (String(res.ok) !== '1') {
      ElMessage.error(res.msg || '操作失败')
      return
    }
    ElMessage.success('已通过并完成退款')
    drawerVisible.value = false
    loadList()
    loadStats()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  } finally {
    processing.value = false
  }
}

async function reject() {
  if (!current.value) return
  if (!handleNote.value.trim()) {
    ElMessage.error('请填写处理备注，将展示给用户')
    return
  }
  const row = current.value
  const ok = await ElMessageBox.confirm(
    `确认驳回订单 #${row.order_id} 的退款申请？`,
    '驳回确认',
    { type: 'warning', confirmButtonText: '驳回', cancelButtonText: '取消' }
  ).catch(() => null)
  if (!ok) return
  processing.value = true
  try {
    const res = await http.post<{ ok: number; msg?: string }>(`/plugin/refund/${row.id}/reject`, {
      handle_note: handleNote.value.trim(),
    })
    if (String(res.ok) !== '1') {
      ElMessage.error(res.msg || '操作失败')
      return
    }
    ElMessage.success('已驳回')
    drawerVisible.value = false
    loadList()
    loadStats()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '操作失败')
  } finally {
    processing.value = false
  }
}

// ---- 商品退款规则 ----
const rulesLoading = ref(false)
const rules = ref<ProductRule[]>([])
const products = ref<{ id: number; name: string }[]>([])
const ruleDialogVisible = ref(false)
const ruleForm = ref<Record<string, any>>({})
const ruleSaving = ref(false)

async function loadRules() {
  rulesLoading.value = true
  try {
    const res = await http.get<{ ok: number; list?: ProductRule[] }>('/plugin/refund/product-rules')
    rules.value = res.list || []
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '读取规则失败')
  } finally {
    rulesLoading.value = false
  }
}

async function loadProducts() {
  try {
    const res = await http.get<{ ok: number; list?: { id: number; name: string }[] }>('/plugin/refund/products')
    products.value = res.list || []
  } catch {
    products.value = []
  }
}

function newRuleForm(): Record<string, any> {
  return {
    id: 0,
    product_id: 0,
    refund_requirement: 'unlimited',
    window_type: 'hours',
    window_value: 0,
    refund_rule: 'daily',
    refund_type: 'balance',
    review_mode: 'manual',
    auto_approve_max: 0,
    gateway_fee_rate: 0,
    gateway_fee_min: 0,
    post_refund_action: 'none',
  }
}

function openRuleDialog(row?: ProductRule) {
  if (row) {
    // 后端 numeric 以字符串返回，el-input-number 需要 number
    ruleForm.value = {
      ...row,
      auto_approve_max: Number(row.auto_approve_max) || 0,
      gateway_fee_rate: Number(row.gateway_fee_rate) || 0,
      gateway_fee_min: Number(row.gateway_fee_min) || 0,
    }
  } else {
    ruleForm.value = newRuleForm()
  }
  ruleDialogVisible.value = true
}

async function saveRule() {
  ruleSaving.value = true
  try {
    const res = await http.post<{ ok: number; id?: number; msg?: string }>(
      '/plugin/refund/product-rules',
      ruleForm.value
    )
    if (String(res.ok) !== '1') {
      ElMessage.error(res.msg || '保存失败')
      return
    }
    ElMessage.success('保存成功')
    ruleDialogVisible.value = false
    loadRules()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    ruleSaving.value = false
  }
}

async function deleteRule(row: ProductRule) {
  const ok = await ElMessageBox.confirm(
    `确认删除「${row.product_name || '全局默认'}」的退款规则？`,
    '删除确认',
    { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
  ).catch(() => null)
  if (!ok) return
  try {
    const delRes = await http.delete<{ ok: number; msg?: string }>(`/plugin/refund/product-rules/${row.id}`)
    if (String(delRes.ok) !== '1') {
      ElMessage.error(delRes.msg || '删除失败')
      return
    }
    ElMessage.success('已删除')
    loadRules()
  } catch (err: unknown) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}

function ruleWindowText(row: ProductRule) {
  if (!row.window_value) return '不限'
  return `${row.window_value}${row.window_type === 'days' ? ' 天' : ' 小时'}`
}

function onTabChange(tab: string | number) {
  activeTab.value = tab as 'requests' | 'rules'
  if (tab === 'rules' && rules.value.length === 0) {
    loadRules()
    loadProducts()
  }
}

onMounted(() => {
  loadStats()
  loadList()
})
</script>

<template>
  <div class="art-full-height">
    <ElCard class="art-card">
      <template #header>
        <div class="art-card-header">
          <div class="title">
            <h4>退款审核</h4>
            <p>审核用户提交的退款申请，通过后由系统自动执行退款。</p>
          </div>
          <el-button link type="primary" @click="scrollToConfig">退款设置 / 通知设置</el-button>
        </div>
      </template>

      <div class="refund-tabs">
        <button
          class="refund-tab"
          :class="{ active: activeTab === 'requests' }"
          @click="activeTab = 'requests'"
        >退款申请</button>
        <button
          class="refund-tab"
          :class="{ active: activeTab === 'rules' }"
          @click="onTabChange('rules')"
        >商品规则</button>
      </div>

      <!-- 退款申请列表 -->
      <div v-show="activeTab === 'requests'">
        <ArtSearchBar
          v-show="showSearchBar"
          v-model="searchForm"
          :items="searchItems"
          @search="handleSearch"
          @reset="handleReset"
        />

        <ElRow :gutter="20" class="mb-5">
          <ElCol :xs="24" :sm="8">
            <ArtStatsCard
              icon="ri:time-line"
              icon-style="bg-warning"
              title="待审核"
              :count="stats.pending"
              description="等待人工处理的申请"
            />
          </ElCol>
          <ElCol :xs="24" :sm="8">
            <ArtStatsCard
              icon="ri:checkbox-circle-line"
              icon-style="bg-primary"
              title="已通过"
              :count="stats.approved"
              description="已通过并完成退款"
            />
          </ElCol>
          <ElCol :xs="24" :sm="8">
            <ArtStatsCard
              icon="ri:close-circle-line"
              icon-style="bg-danger"
              title="已驳回"
              :count="stats.rejected"
              description="已驳回的申请"
            />
          </ElCol>
        </ElRow>

        <ElCard class="art-table-card" :style="{ marginTop: showSearchBar ? '12px' : '0' }">
          <ArtTableHeader
            v-model:columns="columns"
            v-model:showSearchBar="showSearchBar"
            :loading="loading"
            @refresh="loadList"
          />
          <ArtTable
            :loading="loading"
            :data="list"
            :columns="columns"
            :pagination="pagination"
            :pagination-options="{ pageSizes: [10, 20, 50] }"
            empty-text="暂无退款申请"
            @pagination:current-change="handlePageChange"
            @pagination:size-change="handleSizeChange"
          />
        </ElCard>
      </div>

      <!-- 商品退款规则 -->
      <div v-show="activeTab === 'rules'">
        <div class="rules-toolbar">
          <p class="rules-tip">
            为单个商品配置独立的退款规则（期限、类型、审核模式、退款后产品操作等）；未配置的商品回退到全局默认规则，未设全局规则则使用插件配置。
          </p>
          <el-button type="primary" @click="openRuleDialog()">新增规则</el-button>
        </div>
        <ElCard class="art-table-card" :style="{ marginTop: '12px' }">
          <el-table :data="rules" v-loading="rulesLoading" stripe empty-text="暂无规则，点击「新增规则」开始配置">
            <el-table-column prop="product_name" label="商品" min-width="160">
              <template #default="{ row }">
                <el-tag v-if="row.product_id === 0" type="primary" effect="light">全局默认</el-tag>
                <span v-else>{{ row.product_name }}</span>
              </template>
            </el-table-column>
            <el-table-column label="退款要求" width="140">
              <template #default="{ row }">{{ REQUIREMENT_LABELS[row.refund_requirement] || row.refund_requirement }}</template>
            </el-table-column>
            <el-table-column label="可退期限" width="100">
              <template #default="{ row }">{{ ruleWindowText(row) }}</template>
            </el-table-column>
            <el-table-column label="退款规则" width="100">
              <template #default="{ row }">{{ RULE_LABELS[row.refund_rule] || row.refund_rule }}</template>
            </el-table-column>
            <el-table-column label="退款类型" width="130">
              <template #default="{ row }">{{ TYPE_LABELS[row.refund_type] || row.refund_type }}</template>
            </el-table-column>
            <el-table-column label="审核模式" width="100">
              <template #default="{ row }">{{ REVIEW_LABELS[row.review_mode] || row.review_mode }}</template>
            </el-table-column>
            <el-table-column label="退款后操作" width="100">
              <template #default="{ row }">{{ ACTION_LABELS[row.post_refund_action] || row.post_refund_action }}</template>
            </el-table-column>
            <el-table-column label="操作" width="120" fixed="right">
              <template #default="{ row }">
                <el-button size="small" link type="primary" @click="openRuleDialog(row)">编辑</el-button>
                <el-button size="small" link type="danger" @click="deleteRule(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </ElCard>
      </div>
    </ElCard>

    <el-drawer v-model="drawerVisible" title="退款申请详情" size="620px">
      <template v-if="current">
        <el-descriptions :column="1" border class="refund-detail">
          <el-descriptions-item label="申请 ID">#{{ current.id }}</el-descriptions-item>
          <el-descriptions-item label="用户">
            {{ current.user_name || `#${current.user_id}` }}
            <span v-if="current.user_email" class="text-g-500">（{{ current.user_email }}）</span>
          </el-descriptions-item>
          <el-descriptions-item label="订单">
            #{{ current.order_id }}
            <span v-if="current.order_amount" class="text-g-500">
              · 订单金额 ¥{{ current.order_amount }}
            </span>
            <span v-if="current.order_cycle" class="text-g-500">
              · {{ CYCLE_LABELS[current.order_cycle] || current.order_cycle }}
            </span>
            <span v-if="current.order_paid_at" class="text-g-500"> · 支付于 {{ current.order_paid_at }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="退款金额">¥{{ current.amount }}</el-descriptions-item>
          <el-descriptions-item label="退款方式">
            {{ current.method_label || current.method || '—' }}
          </el-descriptions-item>
          <el-descriptions-item label="退款原因">{{ current.reason || '—' }}</el-descriptions-item>
          <el-descriptions-item label="问题描述">
            <span class="detail-text">{{ current.detail || '—' }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="申请时间">{{ current.created_at }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <ElTag :type="STATUS_TAG[current.status] || 'info'" effect="light">
              {{ current.status_label || current.status }}
            </ElTag>
          </el-descriptions-item>
          <el-descriptions-item v-if="current.handled_at" label="处理时间">
            {{ current.handled_at }}
          </el-descriptions-item>
          <el-descriptions-item v-if="current.refund_id" label="关联退款记录">
            #{{ current.refund_id }}
          </el-descriptions-item>
          <el-descriptions-item v-if="current.handle_note" label="处理备注">
            <span class="detail-text">{{ current.handle_note }}</span>
          </el-descriptions-item>
        </el-descriptions>

        <el-form v-if="current.status === 'pending'" label-position="top" class="review-form" @submit.prevent>
          <el-form-item label="处理备注">
            <el-input
              v-model="handleNote"
              type="textarea"
              :rows="4"
              maxlength="500"
              show-word-limit
              placeholder="驳回时必填，将展示给用户；通过时选填。"
            />
          </el-form-item>
          <div class="form-actions">
            <el-button type="primary" :loading="processing" @click="approve">通过并退款</el-button>
            <el-button type="danger" :loading="processing" @click="reject">驳回</el-button>
            <el-button :disabled="processing" @click="drawerVisible = false">关闭</el-button>
          </div>
        </el-form>
        <div v-else class="form-actions">
          <el-button @click="drawerVisible = false">关闭</el-button>
        </div>
      </template>
    </el-drawer>

    <!-- 商品规则编辑弹窗 -->
    <el-dialog
      v-model="ruleDialogVisible"
      :title="ruleForm.id ? '编辑退款规则' : '新增退款规则'"
      width="560px"
    >
      <el-form :model="ruleForm" label-width="130px" label-position="right">
        <el-form-item label="适用商品">
          <el-select v-model="ruleForm.product_id" placeholder="选择商品" style="width: 100%">
            <el-option :value="0" label="全局默认（未配置规则的商品使用）" />
            <el-option
              v-for="p in products"
              :key="p.id"
              :value="p.id"
              :label="p.name"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="退款要求">
          <el-radio-group v-model="ruleForm.refund_requirement">
            <el-radio value="unlimited">不限制</el-radio>
            <el-radio value="first_order">首次订购</el-radio>
            <el-radio value="first_order_of_product">该商品首次订单</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="可退期限">
          <el-input-number v-model="ruleForm.window_value" :min="0" style="width: 120px" />
          <el-select v-model="ruleForm.window_type" style="width: 100px; margin-left: 8px">
            <el-option value="hours" label="小时" />
            <el-option value="days" label="天" />
          </el-select>
          <span class="text-g-500 ml-2">0 表示不限</span>
        </el-form-item>
        <el-form-item label="退款规则">
          <el-radio-group v-model="ruleForm.refund_rule">
            <el-radio value="daily">按天退款</el-radio>
            <el-radio value="full">全额退款</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="退款类型">
          <el-select v-model="ruleForm.refund_type" style="width: 100%">
            <el-option value="balance" label="仅余额退款" />
            <el-option value="balance_gateway" label="余额+原路退款" />
            <el-option value="gateway_record" label="原路退款仅记录" />
          </el-select>
        </el-form-item>
        <el-form-item label="审核模式">
          <el-radio-group v-model="ruleForm.review_mode">
            <el-radio value="manual">需要审核</el-radio>
            <el-radio value="auto">全自动</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="ruleForm.review_mode === 'auto'" label="自动通过阈值">
          <el-input-number v-model="ruleForm.auto_approve_max" :min="0" :precision="2" style="width: 160px">
            <template #append>元</template>
          </el-input-number>
        </el-form-item>
        <el-form-item label="原路退款手续费">
          <el-input-number v-model="ruleForm.gateway_fee_rate" :min="0" :max="100" :precision="2" style="width: 160px">
            <template #append>%</template>
          </el-input-number>
          <span class="text-g-500 mx-2">最低</span>
          <el-input-number v-model="ruleForm.gateway_fee_min" :min="0" :precision="2" style="width: 160px">
            <template #append>元</template>
          </el-input-number>
        </el-form-item>
        <el-form-item label="退款后产品操作">
          <el-select v-model="ruleForm.post_refund_action" style="width: 100%">
            <el-option value="none" label="无操作" />
            <el-option value="suspend" label="暂停产品" />
            <el-option value="terminate" label="终止产品" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="ruleDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="ruleSaving" @click="saveRule">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.refund-detail {
  margin-bottom: 8px;
}

.refund-tabs {
  margin-bottom: 16px;
  display: flex;
  gap: 4px;
  border-bottom: 1px solid var(--art-gray-200, #e4e7ed);
}

.refund-tab {
  padding: 10px 20px;
  background: none;
  border: none;
  border-bottom: 2px solid transparent;
  cursor: pointer;
  font-size: 14px;
  color: var(--art-gray-600, #606266);
  transition: all 0.2s;
  margin-bottom: -1px;
}

.refund-tab:hover {
  color: var(--art-primary, #409eff);
}

.refund-tab.active {
  color: var(--art-primary, #409eff);
  border-bottom-color: var(--art-primary, #409eff);
  font-weight: 600;
}

.rules-toolbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 8px;
}

.rules-tip {
  margin: 0;
  font-size: 13px;
  color: var(--art-gray-500, #909399);
  line-height: 1.6;
}

.detail-text {
  white-space: pre-wrap;
  word-break: break-word;
}

.review-form {
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--art-gray-200, rgba(0, 0, 0, 0.06));
}

.form-actions {
  display: flex;
  gap: 12px;
}

@media (max-width: 600px) {
  :deep(.el-drawer) {
    width: 100% !important;
  }
}
</style>
